package semantic

import (
	"context"
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"elisacore/src/lexer"
	"elisacore/src/predicatetypes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
)

func (invocation *incrementalBodyInvocation) prepareAnalyzer(a *Analyzer) {
	// Static impls and named builtin types are complete before body analysis.
	// Local initializer hooks call this for every declaration, so keep the
	// per-analysis operator proof and its input publication out of that loop.
	if invocation.safetyPrepared {
		return
	}
	invocation.safety = "blocked"
	intType, intOK := a.namedTypes["int"].(*BuiltinType)
	i64Type, i64OK := a.namedTypes["i64"].(*BuiltinType)
	if intOK && i64OK && intType.Name == "int" && i64Type.Name == "i64" && a.incrementalOperators.operatorsAbsent(a, intType, i64Type) {
		invocation.safety = primitiveOperatorAbsence
	}
	invocation.session.db.Apply(incremental.Change{Key: primitiveSafetyKey, Value: invocation.safety})
	invocation.safetyPrepared = true
}

func (invocation *incrementalBodyInvocation) prefetchLocalInitializerQueries() {
	if invocation == nil || invocation.session == nil || invocation.safety != primitiveOperatorAbsence || len(invocation.localInitializers) == 0 {
		return
	}
	inputs := make([]*primitiveLocalInitializer, 0, len(invocation.localInitializers))
	for _, input := range invocation.localInitializers {
		// No stored product means no dependency query can hit. Record that miss
		// directly; retained products are batched below and revalidated by the
		// analyzer immediately before consumption.
		product := primitiveInput("function-product", input.key)
		if invocation.session.productBytes[product] == 0 {
			input.queryResult = incremental.EvaluationResult{Err: errPrimitiveProductMiss}
			input.hasQueryResult = true
			continue
		}
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		return
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].key < inputs[j].key })
	keys := make([]incremental.Key, len(inputs))
	for i, input := range inputs {
		keys[i] = incremental.Key{Kind: primitiveLocalTypingKind, Name: input.key}
	}
	results := invocation.session.db.EvaluateBatch(context.Background(), keys)
	for i, input := range inputs {
		input.queryResult = results[i]
		input.hasQueryResult = true
	}
}

func primitiveReturnHeader(fn *ast.FuncDecl) (*ast.ReturnStmt, bool) {
	return predicatetypes.ReturnHeader(fn, primitiveParameterSchemaValid, primitiveParameterLimit, primitiveSourceByteBudget)
}

const primitiveSourceByteBudget = 1024 * 1024

func primitiveReturnExpressions(fn *ast.FuncDecl) ([]ast.Expr, bool) {
	return primitiveReturnExpressionsWithNodeBudget(fn, nil, nil)
}

func primitiveReturnExpressionsWithNodeBudget(fn *ast.FuncDecl, remainingNodes *int, budgetExhausted *bool) ([]ast.Expr, bool) {
	if nodes, ok := primitiveI64ReturnExpressionsWithNodeBudget(fn, remainingNodes, budgetExhausted); ok {
		return nodes, true
	}
	if budgetExhausted != nil && *budgetExhausted {
		return nil, false
	}
	return predicateReturnExpressionsWithNodeBudget(fn, remainingNodes, budgetExhausted)
}

// Only return-expression typing is retained here. Plain value postconditions
// are analyzed before this boundary and discharged again by analyzeReturnStatement
// after replay. Their syntax/proof results are never part of the body product.
// Valid inline hints affect the live function signature, not expression typing.
// Keep scoped proof blocks and other header features on the cold path.
func primitiveReturnTypingHeader(fn *ast.FuncDecl) (*ast.ReturnStmt, bool) {
	if fn == nil || len(fn.EnsureProofs) > len(fn.EnsureValues) {
		return nil, false
	}
	for _, proof := range fn.EnsureProofs {
		if proof != nil {
			return nil, false
		}
	}
	if len(fn.Annotations) > 1 {
		return nil, false
	}
	for _, annotation := range fn.Annotations {
		if annotation.Name != "inline" || len(annotation.Args) != 1 {
			return nil, false
		}
		if _, valid := normalizeInlineAnnotationArg(annotation.Args[0]); !valid {
			return nil, false
		}
		copy := annotation
		copy.Position, copy.Name, copy.Args = lexer.Pos{}, "", nil
		if !reflect.ValueOf(copy).IsZero() {
			return nil, false
		}
	}
	header := *fn
	header.Annotations = nil
	header.EnsureValues = nil
	header.EnsureProofs = nil
	return primitiveReturnHeader(&header)
}

