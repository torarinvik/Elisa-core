package semantic

import (
	"elisacore/src/ast"
	"strings"
)

func (a *Analyzer) safeChainReceiverType(receiverType Type) (Type, bool) {
	if receiverType == nil {
		return nil, false
	}
	if opt, ok := receiverType.(*OptionalType); ok && opt != nil && opt.Value != nil {
		return opt.Value, true
	}
	if ref, ok := receiverType.(*RefType); ok && ref != nil && ref.State != RefStateNonNull {
		return cloneRefTypeWithState(ref, RefStateNonNull), true
	}
	return nil, false
}

func optionalizeSafeChainResult(result Type) Type {
	if result == nil || IsInvalidType(result) || IsNeverType(result) {
		return result
	}
	return &OptionalType{Value: result}
}

func (a *Analyzer) analyzeSafeFieldExpr(expr *ast.FieldExpr) Type {
	if a == nil || expr == nil || expr.Object == nil || expr.Field == "" {
		return invalidType
	}
	receiverType := a.analyzeExpr(expr.Object)
	baseReceiverType, ok := a.safeChainReceiverType(receiverType)
	if !ok {
		a.errorf(expr.Pos(), nullableRefRequirementMessage("optional chaining receiver", receiverType.String()))
		return invalidType
	}
	if field, ok := cstrSyntheticField(baseReceiverType, expr.Field); ok {
		field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
		return optionalizeSafeChainResult(field.Type)
	}
	field, ok := a.lookupField(baseReceiverType, expr.Field, expr.Pos())
	if !ok {
		return invalidType
	}
	field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
	return optionalizeSafeChainResult(field.Type)
}

