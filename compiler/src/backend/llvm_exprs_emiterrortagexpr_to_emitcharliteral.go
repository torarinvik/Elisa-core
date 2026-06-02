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
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
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
	if errorType.HasPayloads() {
		value, err := s.buildErrorSetValue(errorType, semantic.QualifyErrorTag(ident.Name, expr.Field), nil)
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
	if unionType, ok := s.exprType(expr.Value).(*semantic.ErrorUnionType); ok {
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
		fallbackValue, reachable, err := s.emitRecoveryClause(recovery, resultType, errorValue, unionType.Errors)
		if err != nil {
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
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
	if recovery == nil {
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
	fallbackValue, reachable, err := s.emitRecoveryClause(recovery, resultType, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if reachable && !s.currentBlockTerminated() {
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
// emitGetExpr lowers the `get` prefix (the optional analog of `try`). Three
// shapes:
//   - `get arr[i] else <value>`: the value fallback was parsed onto the inner
//     IndexExpr, which is already a complete checked access — emit it directly.
//   - `get arr[i]` / `get arr[i] else return|raise`: a bounds-checked access
//     whose absence path runs the recovery (or, with no else, returns None).
//   - `get opt` / `get opt else ...`: unwrap an optional / nullable reference,
//     with the recovery (or None-propagation) on the absent path.
func (s *functionState) emitGetExpr(expr *ast.GetExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	if idx, ok := expr.Value.(*ast.IndexExpr); ok && idx.Fallback != nil {
		return s.emitExpr(expr.Value, resultType)
	}
	// Resolve the recovery: an explicit `else`, or the propagation default which
	// early-returns the enclosing function's None.
	recovery := astRecoveryClauseForExpr(expr.Recovery, expr.Fallback, expr.Position)
	if recovery == nil {
		recovery = &ast.RecoveryClause{Position: expr.Position, Kind: ast.RecoveryReturn, Value: &ast.NullLit{Position: expr.Position}}
	}
	if idx, ok := expr.Value.(*ast.IndexExpr); ok {
		return s.emitGetCheckedIndexExpr(idx, recovery, resultType)
	}
	if slice, ok := expr.Value.(*ast.SliceExpr); ok {
		return s.emitGetCheckedSliceExpr(slice, recovery, resultType)
	}
	return s.emitGetUnwrapExpr(expr.Value, recovery, resultType)
}

// emitGetCheckedSliceExpr emits a bounds-checked slice: when [start, end) is
// within the source (0 <= start <= end <= len) it produces the bounded view;
// otherwise it runs the recovery clause. The bounds are usize (unsigned), so the
// non-negativity of start is free.
func (s *functionState) emitGetCheckedSliceExpr(slice *ast.SliceExpr, recovery *ast.RecoveryClause, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	startValue, _, err := s.emitExpr(slice.Start, usizeType)
	if err != nil {
		return nil, nil, err
	}
	endValue, _, err := s.emitExpr(slice.End, usizeType)
	if err != nil {
		return nil, nil, err
	}
	lenValue, err := s.emitSliceSourceLength(slice)
	if err != nil {
		return nil, nil, err
	}
	startLEEnd := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULE), startValue, endValue, cStringFree("get.slice.start_le_end"))
	endLELen := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULE), endValue, lenValue, cStringFree("get.slice.end_le_len"))
	okCond := C.LLVMBuildAnd(s.builder, startLEEnd, endLELen, cStringFree("get.slice.in_range"))
	return s.emitGetBranch(okCond, func() (C.LLVMValueRef, error) {
		view, _, err := s.emitSliceExpr(slice)
		return view, err
	}, recovery, resultType)
}

// emitSliceSourceLength computes the element count of a slice's source for the
// bounds check: a constant for fixed-size arrays, the runtime count field for
// dynamic containers (darray / view / darray-view).
func (s *functionState) emitSliceSourceLength(slice *ast.SliceExpr) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVM, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	objType := s.exprType(slice.Object)
	base := objType
	if ref, ok := base.(*semantic.RefType); ok && ref != nil {
		base = ref.Elem
	}
	if arr, ok := base.(*semantic.ArrayType); ok && arr != nil && arr.HasConstSize {
		return C.LLVMConstInt(usizeLLVM, C.ulonglong(arr.ConstSize), 0), nil
	}
	switch base.(type) {
	case *semantic.DArrayType, *semantic.ViewType, *semantic.DArrayViewType:
		containerPtr, _, err := s.emitAddressOrTemp(slice.Object)
		if err != nil {
			return nil, err
		}
		return s.emitContainerCountValue(containerPtr, base, "get.slice.count")
	default:
		return nil, fmt.Errorf("bounds-checked slice is not supported for %s", objType.String())
	}
}

// emitGetCheckedIndexExpr emits a bounds-checked container access: in-range
// produces the element, out-of-range runs the recovery clause.
func (s *functionState) emitGetCheckedIndexExpr(idx *ast.IndexExpr, recovery *ast.RecoveryClause, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	indexValue, _, err := s.emitExpr(idx.Index, usizeType)
	if err != nil {
		return nil, nil, err
	}
	countValue, loadValue, err := s.prepareSafeIndexFallback(idx, indexValue)
	if err != nil {
		return nil, nil, err
	}
	okCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree("get.index.in.range"))
	return s.emitGetBranch(okCond, func() (C.LLVMValueRef, error) {
		value, actualType, err := loadValue()
		if err != nil {
			return nil, err
		}
		if !semantic.SameType(actualType, resultType) && !isVoidType(resultType) {
			return s.coerceValue(value, actualType, resultType)
		}
		return value, nil
	}, recovery, resultType)
}

// emitGetUnwrapExpr unwraps an optional / nullable reference, running the
// recovery clause on the absent path.
func (s *functionState) emitGetUnwrapExpr(valueExpr ast.Expr, recovery *ast.RecoveryClause, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	valueType := s.exprType(valueExpr)
	value, _, err := s.emitExpr(valueExpr, valueType)
	if err != nil {
		return nil, nil, err
	}
	var okCond, okValue C.LLVMValueRef
	switch t := valueType.(type) {
	case *semantic.RefType:
		llvmRefType, err := s.g.lowerType(valueType)
		if err != nil {
			return nil, nil, err
		}
		nullValue := C.LLVMConstNull(llvmRefType)
		okCond = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), value, nullValue, cStringFree("get.nonnull"))
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
		return nil, nil, fmt.Errorf("get requires an optional value or nullable ref")
	}
	return s.emitGetBranch(okCond, func() (C.LLVMValueRef, error) { return okValue, nil }, recovery, resultType)
}

// emitGetBranch is the shared branch/merge skeleton for `get`: when okCond holds
// it takes okValue (computed lazily so the in-range index load only happens on
// the taken path); otherwise it runs the recovery clause, which may fall through
// (value/void) or terminate (return/raise).
func (s *functionState) emitGetBranch(okCond C.LLVMValueRef, okValue func() (C.LLVMValueRef, error), recovery *ast.RecoveryClause, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("get.ok"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("get.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("get.merge"))
	C.LLVMBuildCondBr(s.builder, okCond, okBB, fallbackBB)

	incomingValues := make([]C.LLVMValueRef, 0, 2)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	okVal, err := okValue()
	if err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		okEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, okVal)
		}
		incomingBlocks = append(incomingBlocks, okEnd)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
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
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("getphi"))
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