func primitiveI64ReturnExpressionsWithNodeBudget(fn *ast.FuncDecl, remainingNodes *int, budgetExhausted *bool) ([]ast.Expr, bool) {
	stmt, ok := primitiveReturnTypingHeader(fn)
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
	nodes, _, ok := primitiveI64ExpressionNodesWithNodeBudget(stmt.Value, parameters, sourceBytes, remainingNodes, budgetExhausted)
	return nodes, ok
}

// primitiveI64ExpressionNodes validates one expression against an already
// checked set of visible parameter/local names. Local-initializer planning uses
// this directly so each statement doesn't rebuild a synthetic function header
// containing every preceding local. It returns the updated spelling-byte count
// so a multi-initializer plan can enforce one cumulative function budget.
func primitiveI64ExpressionNodes(root ast.Expr, parameters map[string]bool, sourceBytes int) ([]ast.Expr, int, bool) {
	return primitiveI64ExpressionNodesWithNodeBudget(root, parameters, sourceBytes, nil, nil)
}

func primitiveI64ExpressionNodesWithNodeBudget(root ast.Expr, parameters map[string]bool, sourceBytes int, remainingNodes *int, budgetExhausted *bool) ([]ast.Expr, int, bool) {
	return predicatetypes.IntegerExpressionNodes(root, parameters, sourceBytes, primitiveSourceByteBudget, primitiveParameterSchemaValid, remainingNodes, budgetExhausted)
}

