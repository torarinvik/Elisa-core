//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitErrorTagExpr(expr *ast.FieldExpr, errorType *semantic.ErrorSetType) (C.LLVMValueRef, semantic.Type, error) {
	if errorType == nil {
		return nil, nil, fmt.Errorf("missing error set for tag expression")
	}
	if _, ok := expr.Object.(*ast.Ident); !ok {
		return nil, nil, fmt.Errorf("missing error set qualifier for tag expression")
	}
	code, ok := errorType.TagCodeFor(errorType.Name, expr.Field)
	if !ok {
		return nil, nil, fmt.Errorf("unknown error tag %s.%s", errorType.Name, expr.Field)
	}
	if errorType.HasPayloads() {
		value, err := s.buildErrorSetValue(errorType, semantic.QualifyErrorTag(errorType.Name, expr.Field), nil)
		if err != nil {
			return nil, nil, err
		}
		return value, errorType, nil
	}
	value, err := s.errorCodeConstant(code)
	if err != nil {
		return nil, nil, err
	}
	return value, errorType, nil
}

func (s *functionState) errorConstructorInfo(expr *ast.CallExpr) (*semantic.ErrorSetType, string, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, "", false
	}
	errorType, qualifiedTag, ok := s.errorTagInfo(fieldExpr)
	return errorType, qualifiedTag, ok
}

