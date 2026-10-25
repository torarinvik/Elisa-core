package semantic

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

const (
	incrementalEnsureProofSchema = "ensure-smt-positive-v1"
	incrementalEnsureProofLimit  = 4096
	primitiveEnsureProofSafety   = primitiveOperatorAbsence + ":no-int-i64-comparison-overloads-v1"
)

type incrementalEnsureProofInput struct {
	tree [32]byte
	keys map[int]string
}

func (session *IncrementalAnalysisSession) hasEnsureProof(key string) bool {
	_, ok := session.ensureProofs[key]
	return ok
}

func (session *IncrementalAnalysisSession) storeEnsureProof(key string) bool {
	if key == "" || session.hasEnsureProof(key) || len(session.ensureProofs) >= incrementalEnsureProofLimit {
		return false
	}
	session.ensureProofs[key] = struct{}{}
	session.stats.EnsureProofMemoPublished++
	return true
}

// prepareEnsureProofMemo admits only a one-return integer function whose SMT
// obligation is closed over its scalar parameters and return expression. This
// excludes ambient requires, locals, flow facts, generic protocol laws, and
// overloaded operators from the proof context.
func (invocation *incrementalBodyInvocation) prepareEnsureProofMemo(a *Analyzer, fn *ast.FuncDecl, owner string) {
	if invocation == nil || invocation.session == nil || a == nil || fn == nil || owner == "" ||
		!a.smtEnabled || !a.enforceStrictProofs || a.currentFuncDecl != fn ||
		invocation.safety != primitiveOperatorAbsence || len(fn.Requires) != 0 ||
		!primitiveCurrentParameterBindings(a, fn) || !primitiveEnsureProofContextIsEmpty(a) {
		return
	}
	if _, ok := primitiveReturnTypingHeader(fn); !ok {
		return
	}
	if !primitiveEnsureFunctionType(a, fn) || !predicateComparisonCandidatesAbsent(a, []string{"bool", "int", "i64"}) {
		return
	}
	if len(fn.EnsureValues) > 256 {
		return
	}
	tree, _, err := functionSyntaxFingerprint(fn)
	if err != nil {
		return
	}
	input := incrementalEnsureProofInput{tree: tree, keys: make(map[int]string)}
	remainingNodes, sourceBytes := 4096, 0
	for index, clause := range fn.EnsureValues {
		if !primitiveEnsureBooleanSyntaxAndTypes(a, fn, clause, &remainingNodes, &sourceBytes) {
			continue
		}
		key := incrementalEnsureProofKey(owner, tree, index, invocation.config, primitiveEnsureProofSafety, a.smtBinary, smtResourceLimit())
		input.keys[index] = key
		if invocation.session.hasEnsureProof(key) {
			if invocation.ensureProofHits == nil {
				invocation.ensureProofHits = make(map[*ast.FuncDecl]map[int]bool)
			}
			if invocation.ensureProofHits[fn] == nil {
				invocation.ensureProofHits[fn] = make(map[int]bool)
			}
			invocation.ensureProofHits[fn][index] = true
		}
	}
	if len(input.keys) == 0 {
		return
	}
	if invocation.ensureProofInputs == nil {
		invocation.ensureProofInputs = make(map[*ast.FuncDecl]incrementalEnsureProofInput)
	}
	invocation.ensureProofInputs[fn] = input
}

func primitiveEnsureFunctionType(a *Analyzer, fn *ast.FuncDecl) bool {
	if a == nil || fn == nil || a.currentFuncType == nil {
		return false
	}
	ft := a.currentFuncType
	if len(ft.TypeParams) != 0 || len(ft.RegionParams) != 0 || len(ft.InferredRegionParams) != 0 ||
		len(ft.PermissionParams) != 0 || len(ft.ShapeParams) != 0 || len(ft.GenericParams) != 0 ||
		len(ft.Params) != len(fn.Params) {
		return false
	}
	ret, ok := a.currentReturn.(*BuiltinType)
	if !ok || (ret.Name != "int" && ret.Name != "i64") || a.namedTypes[ret.Name] != ret ||
		predicateBuiltinSyntaxName(fn.ReturnType) != ret.Name {
		return false
	}
	for index, param := range fn.Params {
		name := predicateBuiltinSyntaxName(param.Type)
		typ, ok := ft.Params[index].(*BuiltinType)
		if !ok || (name != "int" && name != "i64") || typ.Name != name || a.namedTypes[name] != typ {
			return false
		}
	}
	return true
}

