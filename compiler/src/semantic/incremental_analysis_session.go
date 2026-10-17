package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/incremental"
	"elisacore/src/lexer"
)

// IncrementalAnalysisSession is an opt-in, in-process semantic session. The
// initial body class is closed integer-return expressions. It reuses primary
// expression typing; return checks, declarations, summaries and CFGs stay fresh.
// It is not a complete incremental compiler or a persistent whole-body cache.
type IncrementalAnalysisSession struct {
	mu             sync.Mutex
	db             *incremental.Database
	previous       map[incremental.Key]bool
	stats          IncrementalAnalysisStats
	retiredQueries int
}
type IncrementalAnalysisStats struct {
	Analyses, PrimaryBodiesChecked, PrimaryBodiesReused, ReusedExpressionNodes uint64
	PrimaryValueChecks, PrimaryExprChecks                                      uint64
	PrimaryElementScans                                                        uint64
	DatabaseResets                                                             uint64
	ColdFallbackAnalyses                                                       uint64
	Queries                                                                    incremental.Stats
}
type primitiveBodyProduct struct{ Source, Config, Safety, Types string }
type incrementalBodyInvocation struct {
	session        *IncrementalAnalysisSession
	keys           map[*ast.FuncDecl]string
	changes        []incremental.Change
	config, safety string
	tracking       bool
}

var primitiveConfigKey = incremental.Key{Kind: "semantic-config", Name: "primitive-return-v1"}
var primitiveSafetyKey = incremental.Key{Kind: "semantic-lookup-set", Name: "closed-integer-operators-v1"}
var errPrimitiveProductMiss = errors.New("primitive typing product requires cold analysis")

func primitiveInput(kind, name string) incremental.Key {
	return incremental.Key{Kind: kind, Name: name}
}

