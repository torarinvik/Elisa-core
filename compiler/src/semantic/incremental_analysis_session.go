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
// initial body classes are integer returns with current-bound i64 parameters.
// It reuses expression typing; parameter reads, return checks, declarations,
// summaries and CFGs stay fresh. Resolved nongeneric mutable-borrow capability
// queries also reuse pointer-free products after declaration shapes are frozen.
// It is not a complete incremental compiler or a persistent whole-body cache.
type IncrementalAnalysisSession struct {
	mu                                           sync.Mutex
	lexical                                      *incremental.Session
	capabilities                                 *capabilityQueryCache
	db                                           *incremental.Database
	previous                                     map[incremental.Key]bool
	productBytes                                 map[incremental.Key]int
	retainedProductBytes                         int
	maxTrackedBodies                             int // zero selects the production default; tests may lower it
	maxRetainedProductBytes                      int // zero selects the production default; tests may lower it
	maxBindingSourceBytes, maxBindingSourceEdges int
	bindingSourceBytes, bindingSourceEdges       int
	stats                                        IncrementalAnalysisStats
	retiredQueries                               int
}
type IncrementalAnalysisStats struct {
	LexicalCapturedObservations, LexicalCapturedFrames                                                                                                       uint64
	LexicalBinderReadsObserved, LexicalBinderReadsDropped, LexicalBinderSelectionsVerified                                                                   uint64
	LexicalSourceIndexesBuilt, LexicalReadsObserved, LexicalSelectionsVerified, LexicalReadsDropped, LexicalVerificationFailures, LexicalPublicationFailures uint64
	LexicalQueriesExecuted, LexicalQueryMemoHits, LexicalQueriesSkipped                                                                                      uint64
	BinderFramesOwned, BinderFramesOwnerless                                                                                                                 uint64
	Analyses, PrimaryBodiesChecked, PrimaryBodiesReused, ReusedExpressionNodes                                                                               uint64
	PrimaryValueChecks, PrimaryExprChecks                                                                                                                    uint64
	PrimaryElementScans                                                                                                                                      uint64
	LocalInitializersChecked, LocalInitializersReused                                                                                                        uint64
	DatabaseResets                                                                                                                                           uint64
	ColdFallbackAnalyses                                                                                                                                     uint64
	CapacityFallbackAnalyses, ProductAdmissionMisses, RetainedProductBytes                                                                                   uint64
	CapacityLimitedAnalyses, CapacitySkippedQueries                                                                                                          uint64
	BindingSpecAdmissionMisses, RetainedBindingSourceBytes, RetainedBindingSourceEdges                                                                       uint64
	Queries                                                                                                                                                  incremental.Stats
	CapabilityReads, CapabilityLocalHits, CapabilityComputed, CapabilityProductsReused, CapabilityProductsPublished, CapabilityProjectionDeclines            uint64
	CapabilityRetainedBytes                                                                                                                                  uint64
	CapabilityQueries                                                                                                                                        incremental.Stats
}
type primitiveBodyProduct struct{ Source, Config, Safety, Types string }
type incrementalBodyInvocation struct {
	lexical                                    *lexicalReadInvocation
	session                                    *IncrementalAnalysisSession
	keys                                       map[*ast.FuncDecl]string
	changes                                    []incremental.Change
	config, safety                             string
	tracking                                   bool
	localInitializers                          map[*ast.VarDeclStmt]*primitiveLocalInitializer
	bindingReservedBytes, bindingReservedEdges int
	bindingBytes, bindingEdges                 int
}

var primitiveConfigKey = incremental.Key{Kind: "semantic-config", Name: "primitive-return-v1"}
var primitiveSafetyKey = incremental.Key{Kind: "semantic-lookup-set", Name: "closed-integer-operators-v1"}
var errPrimitiveProductMiss = errors.New("primitive typing product requires cold analysis")

const (
	primitiveSessionMaxTrackedBodies        = 4096
	primitiveSessionMaxRetainedProductBytes = 1 << 20
)

