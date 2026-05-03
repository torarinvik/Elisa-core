//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int llcontextLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef llctxAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef llctxAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned llctxMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void llctxSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = llctxAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, llctxMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef llctxCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = llctxAliasMDString(ctx, domainName);
	return llctxAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef llctxCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = llctxAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return llctxAliasMDNode(ctx, operands, 2);
}

static void llctxAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = llctxCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = llctxCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	llctxSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = llctxCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = llctxCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		llctxSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/semantic"
	"strconv"
	"unsafe"
)

func (s *functionState) emitErrorTagExpr(expr *ast.FieldExpr, errorType *semantic.ErrorSetType) (C.LLVMValueRef, semantic.Type, error) {
	if errorType == nil {
		return nil, nil, fmt.Errorf("missing error set for tag expression")
	}
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("missing error set qualifier for tag expression")
	}
	code, ok := errorType.TagCodeFor(ident.Name, expr.Field)
	if !ok {
		return nil, nil, fmt.Errorf("unknown error tag %s.%s", errorType.Name, expr.Field)
	}
	value, err := s.errorCodeConstant(code)
	if err != nil {
		return nil, nil, err
	}
	return value, errorType, nil
}
func (s *functionState) emitRaiseExpr(expr *ast.RaiseExpr) (C.LLVMValueRef, semantic.Type, error) {
	currentUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType)
	if !ok {
		return nil, nil, fmt.Errorf("raise requires an error-union return type")
	}
	var (
		errorValue C.LLVMValueRef
		errorType  semantic.Type
		err        error
	)
	if fieldExpr, ok := expr.Error.(*ast.FieldExpr); ok {
		if _, qualifiedTag, ok := s.errorTagInfo(fieldExpr); ok {
			mappedTag, matched := semantic.MatchErrorTag(currentUnion.Errors, qualifiedTag)
			if matched {
				code, ok := currentUnion.Errors.TagCode(mappedTag)
				if !ok {
					return nil, nil, fmt.Errorf("missing destination error tag %s", mappedTag)
				}
				errorValue, err = s.errorCodeConstant(code)
				errorType = currentUnion.Errors
			} else {
				errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
			}
		} else {
			errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
		}
	} else {
		errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := s.emitFunctionReturn(errorValue, errorType); err != nil {
		return nil, nil, err
	}
	return nil, s.exprType(expr), nil
}
func (s *functionState) emitTryExpr(expr *ast.TryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	if unionType, ok := s.exprType(expr.Value).(*semantic.ErrorUnionType); ok {
		fallibleValue, _, err := s.emitExpr(expr.Value, nil)
		if err != nil {
			return nil, nil, err
		}
		errorCode, err := s.extractErrorUnionCode(fallibleValue, unionType)
		if err != nil {
			return nil, nil, err
		}
		zeroCode, err := s.errorCodeConstant(0)
		if err != nil {
			return nil, nil, err
		}
		successCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), errorCode, zeroCode, cStringFree("try.ok"))

		if expr.Fallback == nil {
			okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.ok"))
			errBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.err"))
			C.LLVMBuildCondBr(s.builder, successCond, okBB, errBB)

			C.LLVMPositionBuilderAtEnd(s.builder, errBB)
			if _, ok := s.fnType.Return.(*semantic.ErrorUnionType); !ok {
				return nil, nil, fmt.Errorf("try propagation requires an error-union function return")
			}
			if err := s.emitFunctionReturn(errorCode, unionType.Errors); err != nil {
				return nil, nil, err
			}

			C.LLVMPositionBuilderAtEnd(s.builder, okBB)
			if isVoidType(resultType) {
				return nil, resultType, nil
			}
			payload, err := s.extractErrorUnionPayload(fallibleValue, unionType)
			if err != nil {
				return nil, nil, err
			}
			return payload, resultType, nil
		}

		okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.value"))
		fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.fallback"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.merge"))
		C.LLVMBuildCondBr(s.builder, successCond, okBB, fallbackBB)

		incomingValues := make([]C.LLVMValueRef, 0, 2)
		incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

		C.LLVMPositionBuilderAtEnd(s.builder, okBB)
		var okValue C.LLVMValueRef
		if !isVoidType(resultType) {
			okValue, err = s.extractErrorUnionPayload(fallibleValue, unionType)
			if err != nil {
				return nil, nil, err
			}
		}
		if !s.currentBlockTerminated() {
			okEnd := C.LLVMGetInsertBlock(s.builder)
			C.LLVMBuildBr(s.builder, mergeBB)
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, okValue)
				incomingBlocks = append(incomingBlocks, okEnd)
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
		fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
		if err != nil {
			return nil, nil, err
		}
		if !s.currentBlockTerminated() {
			fallbackEnd := C.LLVMGetInsertBlock(s.builder)
			C.LLVMBuildBr(s.builder, mergeBB)
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, fallbackValue)
				incomingBlocks = append(incomingBlocks, fallbackEnd)
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		if len(incomingBlocks) == 0 {
			C.LLVMBuildUnreachable(s.builder)
			return nil, resultType, nil
		}
		if isVoidType(resultType) {
			return nil, resultType, nil
		}
		if len(incomingValues) == 1 {
			return incomingValues[0], resultType, nil
		}
		phiType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("tryphi"))
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
		return phi, resultType, nil
	}
	optionalType, ok := s.exprType(expr.Value).(*semantic.OptionalType)
	if !ok {
		return nil, nil, fmt.Errorf("try requires a lowered fallible operand")
	}
	if expr.Fallback == nil {
		return nil, nil, fmt.Errorf("try without else is only supported for error unions")
	}
	fallibleValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	presentValue, err := s.extractOptionalPresent(fallibleValue, optionalType)
	if err != nil {
		return nil, nil, err
	}
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.value"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, okBB, fallbackBB)

	incomingValues := make([]C.LLVMValueRef, 0, 2)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	var okValue C.LLVMValueRef
	if !isVoidType(resultType) {
		okValue, err = s.extractOptionalPayload(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, err
		}
	}
	if !s.currentBlockTerminated() {
		okEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, okValue)
			incomingBlocks = append(incomingBlocks, okEnd)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
	if err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		fallbackEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, fallbackValue)
			incomingBlocks = append(incomingBlocks, fallbackEnd)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingBlocks) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 {
		return incomingValues[0], resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("tryphi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) emitCatchExpr(expr *ast.CatchExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	unionType, ok := s.exprType(expr.Value).(*semantic.ErrorUnionType)
	if !ok {
		return nil, nil, fmt.Errorf("catch requires an error-union operand")
	}
	fallibleValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	errorCode, err := s.extractErrorUnionCode(fallibleValue, unionType)
	if err != nil {
		return nil, nil, err
	}
	zeroCode, err := s.errorCodeConstant(0)
	if err != nil {
		return nil, nil, err
	}
	successCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), errorCode, zeroCode, cStringFree("catch.ok"))
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.value"))
	dispatchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.dispatch"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.merge"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.fail"))
	C.LLVMBuildCondBr(s.builder, successCond, successBB, dispatchBB)

	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	covered := map[string]bool{}

	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	successValue, err := s.extractErrorUnionPayload(fallibleValue, unionType)
	if err != nil {
		return nil, nil, err
	}
	s.pushScope()
	if !isVoidType(unionType.Value) {
		successPtr, err := s.emitStackTempValue(successValue, unionType.Value, "catch.value")
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		s.defineBinding(expr.Success.Name, valueBinding{ptr: successPtr, typ: unionType.Value, mutable: false})
	}
	armValue, reachable, err := s.emitMatchExprArmBody(expr.Success.Body, resultType)
	if err != nil {
		s.popScope()
		return nil, nil, err
	}
	if reachable && !s.currentBlockTerminated() {
		armEnd := C.LLVMGetInsertBlock(s.builder)
		incomingBlocks = append(incomingBlocks, armEnd)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, armValue)
		}
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	s.popScope()

	C.LLVMPositionBuilderAtEnd(s.builder, dispatchBB)
	switchInst := C.LLVMBuildSwitch(s.builder, errorCode, failBB, C.unsigned(len(expr.Arms)))
	for _, arm := range expr.Arms {
		matchedTag, ok := semantic.MatchErrorTag(unionType.Errors, arm.Name)
		if !ok {
			return nil, nil, fmt.Errorf("catch arm %q does not match %s", arm.Name, semantic.ErrorSetDiagnosticName(unionType.Errors))
		}
		if covered[matchedTag] {
			continue
		}
		covered[matchedTag] = true
		code, ok := unionType.Errors.TagCode(matchedTag)
		if !ok {
			return nil, nil, fmt.Errorf("missing error tag code for %s", matchedTag)
		}
		tagConst, err := s.errorCodeConstant(code)
		if err != nil {
			return nil, nil, err
		}
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.arm"))
		C.LLVMAddCase(switchInst, tagConst, bodyBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			incomingBlocks = append(incomingBlocks, armEnd)
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, armValue)
			}
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if len(covered) == len(unionType.Errors.Tags) || semantic.IsNeverType(resultType) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		failEnd := C.LLVMGetInsertBlock(s.builder)
		incomingBlocks = append(incomingBlocks, failEnd)
		if !isVoidType(resultType) {
			llvmType, err := s.g.lowerType(resultType)
			if err != nil {
				return nil, nil, err
			}
			incomingValues = append(incomingValues, C.LLVMGetUndef(llvmType))
		}
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingBlocks) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("catchphi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) emitUnwrapElseExpr(expr *ast.UnwrapElseExpr) (C.LLVMValueRef, semantic.Type, error) {
	valueType, ok := s.exprType(expr.Value).(*semantic.RefType)
	if !ok {
		return nil, nil, fmt.Errorf("else recovery requires a reference operand")
	}
	resultType := s.exprType(expr)
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	llvmRefType, err := s.g.lowerType(valueType)
	if err != nil {
		return nil, nil, err
	}
	nullValue := C.LLVMConstNull(llvmRefType)
	nonNullCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), value, nullValue, cStringFree("unwrap.nonnull"))
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.ok"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.merge"))
	C.LLVMBuildCondBr(s.builder, nonNullCond, okBB, fallbackBB)

	incomingValues := make([]C.LLVMValueRef, 0, 2)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	if !s.currentBlockTerminated() {
		okEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		incomingValues = append(incomingValues, value)
		incomingBlocks = append(incomingBlocks, okEnd)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
	if err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		fallbackEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		incomingValues = append(incomingValues, fallbackValue)
		incomingBlocks = append(incomingBlocks, fallbackEnd)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 {
		return incomingValues[0], resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("unwrapphi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) emitIntLiteral(expr *ast.IntLit) (C.LLVMValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.result.NamedTypes["int"]
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := strconv.ParseUint(expr.Value, 0, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse integer literal %q: %w", expr.Value, err)
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(parsed), 0), t, nil
}
func (s *functionState) emitFloatLiteral(expr *ast.FloatLit) (C.LLVMValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.result.NamedTypes["f64"]
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := strconv.ParseFloat(expr.Value, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse float literal %q: %w", expr.Value, err)
	}
	return C.LLVMConstReal(llvmType, C.double(parsed)), t, nil
}
func (s *functionState) emitStringLiteral(expr *ast.StringLit, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	name := cString("str")
	defer C.free(unsafe.Pointer(name))
	text := cString(expr.Value)
	defer C.free(unsafe.Pointer(text))
	value := C.LLVMBuildGlobalStringPtr(s.builder, text, name)
	resultType := s.exprType(expr)
	if expected != nil && isStringViewCarrierType(expected) {
		resultType = expected
	}
	if isStringViewCarrierType(resultType) {
		viewLLVMType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		i64Type, err := s.g.lowerType(s.g.result.NamedTypes["i64"])
		if err != nil {
			return nil, nil, err
		}
		viewValue := C.LLVMGetUndef(viewLLVMType)
		viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, value, 0, cStringFree("str.view.data"))
		viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, C.LLVMConstInt(i64Type, C.ulonglong(len(expr.Value)), 0), 1, cStringFree("str.view.len"))
		return viewValue, resultType, nil
	}
	return value, resultType, nil
}
func (s *functionState) emitCharLiteral(expr *ast.CharLit) (C.LLVMValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.result.NamedTypes["char"]
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, ok := semantic.ParseCharLiteral(expr)
	if !ok {
		return nil, nil, fmt.Errorf("failed to parse char literal %q", expr.Value)
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(parsed), 0), t, nil
}
