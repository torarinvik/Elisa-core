//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitCatchExpr(expr *ast.CatchExpr) (llvmIRValueRef, semantic.Type, error) {
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
	successCond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), errorCode, zeroCode, cStringFree("catch.ok"))
	var errorBindingArm *ast.CatchArm
	for i := range expr.Arms {
		if expr.Arms[i].ErrorBinding {
			errorBindingArm = &expr.Arms[i]
			break
		}
	}
	successBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.value"))
	dispatchBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.dispatch"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.merge"))
	failName := "catch.fail"
	if errorBindingArm != nil {
		failName = "catch.error"
	}
	failBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(failName))
	llvmBuildCondBr(s.builder, successCond, successBB, dispatchBB)

	incomingValues := make([]llvmIRValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]llvmIRBlockRef, 0, len(expr.Arms)+1)
	covered := map[string]bool{}

	llvmPositionBuilderAtEnd(s.builder, successBB)
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
		armEnd := llvmGetInsertBlock(s.builder)
		incomingBlocks = append(incomingBlocks, armEnd)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, armValue)
		}
		llvmBuildBr(s.builder, mergeBB)
	}
	s.popScope()

	llvmPositionBuilderAtEnd(s.builder, dispatchBB)
	switchInst := llvmBuildSwitch(s.builder, errorCode, failBB, llvmIRUnsigned(len(expr.Arms)))
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
		bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.arm"))
		llvmAddCase(switchInst, tagConst, bodyBB)
		llvmPositionBuilderAtEnd(s.builder, bodyBB)
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
				fieldVal := llvmBuildExtractValue(s.builder, errorValue, llvmIRUnsigned(base+i), cStringFree("catch.payload"))
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
			armEnd := llvmGetInsertBlock(s.builder)
			incomingBlocks = append(incomingBlocks, armEnd)
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, armValue)
			}
			llvmBuildBr(s.builder, mergeBB)
		}
		s.popScope()
	}

	llvmPositionBuilderAtEnd(s.builder, failBB)
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
			armEnd := llvmGetInsertBlock(s.builder)
			incomingBlocks = append(incomingBlocks, armEnd)
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, armValue)
			}
			llvmBuildBr(s.builder, mergeBB)
		}
		s.popScope()
	} else if len(covered) == len(unionType.Errors.Tags) || semantic.IsNeverType(resultType) {
		llvmBuildUnreachable(s.builder)
	} else {
		failEnd := llvmGetInsertBlock(s.builder)
		incomingBlocks = append(incomingBlocks, failEnd)
		if !isVoidType(resultType) {
			llvmType, err := s.g.lowerType(resultType)
			if err != nil {
				return nil, nil, err
			}
			incomingValues = append(incomingValues, llvmGetUndef(llvmType))
		}
		llvmBuildBr(s.builder, mergeBB)
	}

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingBlocks) == 0 {
		llvmBuildUnreachable(s.builder)
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
	phi := llvmBuildPhi(s.builder, phiType, cStringFree("catchphi"))
	llvmAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), llvmIRUnsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) emitUnwrapElseExpr(expr *ast.UnwrapElseExpr) (llvmIRValueRef, semantic.Type, error) {
	valueType := s.exprType(expr.Value)
	resultType := s.exprType(expr)
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	var okCond llvmIRValueRef
	var okValue llvmIRValueRef
	switch t := valueType.(type) {
	case *semantic.RefType:
		llvmRefType, err := s.g.lowerType(valueType)
		if err != nil {
			return nil, nil, err
		}
		nullValue := llvmConstNull(llvmRefType)
		okCond = llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), value, nullValue, cStringFree("unwrap.nonnull"))
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
	okBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.ok"))
	fallbackBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.fallback"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.merge"))
	llvmBuildCondBr(s.builder, okCond, okBB, fallbackBB)

	incomingValues := make([]llvmIRValueRef, 0, 2)
	incomingBlocks := make([]llvmIRBlockRef, 0, 2)

	llvmPositionBuilderAtEnd(s.builder, okBB)
	if !s.currentBlockTerminated() {
		okEnd := llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, okValue)
		}
		incomingBlocks = append(incomingBlocks, okEnd)
	}

	llvmPositionBuilderAtEnd(s.builder, fallbackBB)
	recovery := astRecoveryClauseForExpr(expr.Recovery, expr.Fallback, expr.Position)
	fallbackValue, reachable, err := s.emitRecoveryClause(recovery, resultType, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if reachable && !s.currentBlockTerminated() {
		fallbackEnd := llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, fallbackValue)
		}
		incomingBlocks = append(incomingBlocks, fallbackEnd)
	}

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingBlocks) == 0 {
		llvmBuildUnreachable(s.builder)
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
	phi := llvmBuildPhi(s.builder, phiType, cStringFree("unwrapphi"))
	llvmAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), llvmIRUnsigned(len(incomingValues)))
	return phi, resultType, nil
}