func (a *Analyzer) analyzeSafeCallExpr(expr *ast.CallExpr) Type {
	if a == nil || expr == nil {
		return invalidType
	}
	if expr.SafeReceiver != nil {
		return a.analyzeSafeTransformCallExpr(expr)
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil || fieldExpr.Field == "" {
		a.errorf(expr.Pos(), "optional call requires member-call syntax")
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	baseReceiverType, ok := a.safeChainReceiverType(receiverType)
	if !ok {
		a.errorf(expr.Pos(), nullableRefRequirementMessage("optional chaining receiver", receiverType.String()))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	if field, ok := a.lookupFieldNoError(baseReceiverType, fieldExpr.Field); ok {
		field.Type = a.specializeProjectedFunctionFieldType(fieldExpr, field.Type)
		ft, ok := field.Type.(*FuncType)
		if !ok {
			a.errorf(expr.Pos(), "cannot call non-function value of type %s", field.Type)
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
		orderedArgs, orderedOK := a.resolveFunctionCallArgs(expr, ft)
		if !orderedOK {
			return invalidType
		}
		resultType := a.analyzeResolvedCallExpr(expr, ft, orderedArgs)
		if ft.Return == nil || isVoidType(resultType) {
			return a.namedTypes["void"]
		}
		return optionalizeSafeChainResult(resultType)
	}
	method, methodOK, err := a.lookupVisibleExtensionMethod(fieldExpr.Field, baseReceiverType)
	if err != nil {
		a.errorf(expr.Pos(), "%s", err.Error())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	var resolvedSym *Symbol
	if methodOK && method != nil {
		resolvedSym = method.Symbol
	} else {
		resolvedSym, methodOK, err = a.lookupVisibleUFCSFunctionWithArity(fieldExpr.Field, baseReceiverType, 1+len(expr.Args))
		if err != nil {
			a.errorf(expr.Pos(), "%s", err.Error())
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
		if !methodOK || resolvedSym == nil {
			a.errorf(expr.Pos(), "optional call receiver %s has no member or UFCS function %q", receiverType, fieldExpr.Field)
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
	}
	ft, ok := resolvedSym.Type.(*FuncType)
	if !ok || ft == nil {
		a.errorf(expr.Pos(), "cannot call non-function value of type %s", resolvedSym.Type)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	synthetic := &ast.CallExpr{
		Position: expr.Position,
		Func:     &ast.Ident{Position: fieldExpr.Position, Name: resolvedSym.Name},
		Args:     append([]ast.Expr{&ast.ZeroedLit{Position: fieldExpr.Object.Pos()}}, expr.Args...),
		ArgNames: append([]string{""}, expr.ArgNames...),
	}
	orderedArgs, orderedOK := a.resolveFunctionCallArgs(synthetic, ft)
	if !orderedOK {
		return invalidType
	}
	resultType := a.analyzeResolvedCallExpr(synthetic, ft, orderedArgs)
	resolvedFuncType := ft
	if analyzedType, ok := a.exprType(synthetic.Func).(*FuncType); ok && analyzedType != nil {
		resolvedFuncType = analyzedType
	}
	receiverArgType := baseReceiverType
	if resolvedFuncType != nil && len(resolvedFuncType.Params) != 0 {
		receiverArgType = ufcsPreparedReceiverType(baseReceiverType, resolvedFuncType.Params[0])
	}
	a.safeCalls[expr] = &SafeCallInfo{
		ResolvedFuncName: resolvedSym.Name,
		ResolvedFuncType: resolvedFuncType,
		ReceiverArgType:  receiverArgType,
		TailArgs:         append([]ast.Expr(nil), orderedArgs[1:]...),
		ImplicitArgs:     append([]ast.Expr(nil), synthetic.ResolvedImplicitArgs...),
	}
	if ft.Return == nil || isVoidType(resultType) {
		return a.namedTypes["void"]
	}
	return optionalizeSafeChainResult(resultType)
}

func (a *Analyzer) analyzeSafeTransformCallExpr(expr *ast.CallExpr) Type {
	receiverType := a.analyzeExpr(expr.SafeReceiver)
	baseReceiverType, ok := a.safeChainReceiverType(receiverType)
	if !ok {
		a.errorf(expr.Pos(), nullableRefRequirementMessage("optional chaining receiver", receiverType.String()))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	placeholder := &ast.ZeroedLit{Position: expr.SafeReceiver.Pos()}
	synthetic := &ast.CallExpr{
		Position: expr.Position,
		Func:     expr.Func,
		Args:     append([]ast.Expr{placeholder}, expr.Args...),
		ArgNames: append([]string{""}, expr.ArgNames...),
	}
	resultType := a.analyzeCallExpr(synthetic)
	resolvedFuncType, _ := a.exprType(synthetic.Func).(*FuncType)
	orderedArgs := synthetic.ResolvedArgs
	if !synthetic.ResolvedArgsValid {
		orderedArgs = synthetic.Args
	}
	receiverArgIndex := 0
	for i, arg := range orderedArgs {
		if arg == placeholder {
			receiverArgIndex = i
			break
		}
	}
	receiverArgType := baseReceiverType
	if analyzedReceiverArgType := a.exprType(placeholder); analyzedReceiverArgType != nil {
		receiverArgType = analyzedReceiverArgType
	} else if resolvedFuncType != nil && receiverArgIndex < len(resolvedFuncType.Params) {
		receiverArgType = ufcsPreparedReceiverType(baseReceiverType, resolvedFuncType.Params[receiverArgIndex])
	}
	a.safeCalls[expr] = &SafeCallInfo{
		TransformFunc:    synthetic.Func,
		TransformArgs:    append([]ast.Expr(nil), orderedArgs...),
		ReceiverArgIndex: receiverArgIndex,
		ResolvedFuncType: resolvedFuncType,
		ReceiverArgType:  receiverArgType,
		TailArgs:         append([]ast.Expr(nil), synthetic.Args[1:]...),
		ImplicitArgs:     append([]ast.Expr(nil), synthetic.ResolvedImplicitArgs...),
	}
	if isVoidType(resultType) {
		return a.namedTypes["void"]
	}
	return optionalizeSafeChainResult(resultType)
}

// programLivedRegionArgBinding: docs/128 -- a region parameter carried only by reference
// parameters whose arguments are of a program-lived type (a struct whose every field is
// `@source`) binds to the program region. The callee's growth of those fields allocates where
// their storage already lives; the program region outlives every other storage.
func (a *Analyzer) programLivedRegionArgBinding(ft *FuncType, args []ast.Expr, name string) (string, bool) {
	if a == nil || ft == nil || len(a.programRegions) != 1 {
		return "", false
	}
	program := ""
	for region := range a.programRegions {
		program = region
	}
	carriers := 0
	for i, pt := range ft.Params {
		ref, isRef := pt.(*RefType)
		if !isRef || ref == nil || ref.Region != name {
			if strings.Contains(typeString(pt), "@"+name) {
				return "", false
			}
			continue
		}
		if i >= len(args) || args[i] == nil {
			return "", false
		}
		at := a.exprType(args[i])
		if at == nil || !a.typeCanContainRegionRefs(stripRefForBounds(at), map[string]bool{}) || !a.typeIsProgramLivedCached(stripRefForBounds(at)) {
			return "", false
		}
		carriers++
	}
	if carriers == 0 {
		return "", false
	}
	return program, true
}