func primitiveEnsureProofContextIsEmpty(a *Analyzer) bool {
	if a == nil {
		return false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if len(scope.smtAssertFacts) != 0 || len(scope.rangeFacts) != 0 {
			return false
		}
		for _, symbol := range scope.Symbols {
			if symbol != nil && symbol.Kind == SymbolLocal {
				return false
			}
		}
	}
	return true
}

func primitiveEnsureBooleanSyntaxAndTypes(a *Analyzer, fn *ast.FuncDecl, root ast.Expr, remainingNodes, sourceBytes *int) bool {
	if a == nil || fn == nil || root == nil || remainingNodes == nil || sourceBytes == nil || len(fn.Params) > primitiveParameterLimit {
		return false
	}
	parameters := make(map[string]bool, len(fn.Params))
	for _, param := range fn.Params {
		if param.Name == "result" {
			return false
		}
		parameters[param.Name] = true
	}
	var visit func(ast.Expr, int) (string, bool)
	visit = func(expr ast.Expr, depth int) (string, bool) {
		if expr == nil || depth > 128 || *remainingNodes <= 0 || *sourceBytes > primitiveSourceByteBudget {
			return "", false
		}
		(*remainingNodes)--
		classOf := func() (string, bool) {
			typ, ok := a.exprType(expr).(*BuiltinType)
			if !ok || typ == nil || a.namedTypes[typ.Name] != typ {
				return "", false
			}
			switch typ.Name {
			case "int", "i64", "bool":
				return typ.Name, true
			}
			return "", false
		}
		switch node := expr.(type) {
		case *ast.Ident:
			copy := *node
			copy.Position, copy.Name = lexer.Pos{}, ""
			if !reflect.ValueOf(copy).IsZero() || (node.Name != "result" && !parameters[node.Name]) || len(node.Name) > primitiveSourceByteBudget-*sourceBytes {
				return "", false
			}
			*sourceBytes += len(node.Name)
			class, ok := classOf()
			if !ok || (class != "int" && class != "i64") {
				return "", false
			}
			return class, true
		case *ast.IntLit:
			copy := *node
			copy.Position, copy.Value, copy.Suffix, copy.IsHex = lexer.Pos{}, "", "", false
			if node.Suffix != "" || !reflect.ValueOf(copy).IsZero() || len(node.Value) > primitiveSourceByteBudget-*sourceBytes {
				return "", false
			}
			*sourceBytes += len(node.Value)
			class, ok := classOf()
			return class, ok && (class == "int" || class == "i64")
		case *ast.BoolLit:
			copy := *node
			copy.Position, copy.Value = lexer.Pos{}, false
			class, ok := classOf()
			return class, ok && class == "bool" && reflect.ValueOf(copy).IsZero()
		case *ast.ParenExpr:
			copy := *node
			copy.Position, copy.Inner = lexer.Pos{}, nil
			if !reflect.ValueOf(copy).IsZero() {
				return "", false
			}
			inner, ok := visit(node.Inner, depth+1)
			class, typed := classOf()
			return class, ok && typed && class == inner
		case *ast.UnaryExpr:
			copy := *node
			copy.Position, copy.Op, copy.Operand, copy.LoweredCall = lexer.Pos{}, 0, nil, nil
			if !reflect.ValueOf(copy).IsZero() || node.LoweredCall != nil || (node.Op != lexer.TOKEN_NOT && node.Op != lexer.TOKEN_MINUS) {
				return "", false
			}
			inner, ok := visit(node.Operand, depth+1)
			class, typed := classOf()
			if !typed {
				return "", false
			}
			if node.Op == lexer.TOKEN_NOT {
				return class, ok && inner == "bool" && class == "bool"
			}
			return class, ok && (class == "int" || class == "i64") && inner == class
		case *ast.BinaryExpr:
			copy := *node
			copy.Position, copy.Op, copy.Left, copy.Right, copy.LoweredCall = lexer.Pos{}, 0, nil, nil, nil
			if !reflect.ValueOf(copy).IsZero() || node.LoweredCall != nil {
				return "", false
			}
			left, leftOK := visit(node.Left, depth+1)
			right, rightOK := visit(node.Right, depth+1)
			class, typed := classOf()
			if !leftOK || !rightOK || !typed {
				return "", false
			}
			switch node.Op {
			case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR:
				return class, left == right && (left == "int" || left == "i64") && class == left
			case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
				return class, left == right && (left == "int" || left == "i64") && class == "bool"
			case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
				return class, left == right && class == "bool"
			case lexer.TOKEN_AND, lexer.TOKEN_OR:
				return class, left == "bool" && right == "bool" && class == "bool"
			default:
				return "", false
			}
		default:
			return "", false
		}
	}
	class, ok := visit(root, 0)
	return ok && class == "bool"
}

