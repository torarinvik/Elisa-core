package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

func builtinSetReceiverType(t Type) (*SetType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if setType, ok := t.(*SetType); ok && setType != nil {
		return setType, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	setType, ok := refType.Elem.(*SetType)
	if !ok || setType == nil {
		return nil, nil, false
	}
	return setType, refType, true
}

func builtinSetReceiverTypeExpr(pos lexer.Pos, setType *SetType, mutable bool) ast.TypeExpr {
	if setType == nil {
		return refToTypeExprWithStorage(&ast.NamedType{Position: pos, Name: "void"}, false, ast.RefStorageAny)
	}
	elemExpr := astTypeExprForBuiltinMethodRewrite(pos, setType.Elem)
	elem := &ast.BuiltinTypeExpr{
		Position: pos,
		Name:     "set",
		TypeArgs: []ast.TypeExpr{elemExpr},
	}
	if mutable {
		return refToTypeExprWithStorage(&ast.MutableType{Position: pos, Elem: elem}, false, ast.RefStorageAny)
	}
	return refToTypeExprWithStorage(elem, false, ast.RefStorageAny)
}

func runtimeBackedSetSupportDiagnostic(setType *SetType) string {
	const supported = "runtime-backed set elements must be cstr, an integer type, bool, or a const enum (any type with value equality); floats and aggregates are unsupported"
	if setType == nil {
		return supported
	}
	return fmt.Sprintf("%s; got element type in %s", supported, diagnosticTypeString(setType))
}

func (a *Analyzer) ensureRuntimeBackedSetSupported(pos lexer.Pos, setType *SetType) bool {
	if setSupportsRuntimeBackedOps(setType) {
		return true
	}
	a.errorf(pos, "%s", runtimeBackedSetSupportDiagnostic(setType))
	return false
}

func setMethodReceiverExpr(receiver ast.Expr, receiverType Type, mutable bool) ast.Expr {
	if _, ok := receiverType.(*RefType); ok {
		return receiver
	}
	setType, _, ok := builtinSetReceiverType(receiverType)
	if !ok || setType == nil {
		return &ast.AddrOfExpr{Position: receiver.Pos(), Operand: receiver}
	}
	return &ast.CastExpr{
		Position: receiver.Pos(),
		Operand:  &ast.AddrOfExpr{Position: receiver.Pos(), Operand: receiver},
		Target:   builtinSetReceiverTypeExpr(receiver.Pos(), setType, mutable),
		Origin:   ast.CastExprOriginGeneral,
	}
}

func (a *Analyzer) rewriteBuiltinSetMethodCall(expr *ast.CallExpr) builtinDictMethodRewriteStatus {
	if a == nil || expr == nil {
		return builtinDictMethodRewriteNone
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return builtinDictMethodRewriteNone
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return builtinDictMethodRewriteNone
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	setType, receiverRefType, ok := builtinSetReceiverType(receiverType)
	if !ok || setType == nil {
		return builtinDictMethodRewriteNone
	}
	if !a.ensureRuntimeBackedSetSupported(fieldExpr.Object.Pos(), setType) {
		return builtinDictMethodRewriteInvalid
	}
	method := fieldExpr.Field
	helperName := ""
	mutates := false
	needsAlloc := false
	switch method {
	case "contains", "has":
		helperName = "arena_set_contains"
	case "remove":
		helperName = "arena_set_remove"
		mutates = true
	case "clear":
		helperName = "arena_set_clear"
		mutates = true
	case "reserve":
		helperName = "arena_set_reserve"
		mutates = true
		needsAlloc = true
	case "add", "insert":
		helperName = "arena_set_add"
		mutates = true
		needsAlloc = true
	default:
		return builtinDictMethodRewriteNone
	}
	if mutates && receiverRefType == nil && !a.exprCanYieldWritableRef(fieldExpr.Object) {
		a.errorf(fieldExpr.Object.Pos(), "set %s requires a mutable set receiver", method)
		return builtinDictMethodRewriteInvalid
	}
	if needsAlloc {
		if a.regionAvailableForContainer(setType) || a.currentAllocExpr != nil {
			return builtinDictMethodRewriteNone
		}
		a.errorf(expr.Pos(), "set %s requires an active in <arena>: scope", method)
		return builtinDictMethodRewriteInvalid
	}
	rewrittenArgs := make([]ast.Expr, 0, len(expr.Args)+1)
	rewrittenArgs = append(rewrittenArgs, setMethodReceiverExpr(fieldExpr.Object, receiverType, mutates))
	rewrittenArgs = append(rewrittenArgs, expr.Args...)
	expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: helperName}
	expr.Args = rewrittenArgs
	expr.ArgNames = nil
	return builtinDictMethodRewriteApplied
}

func (a *Analyzer) analyzeBuiltinSetRegionMutationCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return nil, false
	}
	method := fieldExpr.Field
	if method != "add" && method != "insert" && method != "reserve" {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	setType, receiverRefType, ok := builtinSetReceiverType(receiverType)
	if !ok || setType == nil {
		return nil, false
	}
	if !a.ensureRuntimeBackedSetSupported(fieldExpr.Object.Pos(), setType) {
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !a.regionAvailableForContainer(setType) && a.currentAllocExpr == nil {
		return nil, false
	}
	if receiverRefType == nil && !a.exprCanYieldWritableRef(fieldExpr.Object) {
		a.errorf(fieldExpr.Object.Pos(), "set %s requires a mutable set receiver", method)
	}
	if method == "reserve" {
		if len(expr.Args) != 1 {
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			a.errorf(expr.Pos(), "set reserve expects 1 argument, got %d", len(expr.Args))
		} else {
			usizeType := a.namedTypes["usize"]
			argType := a.analyzeValueExpr(expr.Args[0], usizeType)
			if !AssignableTo(usizeType, argType) {
				a.errorf(expr.Args[0].Pos(), "set reserve expects usize, got %s", argType)
			}
		}
		voidType := a.namedTypes["void"]
		a.exprTypes[expr.Func] = &FuncType{Name: "set.reserve", Params: []Type{receiverType, a.namedTypes["usize"]}, Return: voidType}
		a.exprTypes[expr] = voidType
		return voidType, true
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "set %s expects 1 argument, got %d", method, len(expr.Args))
	} else {
		elemType := a.analyzeValueExpr(expr.Args[0], setType.Elem)
		if !AssignableTo(setType.Elem, elemType) {
			a.errorf(expr.Args[0].Pos(), "set %s expects element of type %s, got %s", method, setType.Elem, elemType)
		}
		a.checkNestedRegionElementStoreEscape(expr.Args[0], setType, setType.Elem, elemType)
		if !isFreshContainerProducer(expr.Args[0]) {
			a.checkInteriorRegionAgainstTarget(fieldExpr.Object, containerRegion(setType), expr.Args[0], "by-value copy")
		}
		a.consumeAffineValueExpr(expr.Args[0], setType.Elem, "move into set "+method)
	}
	boolType := a.namedTypes["bool"]
	a.exprTypes[expr.Func] = &FuncType{Name: "set." + method, Params: []Type{receiverType, setType.Elem}, Return: boolType}
	a.exprTypes[expr] = boolType
	return boolType, true
}