func NewIncrementalAnalysisSession() *IncrementalAnalysisSession {
	session := &IncrementalAnalysisSession{previous: map[incremental.Key]bool{}}
	session.resetDatabase()
	return session
}
func (session *IncrementalAnalysisSession) resetDatabase() {
	session.db = incremental.New(map[string]incremental.Evaluator{"primitive-return": func(reader *incremental.Reader, name string) (string, error) {
		source, _ := reader.Input(primitiveInput("function-source", name))
		config, _ := reader.Input(primitiveConfigKey)
		safety, _ := reader.Input(primitiveSafetyKey)
		encoded, ok := reader.Input(primitiveInput("function-product", name))
		if !ok || safety != "canonical-primitives-no-impls" {
			return "", errPrimitiveProductMiss
		}
		var product primitiveBodyProduct
		if err := json.Unmarshal([]byte(encoded), &product); err != nil {
			return "", errPrimitiveProductMiss
		}
		if product.Source != source || product.Config != config || product.Safety != safety {
			return "", errPrimitiveProductMiss
		}
		if !validPrimitiveTyping(product.Types) {
			return "", errPrimitiveProductMiss
		}
		return product.Types, nil
	}})
}
func (session *IncrementalAnalysisSession) Stats() IncrementalAnalysisStats {
	session.mu.Lock()
	defer session.mu.Unlock()
	stats := session.stats
	if session.db != nil {
		stats.Queries = session.db.Stats()
	}
	return stats
}
func (session *IncrementalAnalysisSession) Analyze(file *ast.File, options AnalyzeOptions) *Result {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.stats.Analyses++
	if !primitiveResultSchemaValid || !primitiveSchemaValid {
		return AnalyzeWithOptions(file, options)
	}
	if session.db == nil {
		session.resetDatabase()
	}
	config, err := json.Marshal(struct {
		Options        AnalyzeOptions
		LinearMutation string
		NoAutoReserve  bool
		OS, Arch       string
	}{options, os.Getenv("ELISA_LINEAR_MUTATION"), autoReserveDisabledSem, runtime.GOOS, runtime.GOARCH})
	if err != nil {
		return AnalyzeWithOptions(file, options)
	}
	digest := sha256.Sum256(config)
	invocation := &incrementalBodyInvocation{session: session, keys: map[*ast.FuncDecl]string{}, config: hex.EncodeToString(digest[:])}
	invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveConfigKey, Value: invocation.config})
	current := map[incremental.Key]bool{}
	origin := file.Filename
	if len(origin) > primitiveSourceByteBudget {
		return AnalyzeWithOptions(file, options)
	}
	if absolute, err := filepath.Abs(origin); err == nil {
		origin = absolute
	}
	var collect func([]ast.Decl, string, int)
	collect = func(decls []ast.Decl, namespace string, depth int) {
		if depth > 128 {
			return
		}
		for _, decl := range decls {
			if nested, ok := decl.(*ast.NamespaceDecl); ok {
				if len(nested.Name) > primitiveSourceByteBudget-len(namespace)-1 {
					continue
				}
				collect(nested.Decls, joinQualifiedName(namespace, nested.Name), depth+1)
				continue
			}
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			_, ok = primitiveReturnHeader(fn)
			if !ok {
				continue
			}
			keyBytes := []byte("primitive/noargs/i64/v1\x00")
			qualifiedName := joinQualifiedName(namespace, fn.Name)
			if len(qualifiedName) > primitiveSourceByteBudget {
				continue
			}
			keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(origin)))
			keyBytes = append(keyBytes, origin...)
			keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(qualifiedName)))
			keyBytes = append(keyBytes, qualifiedName...)
			identity := sha256.Sum256(keyBytes)
			name := "primitive-v1:" + hex.EncodeToString(identity[:])
			invocation.keys[fn] = name
			key := primitiveInput("function-source", name)
			current[key] = true
		}
	}
	collect(file.Decls, "", 0)
	var removed []incremental.Key
	for key := range session.previous {
		if !current[key] {
			invocation.changes = append(invocation.changes, incremental.Change{Key: key, Remove: true})
			invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveInput("function-product", key.Name), Remove: true})
			removed = append(removed, incremental.Key{Kind: "primitive-return", Name: key.Name})
		}
	}
	session.previous = current
	session.db.Apply(invocation.changes...)
	for _, key := range removed {
		session.db.EvictQuery(key)
	}
	session.retiredQueries += len(removed)
	// Removed products are released immediately. Periodically discard their
	// small key tombstones as well; active program size is not capped here.
	if session.retiredQueries > 4096 {
		session.resetDatabase()
		var live []incremental.Change
		for _, change := range invocation.changes {
			if !change.Remove {
				live = append(live, change)
			}
		}
		session.db.Apply(live...)
		session.retiredQueries = 0
		session.stats.DatabaseResets++
	}
	if len(invocation.keys) == 0 {
		session.stats.ColdFallbackAnalyses++
		return AnalyzeWithOptions(file, options)
	}
	return analyzeWithBodyInvocation(file, options, invocation)
}
func (invocation *incrementalBodyInvocation) prepareAnalyzer(a *Analyzer) {
	invocation.safety = "blocked"
	intType, intOK := a.namedTypes["int"].(*BuiltinType)
	i64Type, i64OK := a.namedTypes["i64"].(*BuiltinType)
	if intOK && i64OK && intType.Name == "int" && i64Type.Name == "i64" && len(a.staticImpls) == 0 {
		invocation.safety = "canonical-primitives-no-impls"
	}
	invocation.session.db.Apply(incremental.Change{Key: primitiveSafetyKey, Value: invocation.safety})
}

func primitiveReturnHeader(fn *ast.FuncDecl) (*ast.ReturnStmt, bool) {
	if fn == nil || fn.Name == "" || len(fn.Name) > primitiveSourceByteBudget || len(fn.Body) != 1 {
		return nil, false
	}
	// Every other declaration feature stays cold, including future added fields.
	header := *fn
	if len(header.Params) != 0 {
		return nil, false
	}
	header.Params = nil // the parser uses a non-nil empty list for `()`
	header.Position = lexer.Pos{}
	header.Name = ""
	header.ReturnType = nil
	header.Body = nil
	if !reflect.ValueOf(header).IsZero() {
		return nil, false
	}
	switch typ := fn.ReturnType.(type) {
	case *ast.NamedType:
		copy := *typ
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if typ.Name != "i64" || !reflect.ValueOf(copy).IsZero() {
			return nil, false
		}
	case *ast.BuiltinTypeExpr:
		copy := *typ
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if len(copy.TypeArgs) != 0 || len(copy.ValueArgs) != 0 {
			return nil, false
		}
		copy.TypeArgs = nil
		copy.ValueArgs = nil
		if typ.Name != "i64" || !reflect.ValueOf(copy).IsZero() {
			return nil, false
		}
	default:
		return nil, false
	}
	stmt, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok || stmt.Value == nil {
		return nil, false
	}
	statementHeader := *stmt
	statementHeader.Position = lexer.Pos{}
	statementHeader.Value = nil
	if !reflect.ValueOf(statementHeader).IsZero() {
		return nil, false
	}
	return stmt, true
}