func primitiveInput(kind, name string) incremental.Key {
	return incremental.Key{Kind: kind, Name: name}
}

func NewIncrementalAnalysisSession() *IncrementalAnalysisSession {
	session := &IncrementalAnalysisSession{previous: map[incremental.Key]bool{}}
	session.resetDatabase()
	return session
}
func (session *IncrementalAnalysisSession) resetDatabase() {
	session.productBytes = make(map[incremental.Key]int)
	session.retainedProductBytes = 0
	session.bindingSourceBytes = 0
	session.bindingSourceEdges = 0
	session.db = incremental.New(primitiveQueryEvaluators())
}
func (session *IncrementalAnalysisSession) Stats() IncrementalAnalysisStats {
	session.mu.Lock()
	defer session.mu.Unlock()
	stats := session.stats
	stats.RetainedProductBytes = uint64(session.retainedProductBytes)
	stats.RetainedBindingSourceBytes = uint64(session.bindingSourceBytes)
	stats.RetainedBindingSourceEdges = uint64(session.bindingSourceEdges)
	if cache := session.capabilities; cache != nil {
		stats.CapabilityRetainedBytes = uint64(cache.bytes)
		stats.CapabilityQueries = cache.db.Stats()
		stats.CapabilityQueries.Executed += cache.queries.Executed
		stats.CapabilityQueries.MemoHits += cache.queries.MemoHits
		stats.CapabilityQueries.Validated += cache.queries.Validated
		stats.CapabilityQueries.DependencyChecks += cache.queries.DependencyChecks
		stats.CapabilityQueries.InputReads += cache.queries.InputReads
	}
	if session.db != nil {
		stats.Queries = session.db.Stats()
	}
	return stats
}

func (session *IncrementalAnalysisSession) trackedBodyLimit() int {
	if session.maxTrackedBodies > 0 {
		return session.maxTrackedBodies
	}
	return primitiveSessionMaxTrackedBodies
}

func (session *IncrementalAnalysisSession) productByteLimit() int {
	if session.maxRetainedProductBytes > 0 {
		return session.maxRetainedProductBytes
	}
	return primitiveSessionMaxRetainedProductBytes
}

func (session *IncrementalAnalysisSession) forgetPrimitiveProduct(name string) {
	key := primitiveInput("function-product", name)
	if size, ok := session.productBytes[key]; ok {
		if size >= session.retainedProductBytes {
			session.retainedProductBytes = 0
		} else {
			session.retainedProductBytes -= size
		}
		delete(session.productBytes, key)
	}
}

