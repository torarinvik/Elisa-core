//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

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
	errorValue, err := s.extractErrorUnionCode(fallibleValue, unionType)
	if err != nil {
		return nil, nil, err
	}
	errorCode, err := s.extractErrorSetCode(errorValue, unionType.Errors)
	if err != nil {
		return nil, nil, err
	}
	zeroCode, err := s.errorCodeConstant(0)
	if err != nil {
		return nil, nil, err
	}
	successCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), errorCode, zeroCode, cStringFree("catch.ok"))
	var errorBindingArm *ast.CatchArm
	for i := range expr.Arms {
		if expr.Arms[i].ErrorBinding {
			errorBindingArm = &expr.Arms[i]
			break
		}
	}
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.value"))
	dispatchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.dispatch"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.merge"))
	failName := "catch.fail"
	if errorBindingArm != nil {
		failName = "catch.error"
	}
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(failName))
	C.LLVMBuildCondBr(s.builder, successCond, successBB, dispatchBB)

	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	covered := map[string]bool{}

	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	s.pushScope()
	// A void error union has no success payload to bind; only extract+bind when the
	// ok value is non-void (otherwise extractErrorUnionPayload would fail).
	if !isVoidType(unionType.Value) {
		successValue, err := s.extractErrorUnionPayload(fallibleValue, unionType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
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
		if arm.ErrorBinding {
			continue
		}
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
		// Bind the matched variant's payload (`E.Bad(x, y):`). The error-set value is a
		// {code, flat-per-variant-payloads} struct (see buildErrorSetValue); this variant's
		// fields start at base = 1 + the payload counts of all earlier variants.
		if len(arm.Payload) > 0 && unionType.Errors.HasPayloads() {
			base := 1
			for _, candidate := range unionType.Errors.Tags {
				if candidate == matchedTag {
					break
				}
				base += len(unionType.Errors.PayloadForTag(candidate))
			}
			fieldTypes := unionType.Errors.PayloadForTag(matchedTag)
			for i, binder := range arm.Payload {
				if binder == "_" || i >= len(fieldTypes) {
					continue
				}
				fieldVal := C.LLVMBuildExtractValue(s.builder, errorValue, C.unsigned(base+i), cStringFree("catch.payload"))
				payloadPtr, perr := s.emitStackTempValue(fieldVal, fieldTypes[i], "catch.payload")
				if perr != nil {
					s.popScope()
					return nil, nil, perr
				}
				s.defineBinding(binder, valueBinding{ptr: payloadPtr, typ: fieldTypes[i], mutable: false})
			}
		}
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
	if errorBindingArm != nil {
		s.pushScope()
		errorPtr, err := s.emitStackTempValue(errorValue, unionType.Errors, "catch.error")
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		s.defineBinding(errorBindingArm.Name, valueBinding{ptr: errorPtr, typ: unionType.Errors, mutable: false})
		armValue, reachable, err := s.emitMatchExprArmBody(errorBindingArm.Body, resultType)
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
	} else if len(covered) == len(unionType.Errors.Tags) || semantic.IsNeverType(resultType) {
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
	valueType := s.exprType(expr.Value)
	resultType := s.exprType(expr)
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	var okCond C.LLVMValueRef
	var okValue C.LLVMValueRef
	switch t := valueType.(type) {
	case *semantic.RefType:
		llvmRefType, err := s.g.lowerType(valueType)
		if err != nil {
			return nil, nil, err
		}
		nullValue := C.LLVMConstNull(llvmRefType)
		okCond = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), value, nullValue, cStringFree("unwrap.nonnull"))
		okValue = value
	case *semantic.OptionalType:
		okCond, err = s.extractOptionalPresent(value, t)
		if err != nil {
			return nil, nil, err
		}
		if !isVoidType(resultType) {
			okValue, err = s.extractOptionalPayload(value, t)
			if err != nil {
				return nil, nil, err
			}
		}
	default:
		return nil, nil, fmt.Errorf("else recovery requires an optional value or nullable ref")
	}
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.ok"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.merge"))
	C.LLVMBuildCondBr(s.builder, okCond, okBB, fallbackBB)

	incomingValues := make([]C.LLVMValueRef, 0, 2)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	if !s.currentBlockTerminated() {
		okEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, okValue)
		}
		incomingBlocks = append(incomingBlocks, okEnd)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	recovery := astRecoveryClauseForExpr(expr.Recovery, expr.Fallback, expr.Position)
	fallbackValue, reachable, err := s.emitRecoveryClause(recovery, resultType, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if reachable && !s.currentBlockTerminated() {
		fallbackEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, fallbackValue)
		}
		incomingBlocks = append(incomingBlocks, fallbackEnd)
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
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("unwrapphi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
