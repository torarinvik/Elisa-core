//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitSafeCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
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
	presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.call.present"))
	noneBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.call.none"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.call.merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, presentBB, noneBB)

	var (
		wrappedValue C.LLVMValueRef
		noneValue    C.LLVMValueRef
		presentEnd   C.LLVMBasicBlockRef
		noneEnd      C.LLVMBasicBlockRef
	)

	C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
	info := (*semantic.SafeCallInfo)(nil)
	if s != nil && s.g != nil && s.g.result != nil && s.g.result.SafeCalls != nil {
		info = s.g.result.SafeCalls[expr]
	}
	var (
		callValue C.LLVMValueRef
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
		if s.g.result.ExprTypes != nil {
			s.g.result.ExprTypes[fakeReceiver] = receiverArgType
			defer delete(s.g.result.ExprTypes, fakeReceiver)
		}
		callee, funcType, err := s.resolveCallTarget(synthetic)
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
		args := make([]C.LLVMValueRef, 0, 1+len(info.TailArgs)+len(info.ImplicitArgs))
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
		args := make([]C.LLVMValueRef, 0, len(loweredArgs))
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
		presentEnd = C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
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
		presentEnd = C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, noneBB)
	if !isVoidType(resultType) {
		optionalType := resultType.(*semantic.OptionalType)
		noneValue, err = s.buildOptionalNone(optionalType)
		if err != nil {
			return nil, nil, err
		}
	}
	noneEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("safe.call.result"))
	values := []C.LLVMValueRef{wrappedValue, noneValue}
	blocks := []C.LLVMBasicBlockRef{presentEnd, noneEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, nil
}
func (s *functionState) emitSafeTransformCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(expr.SafeReceiver)
	if err != nil {
		return nil, nil, err
	}
	presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.transform.present"))
	noneBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.transform.none"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.transform.merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, presentBB, noneBB)

	var (
		wrappedValue C.LLVMValueRef
		noneValue    C.LLVMValueRef
		presentEnd   C.LLVMBasicBlockRef
		noneEnd      C.LLVMBasicBlockRef
	)

	C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
	info := (*semantic.SafeCallInfo)(nil)
	if s != nil && s.g != nil && s.g.result != nil && s.g.result.SafeCalls != nil {
		info = s.g.result.SafeCalls[expr]
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
	if s.g.result.ExprTypes != nil {
		s.g.result.ExprTypes[fakeReceiver] = receiverArgType
		defer delete(s.g.result.ExprTypes, fakeReceiver)
	}
	callee, funcType, err := s.resolveCallTarget(synthetic)
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
	args := make([]C.LLVMValueRef, 0, len(synthetic.Args)+len(implicitArgs))
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
		presentEnd = C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
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
		presentEnd = C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, noneBB)
	if !isVoidType(resultType) {
		optionalType := resultType.(*semantic.OptionalType)
		noneValue, err = s.buildOptionalNone(optionalType)
		if err != nil {
			return nil, nil, err
		}
	}
	noneEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("safe.transform.result"))
	values := []C.LLVMValueRef{wrappedValue, noneValue}
	blocks := []C.LLVMBasicBlockRef{presentEnd, noneEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, nil
}