func (session *IncrementalAnalysisSession) storePrimitiveProduct(name string, encoded string) bool {
	productKey := primitiveInput("function-product", name)
	oldSize := session.productBytes[productKey]
	if oldSize > session.retainedProductBytes {
		oldSize = session.retainedProductBytes
	}
	retained := session.retainedProductBytes - oldSize + len(encoded)
	if retained > session.productByteLimit() {
		session.db.Apply(incremental.Change{Key: productKey, Remove: true})
		session.evictPrimitiveTyping(name)
		session.forgetPrimitiveProduct(name)
		session.stats.ProductAdmissionMisses++
		return false
	}
	session.db.Apply(incremental.Change{Key: productKey, Value: encoded})
	session.productBytes[productKey] = len(encoded)
	session.retainedProductBytes = retained
	return true
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
	invocation := &incrementalBodyInvocation{session: session, keys: map[*ast.FuncDecl]string{}, localInitializers: map[*ast.VarDeclStmt]*primitiveLocalInitializer{}, config: hex.EncodeToString(digest[:])}
	invocation.lexical = &lexicalReadInvocation{indexes: map[*ast.FuncDecl]*FunctionSourceIndex{}}
	invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveConfigKey, Value: invocation.config})
	current := map[incremental.Key]bool{}
	origin := file.Filename
	if len(origin) > primitiveSourceByteBudget {
		return AnalyzeWithOptions(file, options)
	}
	if absolute, err := filepath.Abs(origin); err == nil {
		origin = absolute
	}
	skipped := 0
	var pendingLocals []*primitiveLocalInitializer
	trimPending := func() {
		available := session.trackedBodyLimit() - len(current)
		if len(pendingLocals) > available {
			skipped += len(pendingLocals) - available
			clear(pendingLocals[available:])
			pendingLocals = pendingLocals[:available]
		}
	}
	var collect func([]ast.Decl, string, int, bool)
	collect = func(decls []ast.Decl, namespace string, depth int, retainedOnly bool) {
		if depth > 128 {
			return
		}
		for _, decl := range decls {
			if nested, ok := decl.(*ast.NamespaceDecl); ok {
				if len(nested.Name) > primitiveSourceByteBudget-len(namespace)-1 {
					continue
				}
				collect(nested.Decls, joinQualifiedName(namespace, nested.Name), depth+1, retainedOnly)
				continue
			}
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn == nil {
				continue
			}
			if fn.Name == "" || len(fn.Name) > primitiveSourceByteBudget {
				continue
			}
			qualifiedName := joinQualifiedName(namespace, fn.Name)
			if len(qualifiedName) > primitiveSourceByteBudget {
				continue
			}

			keyBytes := []byte("primitive/i64-parameters/v2\x00")
			keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(fn.Params)))
			keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(origin)))
			keyBytes = append(keyBytes, origin...)
			keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(qualifiedName)))
			keyBytes = append(keyBytes, qualifiedName...)
			identity := sha256.Sum256(keyBytes)
			name := "primitive-v1:" + hex.EncodeToString(identity[:])
			key := primitiveInput("function-source", name)
			retained := session.previous[key]
			if retained && len(fn.Body) > 1 {
				// A local-function owner is useful only together with a local
				// query. A former return-body owner is not a retained local unit.
				retained = false
				if len(fn.Body) <= 257 {
					for _, stmt := range fn.Body {
						local, ok := stmt.(*ast.VarDeclStmt)
						if !ok || local == nil || len(local.Name) > primitiveSourceByteBudget {
							continue
						}
						id := sha256.Sum256([]byte(local.Name))
						if session.previous[primitiveInput("function-source", name+"/local-name-v2/"+hex.EncodeToString(id[:]))] {
							retained = true
							break
						}
					}
				}
			}
			if retainedOnly != retained || current[key] {
				continue
			}
			expressions, primitiveReturn := primitiveReturnExpressions(fn)
			ok = primitiveReturn
			if _, exists := invocation.lexical.indexes[fn]; !exists && len(invocation.lexical.indexes) < lexicalReadMaxFunctions {
				readFree := primitiveReturn
				for _, expr := range expressions {
					if _, ident := expr.(*ast.Ident); ident {
						readFree = false
						break
					}
				}
				var index *FunctionSourceIndex
				if !readFree {
					index, _ = IndexFunctionSource(fn, FunctionIdentityContext{Origin: origin, QualifiedName: qualifiedName})
					if index != nil {
						if len(index.ordinals) > lexicalReadMaxSourceNodes-invocation.lexical.indexNodes {
							index = nil
						} else {
							invocation.lexical.indexNodes += len(index.ordinals)
							session.stats.LexicalSourceIndexesBuilt++
						}
					}
				}
				invocation.lexical.indexes[fn] = index
			}
			var locals []*primitiveLocalInitializer
			if !ok {
				locals, ok = primitiveLocalInitializers(fn)
				if !ok {
					continue
				}
			}
			needed := 1
			if len(locals) > 0 {
				needed = 2
			} // owner context plus at least one initializer
			if len(current)+needed > session.trackedBodyLimit() {
				skipped += 1 + len(locals)
				continue
			}
			invocation.keys[fn] = name
			current[key] = true
			trimPending()
			for _, input := range locals {
				identity := sha256.Sum256([]byte(input.declaration.Name))
				input.key = name + "/local-name-v2/" + hex.EncodeToString(identity[:])
				localKey := primitiveInput("function-source", input.key)
				if retainedOnly && !session.previous[localKey] {
					// Reserve space for all other retained owners before new locals.
					if len(current)+len(pendingLocals) >= session.trackedBodyLimit() {
						skipped++
						continue
					}
					pendingLocals = append(pendingLocals, input)
					continue
				}
				if len(current) >= session.trackedBodyLimit() {
					skipped++
					continue
				}
				if !invocation.reserveBinding(input) {
					skipped++
					continue
				}
				invocation.localInitializers[input.declaration] = input
				current[localKey] = true
				trimPending()
			}
			if len(locals) > 0 {
				admitted := false
				for _, input := range locals {
					if input.bindingBytes > 0 {
						admitted = true
						break
					}
				}
				if !admitted {
					delete(invocation.keys, fn)
					delete(current, key)
					skipped++
					kept := pendingLocals[:0]
					for _, pending := range pendingLocals {
						if pending.owner == fn {
							skipped++
						} else {
							kept = append(kept, pending)
						}
					}
					clear(pendingLocals[len(kept):])
					pendingLocals = kept
				}
			}
		}
	}
	// Budget pressure must not turn a large request entirely cold. Retain
	// existing owners first, then fill available slots in declaration order.
	if len(session.previous) > 0 {
		collect(file.Decls, "", 0, true)
	}
	for _, input := range pendingLocals {
		if len(current) >= session.trackedBodyLimit() {
			skipped++
			continue
		}
		if invocation.keys[input.owner] == "" {
			skipped++
			continue
		}
		if !invocation.reserveBinding(input) {
			skipped++
			continue
		}
		invocation.localInitializers[input.declaration] = input
		current[primitiveInput("function-source", input.key)] = true
	}
	clear(pendingLocals)
	pendingLocals = nil
	localOwners := map[*ast.FuncDecl]bool{}
	for _, input := range invocation.localInitializers {
		localOwners[input.owner] = true
	}
	for fn, name := range invocation.keys {
		if len(fn.Body) > 1 && !localOwners[fn] {
			delete(invocation.keys, fn)
			delete(current, primitiveInput("function-source", name))
			skipped++
		}
	}
	collect(file.Decls, "", 0, false)
	if skipped > 0 {
		session.stats.CapacityLimitedAnalyses++
		session.stats.CapacitySkippedQueries += uint64(skipped)
	}
	invocation.registerBindingRecipes()
	var removed []incremental.Key
	for key := range session.previous {
		if !current[key] {
			invocation.changes = append(invocation.changes, incremental.Change{Key: key, Remove: true})
			invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveInput("function-product", key.Name), Remove: true})
			invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveInput(primitiveBindingRecipeKind, key.Name), Remove: true})
			session.forgetPrimitiveProduct(key.Name)
			removed = append(removed, incremental.Key{Kind: "primitive-return", Name: key.Name}, incremental.Key{Kind: primitiveLocalTypingKind, Name: key.Name}, incremental.Key{Kind: primitiveBindingSourceKind, Name: key.Name})
		}
	}
	session.previous = current
	session.db.Apply(invocation.changes...)
	for _, key := range removed {
		session.db.EvictQuery(key)
	}
	session.retiredQueries += len(removed) / 3
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
	session.bindingSourceBytes = invocation.bindingBytes
	session.bindingSourceEdges = invocation.bindingEdges
	if len(invocation.keys) == 0 {
		session.stats.ColdFallbackAnalyses++
	}
	result := analyzeWithBodyInvocation(file, options, invocation)
	invocation.lexical.publish(session)
	return result
}
func (invocation *incrementalBodyInvocation) prepareAnalyzer(a *Analyzer) {
	invocation.safety = "blocked"
	intType, intOK := a.namedTypes["int"].(*BuiltinType)
	i64Type, i64OK := a.namedTypes["i64"].(*BuiltinType)
	if intOK && i64OK && intType.Name == "int" && i64Type.Name == "i64" && a.incrementalOperators.operatorsAbsent(a, intType, i64Type) {
		invocation.safety = primitiveOperatorAbsence
	}
	invocation.session.db.Apply(incremental.Change{Key: primitiveSafetyKey, Value: invocation.safety})
}