const primitiveSourceByteBudget = 1024 * 1024

var primitiveSeenPool = sync.Pool{New: func() any { return make(map[ast.Expr]bool, 256) }}

func primitiveReturnExpressions(fn *ast.FuncDecl) ([]ast.Expr, bool) {
	stmt, ok := primitiveReturnHeader(fn)
	if !ok || len(fn.Name) > primitiveSourceByteBudget {
		return nil, false
	}
	sourceBytes := len(fn.Name)
	var nodes []ast.Expr
	seen := primitiveSeenPool.Get().(map[ast.Expr]bool)
	defer func() { clear(seen); primitiveSeenPool.Put(seen) }()
	var visit func(ast.Expr, int) bool
	visit = func(expr ast.Expr, depth int) bool {
		if expr == nil || depth > 128 || len(nodes) >= 4096 {
			return false
		}
		if seen[expr] {
			return false
		}
		seen[expr] = true
		nodes = append(nodes, expr)
		switch node := expr.(type) {
		case *ast.IntLit:
			if len(node.Value) > primitiveSourceByteBudget-sourceBytes {
				return false
			}
			sourceBytes += len(node.Value)
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Value = ""
			copy.IsHex = false
			return node.Suffix == "" && reflect.ValueOf(copy).IsZero()
		case *ast.ParenExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Inner = nil
			return reflect.ValueOf(copy).IsZero() && visit(node.Inner, depth+1)
		case *ast.UnaryExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Op = 0
			copy.Operand = nil
			return node.Op == lexer.TOKEN_MINUS && reflect.ValueOf(copy).IsZero() && visit(node.Operand, depth+1)
		case *ast.BinaryExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Op = 0
			copy.Left = nil
			copy.Right = nil
			if !reflect.ValueOf(copy).IsZero() || (node.Op != lexer.TOKEN_PLUS && node.Op != lexer.TOKEN_MINUS && node.Op != lexer.TOKEN_STAR) {
				return false
			}
			return visit(node.Left, depth+1) && visit(node.Right, depth+1)
		default:
			return false
		}
	}
	if !visit(stmt.Value, 0) {
		return nil, false
	}
	return nodes, true
}