func incrementalEnsureProofKey(owner string, tree [32]byte, clause int, config, safety, solver string, resourceLimit int64) string {
	data := []byte(incrementalEnsureProofSchema + "\x00")
	data = append(data, tree[:]...)
	for _, value := range []string{owner, strconv.Itoa(clause), config, safety, solver, strconv.FormatInt(resourceLimit, 10)} {
		data = binary.LittleEndian.AppendUint64(data, uint64(len(value)))
		data = append(data, value...)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (invocation *incrementalBodyInvocation) hasEnsureProof(fn *ast.FuncDecl, clause int) bool {
	if invocation == nil || invocation.session == nil || fn == nil || invocation.ensureProofHits[fn] == nil || !invocation.ensureProofHits[fn][clause] {
		return false
	}
	input, ok := invocation.ensureProofInputs[fn]
	if !ok {
		return false
	}
	tree, _, err := functionSyntaxFingerprint(fn)
	if err != nil || tree != input.tree {
		return false
	}
	invocation.session.stats.EnsureProofMemoHits++
	return true
}

func (invocation *incrementalBodyInvocation) recordEnsureProof(fn *ast.FuncDecl, clause int, proven bool) {
	if !proven || invocation == nil || invocation.session == nil || fn == nil {
		return
	}
	input, ok := invocation.ensureProofInputs[fn]
	key, hasClause := input.keys[clause]
	if !ok || !hasClause {
		return
	}
	tree, _, err := functionSyntaxFingerprint(fn)
	if err != nil || tree != input.tree {
		return
	}
	if invocation.pendingEnsureProofs == nil {
		invocation.pendingEnsureProofs = make(map[*ast.FuncDecl]map[int]string)
	}
	if invocation.pendingEnsureProofs[fn] == nil {
		invocation.pendingEnsureProofs[fn] = make(map[int]string)
	}
	invocation.pendingEnsureProofs[fn][clause] = key
}

func (invocation *incrementalBodyInvocation) publishEnsureProofs() {
	if invocation == nil || invocation.session == nil {
		return
	}
	for fn, pending := range invocation.pendingEnsureProofs {
		input, ok := invocation.ensureProofInputs[fn]
		if !ok {
			continue
		}
		tree, _, err := functionSyntaxFingerprint(fn)
		if err != nil || tree != input.tree {
			continue
		}
		for clause, key := range pending {
			if input.keys[clause] == key {
				invocation.session.storeEnsureProof(key)
			}
		}
	}
}