func primitiveReturnHeader(fn *ast.FuncDecl) (*ast.ReturnStmt, bool) {
	if fn == nil || fn.Name == "" || len(fn.Name) > primitiveSourceByteBudget || len(fn.Body) != 1 {
		return nil, false
	}
	// Every other declaration feature stays cold, including future added fields.
	header := *fn
	if !primitiveParameterSyntax(header.Params) {
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
	var parameters map[string]bool
	if len(fn.Params) > 0 {
		parameters = make(map[string]bool, len(fn.Params))
	}
	for _, param := range fn.Params {
		parameters[param.Name] = true
		sourceBytes += len(param.Name)
	}
	if sourceBytes > primitiveSourceByteBudget {
		return nil, false
	}
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
		case *ast.Ident:
			if !primitiveParameterSchemaValid || !parameters[node.Name] || len(node.Name) > primitiveSourceByteBudget-sourceBytes {
				return false
			}
			sourceBytes += len(node.Name)
			return true
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
	data = append(data, "parameter-i64-typing-v2\x00"...)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(fn.Name)))
	data = append(data, fn.Name...)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(fn.Params)))
	for _, param := range fn.Params {
		data = binary.LittleEndian.AppendUint64(data, uint64(len(param.Name)))
		data = append(data, param.Name...)
	}
	for _, expr := range nodes {
		switch node := expr.(type) {
		case *ast.Ident:
			data = append(data, 'N')
			data = binary.LittleEndian.AppendUint64(data, uint64(len(node.Name)))
			data = append(data, node.Name...)
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
	// A local product adds one pre-conversion result tag to 4096 expression tags.
	if len(types) == 0 || len(types) > 4097 {
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
	if reads := a.lexicalReads; reads != nil {
		prior := reads.primaryOwner
		reads.primaryOwner = nil
		if !a.suppressDiagnostics && a.convergeDepth == 0 && len(typeArgs) == 0 {
			reads.primaryOwner = fn
		}
		defer func() { reads.primaryOwner = prior }()
	}

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
	if !indexed || !eligible || !primitiveCurrentParameterBindings(a, fn) || invocation.safety != primitiveOperatorAbsence || !returnOK || currentReturn.Name != "i64" || currentReturn != a.namedTypes["i64"] {
		if indexed && !eligible {
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("function-source", key), Remove: true}, incremental.Change{Key: primitiveInput("function-product", key), Remove: true})
			invocation.session.forgetPrimitiveProduct(key)
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
			restored := replayPrimitiveParameterNodes(a, nodes, encoded)
			session.stats.PrimaryBodiesReused++
			session.stats.ReusedExpressionNodes += uint64(restored)
			a.visitFunctionBodyWithElementScan(fn.Body, func(stmt ast.Stmt) {
				ret := stmt.(*ast.ReturnStmt)
				a.analyzeReturnStatement(ret, a.exprTypes[ret.Value])
			}, true)
			return
		}
		// Drop an unusable memo before the cold fallback. Other valid products stay.
		session.db.Apply(incremental.Change{Key: primitiveInput("function-product", key), Remove: true})
		session.forgetPrimitiveProduct(key)
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
		_, parameter := expr.(*ast.Ident)
		if !ok || (!parameter && a.exprFacts[expr].HasAnyFacts()) || typ != a.namedTypes[typ.Name] {
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
	if session.storePrimitiveProduct(key, string(product)) {
		_, _ = session.db.Evaluate(context.Background(), query)
	}
}