// The compact source protocol includes every non-position field admitted by
// primitiveReturnExpressions. Fixed node arities make preorder unambiguous;
// values are length-prefixed, and literal spelling/hex mode remain inputs.
func primitiveExpressionDigest(fn *ast.FuncDecl, nodes []ast.Expr) string {
	data := make([]byte, 0, len(nodes)*12+len(fn.Name)+32)
	data = append(data, "closed-i64-typing-v1\x00"...)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(fn.Name)))
	data = append(data, fn.Name...)
	for _, expr := range nodes {
		switch node := expr.(type) {
		case *ast.IntLit:
			data = append(data, 'I')
			if node.IsHex {
				data = append(data, 1)
			} else {
				data = append(data, 0)
			}
			data = binary.LittleEndian.AppendUint64(data, uint64(len(node.Value)))
			data = append(data, node.Value...)
		case *ast.ParenExpr:
			data = append(data, 'P')
		case *ast.UnaryExpr:
			data = append(data, 'U')
			data = binary.LittleEndian.AppendUint64(data, uint64(node.Op))
		case *ast.BinaryExpr:
			data = append(data, 'B')
			data = binary.LittleEndian.AppendUint64(data, uint64(node.Op))
		}
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func validPrimitiveTyping(types string) bool {
	if len(types) == 0 || len(types) > 4096 {
		return false
	}
	for i := range types {
		if types[i] != 'n' && types[i] != 'i' {
			return false
		}
	}
	return true
}

func (a *Analyzer) analyzeIncrementalFunctionBody(fn *ast.FuncDecl, typeArgs []Type) {
	invocation := a.incrementalBodies
	if invocation == nil || a.suppressDiagnostics || a.convergeDepth != 0 || len(typeArgs) != 0 {
		a.analyzeFunctionBodyStmts(fn.Body)
		return
	}
	priorTracking := invocation.tracking
	invocation.tracking = true
	defer func() { invocation.tracking = priorTracking }()
	key, indexed := invocation.keys[fn]
	if !indexed {
		invocation.session.stats.PrimaryBodiesChecked++
		a.analyzeFunctionBodyStmts(fn.Body)
		return
	}
	if indexed {
		invocation.prepareAnalyzer(a)
	} // validate current lookup absence at the reuse boundary
	currentReturn, returnOK := a.currentReturn.(*BuiltinType)
	nodes, eligible := primitiveReturnExpressions(fn)
	if !indexed || !eligible || invocation.safety != "canonical-primitives-no-impls" || !returnOK || currentReturn.Name != "i64" || currentReturn != a.namedTypes["i64"] {
		if indexed && !eligible {
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("function-source", key), Remove: true}, incremental.Change{Key: primitiveInput("function-product", key), Remove: true})
			invocation.session.db.EvictQuery(incremental.Key{Kind: "primitive-return", Name: key})
		}
		invocation.session.stats.PrimaryBodiesChecked++
		a.analyzeFunctionBodyStmts(fn.Body)
		return
	}
	session := invocation.session
	source := primitiveExpressionDigest(fn, nodes)
	sourceChange := incremental.Change{Key: primitiveInput("function-source", key), Value: source}
	session.db.Apply(sourceChange)
	query := incremental.Key{Kind: "primitive-return", Name: key}
	encoded, err := session.db.Evaluate(context.Background(), query)
	if err == nil && encoded != "" {
		if len(encoded) == len(nodes) && validPrimitiveTyping(encoded) {
			for i, expr := range nodes {
				name := "int"
				if encoded[i] == 'i' {
					name = "i64"
				}
				a.recordAnalyzedExprType(expr, a.namedTypes[name])
			}
			session.stats.PrimaryBodiesReused++
			session.stats.ReusedExpressionNodes += uint64(len(nodes))
			a.visitFunctionBodyWithElementScan(fn.Body, func(stmt ast.Stmt) {
				ret := stmt.(*ast.ReturnStmt)
				a.analyzeReturnStatement(ret, a.exprTypes[ret.Value])
			}, true)
			return
		}
		// Drop an unusable memo before the cold fallback. Other valid products stay.
		session.db.Apply(incremental.Change{Key: primitiveInput("function-product", key), Remove: true})
		session.db.EvictQuery(query)
	}
	session.stats.PrimaryBodiesChecked++
	diagnosticsBefore := len(a.diagnostics)
	a.analyzeFunctionBodyStmts(fn.Body)
	if len(a.diagnostics) != diagnosticsBefore {
		return
	}
	currentNodes, valid := primitiveReturnExpressions(fn)
	if !valid || primitiveExpressionDigest(fn, currentNodes) != source {
		return
	}
	types := make([]byte, len(nodes))
	for i, expr := range nodes {
		typ, ok := a.exprTypes[expr].(*BuiltinType)
		if !ok || a.exprFacts[expr].HasAnyFacts() || typ != a.namedTypes[typ.Name] {
			return
		}
		switch typ.Name {
		case "int":
			types[i] = 'n'
		case "i64":
			types[i] = 'i'
		default:
			return
		}
	}
	product, _ := json.Marshal(primitiveBodyProduct{Source: source, Config: invocation.config, Safety: invocation.safety, Types: string(types)})
	session.db.Apply(incremental.Change{Key: primitiveInput("function-product", key), Value: string(product)})
	_, _ = session.db.Evaluate(context.Background(), query)
}
