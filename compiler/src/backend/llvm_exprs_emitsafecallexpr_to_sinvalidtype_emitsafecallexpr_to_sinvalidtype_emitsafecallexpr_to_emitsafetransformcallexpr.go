//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitSafeCallExpr(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, error) {
	if expr.SafeReceiver != nil {
		return s.emitSafeTransformCallExpr(expr)
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return nil, nil, fmt.Errorf("optional call requires member-call syntax")
	}
	resultType := s.exprType(expr)
	presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(fieldExpr.Object)
	if err != nil {
		return nil, nil, err
	}
	presentBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.call.present"))
	noneBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.call.none"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.call.merge"))
	llvmBuildCondBr(s.builder, presentValue, presentBB, noneBB)

	var (
		wrappedValue llvmIRValueRef
		noneValue    llvmIRValueRef
		presentEnd   llvmIRBlockRef
		noneEnd      llvmIRBlockRef
	)

	llvmPositionBuilderAtEnd(s.builder, presentBB)
	info := (*semantic.SafeCallInfo)(nil)
	if s != nil && s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnSafeCallsMap() {
		info = s.g.semanticReadView().ValueSafeCalls(expr)
	}
	var (
		callValue llvmIRValueRef
		callType  semantic.Type
	)
	if info != nil && info.ResolvedFuncName != "" {
		receiverArgType := info.ReceiverArgType
		if receiverArgType == nil {
			receiverArgType = receiverType
		}
		fakeReceiver := &ast.ZeroedLit{Position: fieldExpr.Object.Pos()}
		synthetic := &ast.CallExpr{
			Position:                  expr.Position,
			Func:                      &ast.Ident{Position: fieldExpr.Position, Name: info.ResolvedFuncName},
			Args:                      append([]ast.Expr{fakeReceiver}, info.TailArgs...),
			ResolvedImplicitArgsValid: len(info.ImplicitArgs) != 0,
			ResolvedImplicitArgs:      append([]ast.Expr(nil), info.ImplicitArgs...),
		}
		if s.g.semanticReadView().HasOwnExprTypesMap() {
			{
				key, value := fakeReceiver, receiverArgType
				s.g.setResultExprType(key, value)
			}
			defer s.g.deleteResultExprType(fakeReceiver)
		}
		callee, funcType, _, err := s.resolveCallTarget(synthetic)
		if err != nil {
			return nil, nil, err
		}
		expectedReceiverType := receiverArgType
		if len(funcType.Params) != 0 {
			expectedReceiverType = funcType.Params[0]
		}
		receiverArg, _, err := s.emitPreparedUFCSReceiverValue(receiverValue, receiverType, expectedReceiverType, "safe.call.receiver")
		if err != nil {
			return nil, nil, err
		}
		args := make([]llvmIRValueRef, 0, 1+len(info.TailArgs)+len(info.ImplicitArgs))
		args = append(args, receiverArg)
		for i, arg := range info.TailArgs {
			paramIndex := i + 1
			var expected semantic.Type
			if paramIndex < len(funcType.Params) {
				expected = funcType.Params[paramIndex]
			}
			value, _, err := s.emitCallArg(arg, expected, funcType, paramIndex)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, value)
		}
		implicitStart := 1 + len(info.TailArgs)
		for i, arg := range info.ImplicitArgs {
			paramIndex := implicitStart + i
			var expected semantic.Type
			if paramIndex < len(funcType.Params) {
				expected = funcType.Params[paramIndex]
			}
			value, _, err := s.emitCallArg(arg, expected, funcType, paramIndex)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, value)
		}
		callValue, callType, err = s.emitResolvedCall(callee, funcType, true, args, nil)
		if err != nil {
			return nil, nil, err
		}
	} else {
		calleeValue, calleeType, err := s.emitFieldValueFromObjectValue(receiverValue, receiverType, fieldExpr.Field, "safe.call.callee")
		if err != nil {
			return nil, nil, err
		}
		funcType, ok := calleeType.(*semantic.FuncType)
		if !ok || funcType == nil {
			return nil, nil, fmt.Errorf("cannot call non-function value of type %s", calleeType.String())
		}
		loweredArgs := expr.LoweredArgs()
		args := make([]llvmIRValueRef, 0, len(loweredArgs))
		for i, arg := range loweredArgs {
			var expected semantic.Type
			if i < len(funcType.Params) {
				expected = funcType.Params[i]
			}
			value, _, err := s.emitCallArg(arg, expected, funcType, i)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, value)
		}
		callValue, callType, err = s.emitResolvedCall(calleeValue, funcType, false, args, nil)
		if err != nil {
			return nil, nil, err
		}
	}
	if isVoidType(resultType) {
		presentEnd = llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
	} else {
		optionalType, ok := resultType.(*semantic.OptionalType)
		if !ok || optionalType == nil || optionalType.Value == nil {
			return nil, nil, fmt.Errorf("optional call requires an optional result type")
		}
		callValue, err = s.coerceValue(callValue, callType, optionalType.Value)
		if err != nil {
			return nil, nil, err
		}
		wrappedValue, err = s.buildOptionalSome(optionalType, callValue)
		if err != nil {
			return nil, nil, err
		}
		presentEnd = llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
	}

	llvmPositionBuilderAtEnd(s.builder, noneBB)
	if !isVoidType(resultType) {
		optionalType := resultType.(*semantic.OptionalType)
		noneValue, err = s.buildOptionalNone(optionalType)
		if err != nil {
			return nil, nil, err
		}
	}
	noneEnd = llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := llvmBuildPhi(s.builder, resultLLVMType, cStringFree("safe.call.result"))
	values := []llvmIRValueRef{wrappedValue, noneValue}
	blocks := []llvmIRBlockRef{presentEnd, noneEnd}
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return phi, resultType, nil
}
func (s *functionState) emitSafeTransformCallExpr(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(expr.SafeReceiver)
	if err != nil {
		return nil, nil, err
	}
	presentBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.transform.present"))
	noneBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.transform.none"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.transform.merge"))
	llvmBuildCondBr(s.builder, presentValue, presentBB, noneBB)

	var (
		wrappedValue llvmIRValueRef
		noneValue    llvmIRValueRef
		presentEnd   llvmIRBlockRef
		noneEnd      llvmIRBlockRef
	)

	llvmPositionBuilderAtEnd(s.builder, presentBB)
	info := (*semantic.SafeCallInfo)(nil)
	if s != nil && s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnSafeCallsMap() {
		info = s.g.semanticReadView().ValueSafeCalls(expr)
	}
	transformFunc := expr.Func
	transformArgs := append([]ast.Expr(nil), expr.Args...)
	receiverArgIndex := 0
	var implicitArgs []ast.Expr
	receiverArgType := receiverType
	if info != nil {
		if info.TransformFunc != nil {
			transformFunc = info.TransformFunc
		}
		if len(info.TransformArgs) != 0 {
			transformArgs = append([]ast.Expr(nil), info.TransformArgs...)
			receiverArgIndex = info.ReceiverArgIndex
		} else {
			transformArgs = append([]ast.Expr{nil}, info.TailArgs...)
		}
		implicitArgs = info.ImplicitArgs
		if info.ReceiverArgType != nil {
			receiverArgType = info.ReceiverArgType
		}
	}
	fakeReceiver := &ast.ZeroedLit{Position: expr.SafeReceiver.Pos()}
	synthetic := &ast.CallExpr{
		Position:                  expr.Position,
		Func:                      transformFunc,
		Args:                      append([]ast.Expr(nil), transformArgs...),
		ResolvedImplicitArgsValid: len(implicitArgs) != 0,
		ResolvedImplicitArgs:      append([]ast.Expr(nil), implicitArgs...),
	}
	if receiverArgIndex < 0 || receiverArgIndex > len(synthetic.Args) {
		receiverArgIndex = 0
	}
	if len(synthetic.Args) == 0 {
		synthetic.Args = []ast.Expr{fakeReceiver}
		receiverArgIndex = 0
	} else {
		synthetic.Args[receiverArgIndex] = fakeReceiver
	}
	if s.g.semanticReadView().HasOwnExprTypesMap() {
		{
			key, value := fakeReceiver, receiverArgType
			s.g.setResultExprType(key, value)
		}
		defer s.g.deleteResultExprType(fakeReceiver)
	}
	callee, funcType, _, err := s.resolveCallTarget(synthetic)
	if err != nil {
		return nil, nil, err
	}
	expectedReceiverType := receiverArgType
	if receiverArgIndex < len(funcType.Params) {
		expectedReceiverType = funcType.Params[receiverArgIndex]
	}
	receiverArg, _, err := s.emitPreparedUFCSReceiverValue(receiverValue, receiverType, expectedReceiverType, "safe.transform.receiver")
	if err != nil {
		return nil, nil, err
	}
	args := make([]llvmIRValueRef, 0, len(synthetic.Args)+len(implicitArgs))
	for i, arg := range synthetic.Args {
		if i == receiverArgIndex {
			args = append(args, receiverArg)
			continue
		}
		paramIndex := i
		var expected semantic.Type
		if paramIndex < len(funcType.Params) {
			expected = funcType.Params[paramIndex]
		}
		value, _, err := s.emitCallArg(arg, expected, funcType, paramIndex)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, value)
	}
	implicitStart := len(synthetic.Args)
	for i, arg := range implicitArgs {
		paramIndex := implicitStart + i
		var expected semantic.Type
		if paramIndex < len(funcType.Params) {
			expected = funcType.Params[paramIndex]
		}
		value, _, err := s.emitCallArg(arg, expected, funcType, paramIndex)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, value)
	}
	callValue, callType, err := s.emitResolvedCall(callee, funcType, true, args, nil)
	if err != nil {
		return nil, nil, err
	}
	if isVoidType(resultType) {
		presentEnd = llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
	} else {
		optionalType, ok := resultType.(*semantic.OptionalType)
		if !ok || optionalType == nil || optionalType.Value == nil {
			return nil, nil, fmt.Errorf("optional transform call requires an optional result type")
		}
		callValue, err = s.coerceValue(callValue, callType, optionalType.Value)
		if err != nil {
			return nil, nil, err
		}
		wrappedValue, err = s.buildOptionalSome(optionalType, callValue)
		if err != nil {
			return nil, nil, err
		}
		presentEnd = llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
	}

	llvmPositionBuilderAtEnd(s.builder, noneBB)
	if !isVoidType(resultType) {
		optionalType := resultType.(*semantic.OptionalType)
		noneValue, err = s.buildOptionalNone(optionalType)
		if err != nil {
			return nil, nil, err
		}
	}
	noneEnd = llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := llvmBuildPhi(s.builder, resultLLVMType, cStringFree("safe.transform.result"))
	values := []llvmIRValueRef{wrappedValue, noneValue}
	blocks := []llvmIRBlockRef{presentEnd, noneEnd}
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return phi, resultType, nil
}