func (s *functionState) emitErrorConstructorValue(expr *ast.CallExpr, errorType *semantic.ErrorSetType, qualifiedTag string) (C.LLVMValueRef, semantic.Type, error) {
	payloadTypes := errorType.PayloadForTag(qualifiedTag)
	args := make([]C.LLVMValueRef, 0, len(expr.Args))
	for i, arg := range expr.Args {
		var expected semantic.Type
		if i < len(payloadTypes) {
			expected = payloadTypes[i]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, value)
	}
	value, err := s.buildErrorSetValue(errorType, qualifiedTag, args)
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
	if callExpr, ok := expr.Error.(*ast.CallExpr); ok {
		if _, qualifiedTag, ok := s.errorConstructorInfo(callExpr); ok {
			mappedTag, matched := semantic.MatchErrorTag(currentUnion.Errors, qualifiedTag)
			if !matched {
				return nil, nil, fmt.Errorf("payload error raise of %s has no matching tag in destination error set %s", qualifiedTag, currentUnion.Errors)
			}
			// emitErrorConstructorValue builds the value in the DESTINATION set's layout
			// (buildErrorSetValue places the payload at the destination's field offset for
			// this tag), so a payloaded tag can be raised into a wider mixed/combined set
			// such as error[FooError{Bad1}, BarError{Bad3, Bad4}] — not only its own set.
			errorValue, errorType, err = s.emitErrorConstructorValue(callExpr, currentUnion.Errors, mappedTag)
		} else {
			errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
		}
	} else if fieldExpr, ok := expr.Error.(*ast.FieldExpr); ok {
		if _, qualifiedTag, ok := s.errorTagInfo(fieldExpr); ok {
			mappedTag, matched := semantic.MatchErrorTag(currentUnion.Errors, qualifiedTag)
			if matched {
				code, ok := currentUnion.Errors.TagCode(mappedTag)
				if !ok {
					return nil, nil, fmt.Errorf("missing destination error tag %s", mappedTag)
				}
				if currentUnion.Errors.HasPayloads() {
					errorValue, err = s.buildErrorSetValue(currentUnion.Errors, mappedTag, nil)
				} else {
					errorValue, err = s.errorCodeConstant(code)
				}
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

func astRecoveryClauseForExpr(recovery *ast.RecoveryClause, fallback ast.Expr, pos lexer.Pos) *ast.RecoveryClause {
	if recovery != nil {
		return recovery
	}
	if fallback == nil {
		return nil
	}
	if raise, ok := fallback.(*ast.RaiseExpr); ok {
		return &ast.RecoveryClause{Position: fallback.Pos(), Kind: ast.RecoveryRaise, Value: raise.Error}
	}
	return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryValue, Value: fallback}
}

func (s *functionState) emitRecoveryClause(recovery *ast.RecoveryClause, resultType semantic.Type, errorValue C.LLVMValueRef, errorType semantic.Type) (C.LLVMValueRef, bool, error) {
	if recovery == nil {
		return nil, false, fmt.Errorf("missing recovery clause")
	}
	// The recovery clause is emitted in a fallback basic block that does not dominate the merge
	// continuation. Snapshot the packed-store bindings (same discipline as emitBlockInCurrentScope):
	// an implicit region-backed store created while emitting the fallback value must not leak its
	// SSA value to successors — that fails the LLVM dominance verifier.
	savedPackedStores := s.packedStores
	s.packedStores = s.clonePackedStores()
	defer func() {
		s.packedStores = savedPackedStores
	}()
	switch recovery.Kind {
	case ast.RecoveryValue:
		value, _, err := s.emitExpr(recovery.Value, resultType)
		return value, !s.currentBlockTerminated(), err
	case ast.RecoveryRaise:
		_, _, err := s.emitRaiseExpr(&ast.RaiseExpr{Position: recovery.Position, Error: recovery.Value})
		return nil, false, err
	case ast.RecoveryReturn:
		err := s.emitStmt(&ast.ReturnStmt{Position: recovery.Position, Value: recovery.Value})
		return nil, false, err
	case ast.RecoveryVoid:
		return nil, !s.currentBlockTerminated(), nil
	case ast.RecoveryBlock:
		s.pushScope()
		if recovery.Binding != "" {
			if errorValue == nil || errorType == nil {
				s.popScope()
				return nil, false, fmt.Errorf("else error binding requires an error-union operand")
			}
			errorPtr, err := s.emitStackTempValue(errorValue, errorType, "else.error")
			if err != nil {
				s.popScope()
				return nil, false, err
			}
			s.defineBinding(recovery.Binding, valueBinding{ptr: errorPtr, typ: errorType, mutable: false})
		}
		for _, stmt := range recovery.Body {
			if err := s.emitStmt(stmt); err != nil {
				s.popScope()
				return nil, false, err
			}
			if s.currentBlockTerminated() {
				break
			}
		}
		s.popScope()
		if !s.currentBlockTerminated() && !isVoidType(resultType) {
			return nil, false, fmt.Errorf("else recovery block must return or raise for non-void result")
		}
		return nil, !s.currentBlockTerminated(), nil
	default:
		return nil, false, fmt.Errorf("unsupported recovery clause")
	}
}

func (s *functionState) emitTryExpr(expr *ast.TryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	recovery := astRecoveryClauseForExpr(expr.Recovery, expr.Fallback, expr.Position)
	// Decide fallibility from the EMITTED operand type rather than s.exprType:
	// once a generic combinator is instantiated at R := ∅ anywhere in the
	// program, the analyzer's shared ExprTypes record for the body's `f()`
	// collapses to the bare value type, which would otherwise mislead every
	// other (fallible) instantiation of the same body.
	fallibleValue, operandType, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	if unionType, ok := operandType.(*semantic.ErrorUnionType); ok {
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
		successCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), errorCode, zeroCode, cStringFree("try.ok"))

		if recovery == nil {
			okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.ok"))
			errBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.err"))
			C.LLVMBuildCondBr(s.builder, successCond, okBB, errBB)

			C.LLVMPositionBuilderAtEnd(s.builder, errBB)
			if _, ok := s.fnType.Return.(*semantic.ErrorUnionType); !ok {
				return nil, nil, fmt.Errorf("try propagation requires an error-union function return")
			}
			if err := s.emitFunctionReturn(errorValue, unionType.Errors); err != nil {
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
		mergeReachable := false

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
			mergeReachable = true
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, okValue)
				incomingBlocks = append(incomingBlocks, okEnd)
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
		fallbackValue, reachable, err := s.emitRecoveryClause(recovery, resultType, errorValue, unionType.Errors)
		if err != nil {
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			fallbackEnd := C.LLVMGetInsertBlock(s.builder)
			C.LLVMBuildBr(s.builder, mergeBB)
			mergeReachable = true
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, fallbackValue)
				incomingBlocks = append(incomingBlocks, fallbackEnd)
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		if !mergeReachable {
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
	optionalType, ok := operandType.(*semantic.OptionalType)
	if !ok {
		// Infallible operand: an `[errorset R]` callback monomorphized at R := ∅
		// collapses `T error[∅]` to a plain `T`, so `try v` is the identity — it
		// can never take the error path and any `else` recovery is dead.
		return fallibleValue, operandType, nil
	}
	if recovery == nil {
		return nil, nil, fmt.Errorf("try without else is only supported for error unions")
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
	mergeReachable := false

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
		mergeReachable = true
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, okValue)
			incomingBlocks = append(incomingBlocks, okEnd)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, reachable, err := s.emitRecoveryClause(recovery, resultType, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if reachable && !s.currentBlockTerminated() {
		fallbackEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		mergeReachable = true
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, fallbackValue)
			incomingBlocks = append(incomingBlocks, fallbackEnd)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if !mergeReachable {
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
