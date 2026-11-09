package semantic

import (
	"elisacore/src/ast"
	"strconv"
	"strings"
)

func (a *Analyzer) rewriteExtensionMethodCall(expr *ast.CallExpr) extensionMethodCallRewriteStatus {
	if a == nil || expr == nil {
		return extensionMethodCallRewriteNone
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	// `recv.method[TypeArgs](args)` parses as a SpecializeExpr wrapping the FieldExpr.
	// Capture the explicit type args and unwrap to the FieldExpr so UFCS resolution
	// proceeds; the resolved callee is re-wrapped with the type args below.
	var callTypeArgs []ast.TypeExpr
	if !ok {
		if spec, sok := expr.Func.(*ast.SpecializeExpr); sok && spec != nil {
			if fe, fok := spec.Operand.(*ast.FieldExpr); fok && fe != nil {
				fieldExpr = fe
				callTypeArgs = spec.TypeArgs
				ok = true
			}
		}
	}
	if !ok || fieldExpr == nil || fieldExpr.Object == nil || fieldExpr.Field == "" {
		return extensionMethodCallRewriteNone
	}
	withCallTypeArgs := func(fn ast.Expr) ast.Expr {
		if len(callTypeArgs) == 0 {
			return fn
		}
		return &ast.SpecializeExpr{Position: fieldExpr.Position, Operand: fn, TypeArgs: callTypeArgs}
	}
	if ref := a.interfaceMethodRefs[fieldExpr]; ref != nil && ref.ImplKey != "" && ref.Receiver != nil {
		// Target-typed concrete protocol casts install their selected impl before
		// ordinary call analysis. Keep that target-filtered selection pinned rather
		// than re-running the unfiltered member-name ambiguity search.
		return extensionMethodCallRewriteNone
	}
	if a.rewriteTypestateConstructorCall(expr, fieldExpr) {
		return extensionMethodCallRewriteApplied
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return extensionMethodCallRewriteNone
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	if receiverType == nil || IsInvalidType(receiverType) {
		return extensionMethodCallRewriteNone
	}
	// docs/126 §2: `value.drop()` is the explicit early release of a drop-typed value.
	// Lower it to the destructor call it stands for, moving the receiver in, so the
	// ordinary affine machinery both rejects a later use and elides the scope-exit drop.
	if a.rewriteExplicitDropCall(expr, fieldExpr, receiverType) {
		return extensionMethodCallRewriteApplied
	}
	if _, ok := a.lookupFieldNoError(receiverType, fieldExpr.Field); ok {
		return extensionMethodCallRewriteNone
	}
	// Bounded generic instance-method dispatch: when the receiver has a bare
	// type-parameter type `T` whose generic bound `T: Protocol` declares a method
	// named `fieldExpr.Field`, resolve `value.method(args...)` against the bound
	// protocol by rewriting it to the static interface-method form
	// `T.method(value, args...)` (the receiver value becomes the first argument).
	// resolveInterfaceMethodExprType already specializes the protocol signature with
	// the type-param receiver and records the InterfaceMethodRef for dispatch.
	if status := a.rewriteBoundTypeParamMethodCall(expr, fieldExpr, receiverType, callTypeArgs); status != extensionMethodCallRewriteNone {
		return status
	}
	if proofCarryingViewReceiverHelper(fieldExpr.Field) {
		prependedArgs := make([]ast.Expr, 0, len(expr.Args)+1)
		prependedArgs = append(prependedArgs, fieldExpr.Object)
		prependedArgs = append(prependedArgs, expr.Args...)
		expr.Args = prependedArgs
		if len(expr.ArgNames) != 0 {
			prependedNames := make([]string, 0, len(expr.ArgNames)+1)
			prependedNames = append(prependedNames, "")
			prependedNames = append(prependedNames, expr.ArgNames...)
			expr.ArgNames = prependedNames
		}
		if len(expr.ArgShorthand) != 0 {
			prependedShorthand := make([]bool, 0, len(expr.ArgShorthand)+1)
			prependedShorthand = append(prependedShorthand, false)
			prependedShorthand = append(prependedShorthand, expr.ArgShorthand...)
			expr.ArgShorthand = prependedShorthand
		}
		if len(expr.ArgItemOrder) != 0 {
			prependedItems := make([]ast.CallArgItem, 0, len(expr.ArgItemOrder)+1)
			prependedItems = append(prependedItems, ast.CallArgItem{Position: fieldExpr.Object.Pos(), ArgIndex: 0})
			for _, item := range expr.ArgItemOrder {
				item.ArgIndex++
				prependedItems = append(prependedItems, item)
			}
			expr.ArgItemOrder = prependedItems
		}
		expr.Func = withCallTypeArgs(&ast.Ident{Position: fieldExpr.Position, Name: fieldExpr.Field})
		return extensionMethodCallRewriteApplied
	}
	method, ok, err := a.lookupVisibleExtensionMethod(fieldExpr.Field, receiverType)
	if err != nil {
		a.errorf(expr.Pos(), "%s", err.Error())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return extensionMethodCallRewriteInvalid
	}
	if !ok || method == nil || method.Symbol == nil {
		ufcsSym, ufcsOK, ufcsErr := a.lookupVisibleUFCSFunctionWithArity(fieldExpr.Field, receiverType, 1+len(expr.Args))
		if ufcsErr != nil {
			a.errorf(expr.Pos(), "%s", ufcsErr.Error())
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return extensionMethodCallRewriteInvalid
		}
		if !ufcsOK || ufcsSym == nil {
			// No extension method or free UFCS function matched: fall back to protocol-impl
			// UFCS dispatch (`value.method(args)` where the receiver's concrete type conforms
			// to a protocol — incl. default methods — declaring `method`).
			if status := a.rewriteConcreteImplMethodCall(expr, fieldExpr, receiverType, callTypeArgs); status != extensionMethodCallRewriteNone {
				return status
			}
			return extensionMethodCallRewriteNone
		}
		if ufcsSym.Deprecated != "" {
			a.deprecatedf(expr.Pos(), "%s", ufcsSym.Deprecated)
		}
		ufcsType, _ := ufcsSym.Type.(*FuncType)
		receiverArg := fieldExpr.Object
		if ufcsType != nil && len(ufcsType.Params) != 0 {
			receiverArg = a.prepareUFCSReceiverArg(fieldExpr.Object, receiverType, ufcsType.Params[0])
		}
		prependedArgs := make([]ast.Expr, 0, len(expr.Args)+1)
		prependedArgs = append(prependedArgs, receiverArg)
		prependedArgs = append(prependedArgs, expr.Args...)
		expr.Args = prependedArgs
		if len(expr.ArgNames) != 0 {
			prependedNames := make([]string, 0, len(expr.ArgNames)+1)
			prependedNames = append(prependedNames, "")
			prependedNames = append(prependedNames, expr.ArgNames...)
			expr.ArgNames = prependedNames
		}
		if len(expr.ArgShorthand) != 0 {
			prependedShorthand := make([]bool, 0, len(expr.ArgShorthand)+1)
			prependedShorthand = append(prependedShorthand, false)
			prependedShorthand = append(prependedShorthand, expr.ArgShorthand...)
			expr.ArgShorthand = prependedShorthand
		}
		if len(expr.ArgItemOrder) != 0 {
			prependedItems := make([]ast.CallArgItem, 0, len(expr.ArgItemOrder)+1)
			prependedItems = append(prependedItems, ast.CallArgItem{Position: receiverArg.Pos(), ArgIndex: 0})
			for _, item := range expr.ArgItemOrder {
				item.ArgIndex++
				prependedItems = append(prependedItems, item)
			}
			expr.ArgItemOrder = prependedItems
		}
		expr.Func = withCallTypeArgs(&ast.Ident{Position: fieldExpr.Position, Name: ufcsSym.Name})
		return extensionMethodCallRewriteApplied
	}
	prependedArgs := make([]ast.Expr, 0, len(expr.Args)+1)
	prependedArgs = append(prependedArgs, fieldExpr.Object)
	prependedArgs = append(prependedArgs, expr.Args...)
	expr.Args = prependedArgs
	if len(expr.ArgNames) != 0 {
		prependedNames := make([]string, 0, len(expr.ArgNames)+1)
		prependedNames = append(prependedNames, "")
		prependedNames = append(prependedNames, expr.ArgNames...)
		expr.ArgNames = prependedNames
	}
	if len(expr.ArgShorthand) != 0 {
		prependedShorthand := make([]bool, 0, len(expr.ArgShorthand)+1)
		prependedShorthand = append(prependedShorthand, false)
		prependedShorthand = append(prependedShorthand, expr.ArgShorthand...)
		expr.ArgShorthand = prependedShorthand
	}
	if len(expr.ArgItemOrder) != 0 {
		prependedItems := make([]ast.CallArgItem, 0, len(expr.ArgItemOrder)+1)
		prependedItems = append(prependedItems, ast.CallArgItem{Position: fieldExpr.Object.Pos(), ArgIndex: 0})
		for _, item := range expr.ArgItemOrder {
			item.ArgIndex++
			prependedItems = append(prependedItems, item)
		}
		expr.ArgItemOrder = prependedItems
	}
	expr.Func = withCallTypeArgs(&ast.Ident{Position: fieldExpr.Position, Name: method.Symbol.Name})
	return extensionMethodCallRewriteApplied
}

func (a *Analyzer) rewriteTypestateConstructorCall(expr *ast.CallExpr, fieldExpr *ast.FieldExpr) bool {
	if a == nil || expr == nil || fieldExpr == nil || fieldExpr.Field != "new" {
		return false
	}
	typeName, ok := fieldExpr.Object.(*ast.Ident)
	if !ok || typeName == nil || typeName.Name == "" || !a.exprResolvesToTypePath(fieldExpr.Object) {
		return false
	}
	hiddenName := "__typestate_" + typeName.Name + "_new"
	if _, _, found := a.lookupVisibleGlobal(hiddenName); !found {
		return false
	}
	expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: hiddenName}
	return true
}

func proofCarryingViewReceiverHelper(name string) bool {
	switch name {
	case "enumerate",
		"any", "all", "column", "where_kind",
		"readonly", "split_at", "chunks_exact", "reduce_sum":
		return true
	default:
		return false
	}
}

func (a *Analyzer) prepareUFCSReceiverArg(receiver ast.Expr, receiverType Type, expected Type) ast.Expr {
	if a == nil || receiver == nil || receiverType == nil || expected == nil {
		return receiver
	}
	if AssignableTo(expected, receiverType) {
		return receiver
	}
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil {
		return receiver
	}
	if _, ok := implicitCallLikeRefUpcastType(expectedRef, receiverType); ok {
		return receiver
	}
	if !AssignableTo(expectedRef.Elem, receiverType) {
		return receiver
	}
	autoref := ast.Expr(&ast.AddrOfExpr{Position: receiver.Pos(), Operand: receiver})
	if expectedRef.State == RefStateNonNull {
		return autoref
	}
	return &ast.CastExpr{
		Position: receiver.Pos(),
		Operand:  autoref,
		Target:   astTypeExprForBuiltinMethodRewrite(receiver.Pos(), expectedRef),
		Origin:   ast.CastExprOriginGeneral,
	}
}

func ufcsPreparedReceiverType(actual Type, expected Type) Type {
	if actual == nil || expected == nil {
		return actual
	}
	if AssignableTo(expected, actual) {
		return actual
	}
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil {
		return actual
	}
	if upcastType, ok := implicitCallLikeRefUpcastType(expectedRef, actual); ok {
		return upcastType
	}
	if AssignableTo(expectedRef.Elem, actual) {
		return expected
	}
	return actual
}

func (a *Analyzer) exprResolvesToTypePath(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	name, ok := qualifiedTypePathFromExpr(expr)
	if !ok || name == "" {
		return false
	}
	if _, ok := a.lookupTypeParam(name); ok {
		return true
	}
	_, _, ok = a.lookupVisibleType(name)
	return ok
}

func (a *Analyzer) tryConsumeSinkCallArg(funcExpr ast.Expr, fnType *FuncType, index int, arg ast.Expr, expected Type) bool {
	if a == nil || fnType == nil || arg == nil || !a.funcParamAllowsImplicitSink(funcExpr, fnType, index) {
		return false
	}
	if _, moved := explicitMoveOperand(arg); moved {
		return false
	}
	if !a.containsAffineHandleValues(expected, map[string]bool{}) {
		return false
	}
	key, ok := a.lookupAffineValueKey(arg)
	if !ok {
		return false
	}
	a.recordAffineConsumption(key, "argument to call "+strconv.Quote(fnType.Name))
	return true
}

func (a *Analyzer) funcParamAllowsImplicitSink(funcExpr ast.Expr, fnType *FuncType, index int) bool {
	if a == nil || fnType == nil || index < 0 {
		return false
	}
	if decl, resolvedType, ok := a.resolveSinkFuncDecl(funcExpr); ok && resolvedType != nil {
		if !resolvedType.SinkParamsKnown && decl != nil {
			a.inferFuncSinkParams(decl, resolvedType)
		}
		if resolvedType.SinkParamsKnown {
			sinkParams := resolvedType.SinkParams
			return index < len(sinkParams) && sinkParams[index]
		}
		fnType = resolvedType
	} else if a.parallel != nil {
		fnType = a.workerLocalSinkFuncType(nil, fnType)
	}
	if !fnType.SinkParamsKnown {
		a.inferFuncSinkParamsForExpr(funcExpr, fnType)
	}
	sinkParams := fnType.SinkParams
	return fnType.SinkParamsKnown && index < len(sinkParams) && sinkParams[index]
}

// boundsLengthHint completes an arity message for a `@bounds` extern (docs/127 D6): the
// caller most likely still passes the length the annotation now supplies.
func boundsLengthHint(ft *FuncType) string {
	if ft == nil || len(ft.BoundsLengthNames) == 0 {
		return ""
	}
	names := make([]string, 0, len(ft.BoundsLengthNames))
	for _, n := range ft.BoundsLengthNames {
		names = append(names, "\""+n+"\"")
	}
	return "; the length parameter " + strings.Join(names, ", ") + " is supplied by @bounds from the view's count, so remove the explicit argument"
}

// callNameHasOverloadSet reports whether some name a call spelled `name` can resolve
// to has two or more registered functions. Checking only the spelled name made a bare
// call skip overload dispatch while the same call, once qualified by an earlier pass,
// took it.
func (a *Analyzer) callNameHasOverloadSet(name string) bool {
	if len(a.ufcsFunctionsByName[name]) >= 2 {
		return true
	}
	for _, candidate := range a.visibleNameCandidates(name) {
		if len(a.ufcsFunctionsByName[candidate]) >= 2 {
			return true
		}
	}
	return false
}