// The compact source protocol includes every non-position field admitted by
// primitiveReturnExpressions. Fixed node arities make preorder unambiguous;
// values are length-prefixed, and literal spelling/hex mode remain inputs.
func primitiveExpressionDigest(fn *ast.FuncDecl, nodes []ast.Expr) string {
	data := make([]byte, 0, len(nodes)*12+len(fn.Name)+32)
	data = append(data, "canonical-scalar-typing-v3\x00"...)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(fn.Name)))
	data = append(data, fn.Name...)
	returnName := predicateBuiltinSyntaxName(fn.ReturnType)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(returnName)))
	data = append(data, returnName...)

	data = binary.LittleEndian.AppendUint64(data, uint64(len(fn.Params)))
	for _, param := range fn.Params {
		data = binary.LittleEndian.AppendUint64(data, uint64(len(param.Name)))
		data = append(data, param.Name...)
		typeName := predicateBuiltinSyntaxName(param.Type)
		data = binary.LittleEndian.AppendUint64(data, uint64(len(typeName)))
		data = append(data, typeName...)

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
		case *ast.BoolLit:
			data = append(data, 'T')
			if node.Value {
				data = append(data, 1)
			} else {
				data = append(data, 0)
			}
		case *ast.CharLit:
			data = append(data, 'C')
			data = binary.LittleEndian.AppendUint64(data, uint64(len(node.Value)))
			data = append(data, node.Value...)
		case *ast.MembershipRangeExpr:
			data = append(data, 'R')
			data = binary.LittleEndian.AppendUint64(data, uint64(node.Op))
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
	budgetExhausted := false
	nodes, eligible := primitiveReturnExpressionsWithNodeBudget(fn, &invocation.expressionNodesRemaining, &budgetExhausted)
	predicate := predicateBuiltinSyntaxName(fn.ReturnType) == "bool"
	returnName := predicateBuiltinSyntaxName(fn.ReturnType)
	bindingsReady := primitiveCurrentParameterBindings(a, fn)
	safetyReady := invocation.safety == primitiveOperatorAbsence
	if predicate {
		returnName = "bool"
		if eligible {
			bindingsReady = predicateCurrentBindingsAndOperators(a, fn, nodes)
			safetyReady = bindingsReady
			safety := "blocked"
			if safetyReady {
				safety = predicateOperatorAbsence
			}
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("predicate-safety", key), Value: safety})
		} else if !budgetExhausted {
			bindingsReady, safetyReady = false, false
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("predicate-safety", key), Remove: true})
		} else {
			bindingsReady, safetyReady = false, false
		}
	} else if !budgetExhausted {
		invocation.session.db.Apply(incremental.Change{Key: primitiveInput("predicate-safety", key), Remove: true})
	}
	if !eligible || !bindingsReady || !safetyReady || !returnOK || currentReturn.Name != returnName || currentReturn != a.namedTypes[returnName] {
		if indexed && !eligible && !budgetExhausted {
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("function-source", key), Remove: true}, incremental.Change{Key: primitiveInput("function-product", key), Remove: true})
			invocation.session.forgetPrimitiveProduct(key)
			invocation.session.db.EvictQuery(incremental.Key{Kind: "primitive-return", Name: key})
		} else if predicate && (!bindingsReady || !safetyReady) && !budgetExhausted {
			invocation.session.db.Apply(incremental.Change{Key: primitiveInput("function-product", key), Remove: true})
			invocation.session.forgetPrimitiveProduct(key)
			invocation.session.db.EvictQuery(incremental.Key{Kind: "primitive-return", Name: key})
		}
		invocation.session.stats.PrimaryBodiesChecked++
		a.analyzeFunctionBodyStmts(fn.Body)
		return
	}
	session := invocation.session
	if !predicate {
		invocation.prepareEnsureProofMemo(a, fn, key)
	}
	source := primitiveExpressionDigest(fn, nodes)
	sourceChange := incremental.Change{Key: primitiveInput("function-source", key), Value: source}
	session.db.Apply(sourceChange)
	query := incremental.Key{Kind: "primitive-return", Name: key}
	encoded, err := session.db.Evaluate(context.Background(), query)
	if err == nil && encoded != "" {
		if len(encoded) == len(nodes) && ((!predicate && validPrimitiveTyping(encoded)) || (predicate && predicateTypingMatchesNodes(nodes, encoded))) {
			restored := replayPrimitiveParameterNodes(a, nodes, encoded)
			session.stats.PrimaryBodiesReused++
			session.stats.ReusedExpressionNodes += uint64(restored)
			a.visitFunctionBodyWithElementScan(fn.Body, func(stmt ast.Stmt) {
				ret := stmt.(*ast.ReturnStmt)
				a.analyzeReturnStatement(ret, a.exprType(ret.Value))
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
	if predicate && !safetyReady {
		return
	}
	budgetExhausted = false
	currentNodes, valid := primitiveReturnExpressionsWithNodeBudget(fn, &invocation.expressionNodesRemaining, &budgetExhausted)
	if !valid || primitiveExpressionDigest(fn, currentNodes) != source {
		return
	}
	types := make([]byte, len(nodes))
	for i, expr := range nodes {
		if _, rangeNode := expr.(*ast.MembershipRangeExpr); predicate && rangeNode {
			// The marker means exactly "no standalone semantic contribution".
			// Decline if a later analyzer adds one instead of silently losing it.
			_, typed := a.lookupExprType(expr)
			_, facts := a.lookupExprFacts(expr)
			if typed || facts {
				return
			}
			types[i] = predicateRangeTypeTag
			continue
		}
		typ, ok := a.exprType(expr).(*BuiltinType)
		_, parameter := expr.(*ast.Ident)
		if !ok || (!parameter && a.exprFact(expr).HasAnyFacts()) || typ != a.namedTypes[typ.Name] {
			return
		}
		if predicate {
			tag := predicateTypeTag(typ.Name)
			if tag == 0 {
				return
			}
			types[i] = tag
			continue
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
	value := primitiveBodyProduct{Source: source, Config: invocation.config, Safety: invocation.safety, Types: string(types)}
	if predicate {
		value.Class = "predicate-v1"
		value.Safety = predicateOperatorAbsence
		if validRangePredicateTyping(value.Types) {
			value.Class = "predicate-range-v1"
		}
		if !predicateTypingMatchesNodes(nodes, value.Types) {
			return
		}
	}
	product, _ := json.Marshal(value)
	// Keep query evaluation lazy after publishing. Reuse validation runs when a
	// later analysis requests this product, avoiding work on the cold analysis.
	session.storePrimitiveProduct(key, string(product))
}
