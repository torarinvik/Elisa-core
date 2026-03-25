//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

static int llcontextLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}
*/
import "C"

import (
	"fmt"
	"strconv"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

func llvmValueIsZeroConstant(value C.LLVMValueRef) bool {
	return value != nil && C.llcontextLLVMIsZeroValue(value) != 0
}

func (s *functionState) addCallSiteEnumAttribute(call C.LLVMValueRef, index C.uint, name string) {
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	kind := C.LLVMGetEnumAttributeKindForName(nameC, C.size_t(len(name)))
	if kind == 0 {
		return
	}
	attr := C.LLVMCreateEnumAttribute(s.g.context, kind, 0)
	C.LLVMAddCallSiteAttribute(call, index, attr)
}

func callIdentName(expr *ast.CallExpr) string {
	if expr == nil {
		return ""
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

func callSpecializedIdent(expr ast.Expr) (*ast.Ident, *ast.SpecializeExpr, bool) {
	if expr == nil {
		return nil, nil, false
	}
	specialize, ok := expr.(*ast.SpecializeExpr)
	if !ok || specialize == nil {
		return nil, nil, false
	}
	ident, ok := specialize.Operand.(*ast.Ident)
	if !ok || ident == nil {
		return nil, nil, false
	}
	return ident, specialize, true
}

func callSpecializedIdentName(expr *ast.CallExpr) string {
	if expr == nil {
		return ""
	}
	if ident, _, ok := callSpecializedIdent(expr.Func); ok {
		return ident.Name
	}
	return ""
}

func denseKeySourceEnumType(t semantic.Type) (*semantic.EnumType, bool) {
	if t == nil {
		return nil, false
	}
	t = semantic.StripAggregateStateType(t)
	if enumType, ok := t.(*semantic.EnumType); ok && enumType != nil && enumType.Packed {
		return enumType, true
	}
	if viewType, ok := t.(*semantic.PackedVariantViewType); ok && viewType != nil && viewType.Enum != nil {
		return viewType.Enum, true
	}
	return nil, false
}

func (s *functionState) emitExpr(expr ast.Expr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("cannot emit nil expression")
	}

	actualType := s.exprType(expr)
	if expected != nil && isZeroedExpr(expr) {
		value, err := s.zeroValue(expected)
		return value, expected, err
	}

	var (
		value C.LLVMValueRef
		err   error
	)

	switch n := expr.(type) {
	case *ast.Ident:
		value, actualType, err = s.emitIdent(n)
	case *ast.IntLit:
		value, actualType, err = s.emitIntLiteral(n)
	case *ast.FloatLit:
		value, actualType, err = s.emitFloatLiteral(n)
	case *ast.StringLit:
		value, actualType, err = s.emitStringLiteral(n)
	case *ast.BoolLit:
		value, actualType, err = s.emitBoolLiteral(n)
	case *ast.NullLit:
		value, actualType, err = s.emitNullLiteral()
	case *ast.ZeroedLit:
		return nil, nil, fmt.Errorf("zeroed requires an expected destination type")
	case *ast.ListLitExpr:
		value, actualType, err = s.emitListLitExpr(n, expected)
	case *ast.BinaryExpr:
		value, actualType, err = s.emitBinaryExpr(n)
	case *ast.UnaryExpr:
		value, actualType, err = s.emitUnaryExpr(n)
	case *ast.CallExpr:
		value, actualType, err = s.emitCallExpr(n)
	case *ast.FieldExpr:
		if errorType, _, ok := s.errorTagInfo(n); ok {
			value, actualType, err = s.emitErrorTagExpr(n, errorType)
		} else if constEnumType, member, ok := s.constEnumMemberInfo(n); ok {
			value, actualType, err = s.emitConstEnumMemberExpr(constEnumType, member)
		} else if enumType, variant, ok := s.enumConstructorInfoFromField(n); ok && variant != nil && len(variant.Payload) == 0 {
			value, actualType, err = s.emitEnumConstructorValue(enumType, variant, nil, nil)
		} else {
			value, actualType, err = s.emitFieldExpr(n)
		}
	case *ast.RaiseExpr:
		value, actualType, err = s.emitRaiseExpr(n)
	case *ast.TryExpr:
		value, actualType, err = s.emitTryExpr(n)
	case *ast.UnwrapElseExpr:
		value, actualType, err = s.emitUnwrapElseExpr(n)
	case *ast.AllocExpr:
		value, actualType, err = s.emitAllocExpr(n)
	case *ast.MatchExpr:
		value, actualType, err = s.emitMatchExpr(n)
	case *ast.IndexExpr:
		value, actualType, err = s.emitIndexExpr(n)
	case *ast.SliceExpr:
		value, actualType, err = s.emitSliceExpr(n)
	case *ast.CastExpr:
		value, actualType, err = s.emitCastExpr(n)
	case *ast.SizeofExpr:
		value, actualType, err = s.emitSizeofExpr(n)
	case *ast.TernaryExpr:
		value, actualType, err = s.emitTernaryExpr(n)
	case *ast.AddrOfExpr:
		value, actualType, err = s.emitAddrOfExpr(n)
	case *ast.MoveExpr:
		value, actualType, err = s.emitMoveExpr(n, expected)
	case *ast.SpecializeExpr:
		value, actualType, err = s.emitSpecializeExpr(n)
	case *ast.StructLitExpr:
		value, actualType, err = s.emitStructLitExpr(n)
	case *ast.ParenExpr:
		value, actualType, err = s.emitExpr(n.Inner, expected)
	case *ast.CanExpr:
		value, actualType, err = s.emitExpr(n.Expr, expected)
	default:
		return nil, nil, fmt.Errorf("unsupported expression %T", expr)
	}
	if err != nil {
		return nil, nil, err
	}
	if expected != nil {
		coerced, err := s.coerceValue(value, actualType, expected)
		if err != nil {
			return nil, nil, err
		}
		return coerced, expected, nil
	}
	return value, actualType, nil
}

func (s *functionState) emitMoveExpr(expr *ast.MoveExpr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if binding, ok := s.lookupScopedMoveBinding(expr.Operand); ok {
		value, err := s.loadValue(binding.ptr, binding.typ, binding.name)
		if err != nil {
			return nil, nil, err
		}
		zero, err := s.zeroValue(binding.typ)
		if err != nil {
			return nil, nil, err
		}
		C.LLVMBuildStore(s.builder, zero, binding.ptr)
		return value, binding.typ, nil
	}
	return s.emitExpr(expr.Operand, expected)
}

func (s *functionState) lookupScopedMoveBinding(expr ast.Expr) (scopedCleanupBinding, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.lookupScopedMoveBinding(n.Inner)
	case *ast.Ident:
		binding, ok := s.lookupBinding(n.Name)
		if !ok {
			return scopedCleanupBinding{}, false
		}
		for i := len(s.scopedCleanups) - 1; i >= 0; i-- {
			if s.scopedCleanups[i].ptr == binding.ptr {
				return s.scopedCleanups[i], true
			}
		}
	}
	return scopedCleanupBinding{}, false
}

func (s *functionState) buildCall(llvmFnType C.LLVMTypeRef, callee C.LLVMValueRef, args []C.LLVMValueRef, name string) C.LLVMValueRef {
	argCount := len(args)
	argPtr := llvmValueSlicePtr(args)
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	return C.LLVMBuildCall2(s.builder, llvmFnType, callee, argPtr, C.unsigned(argCount), nameC)
}

func (s *functionState) emitIdent(expr *ast.Ident) (C.LLVMValueRef, semantic.Type, error) {
	if actualType := s.exprType(expr); semantic.IsNullType(actualType) {
		return s.emitNullLiteral()
	} else if viewType, ok := actualType.(*semantic.PackedVariantViewType); ok {
		if binding, ok := s.lookupPackedVariantView(expr.Name); ok {
			return s.materializePackedVariantViewValue(binding)
		}
		if ptr, valueType, err := s.emitIdentValueAddress(expr); err == nil {
			if enumType, ok := valueType.(*semantic.EnumType); ok && enumType == viewType.Enum && enumType.Packed {
				handle, err := s.loadValue(ptr, valueType, expr.Name)
				if err != nil {
					return nil, nil, err
				}
				store, ok := s.lookupPackedStore(enumType)
				if !ok {
					return nil, nil, fmt.Errorf("packedview %s requires store context for %q", viewType.String(), expr.Name)
				}
				value, err := s.buildPackedVariantViewValue(viewType, handle, &store)
				if err != nil {
					return nil, nil, err
				}
				s.bindPackedVariantView(expr.Name, viewType, nil, handle, &store)
				return value, viewType, nil
			}
		}
	}
	if ptr, valueType, err := s.emitIdentValueAddress(expr); err == nil {
		value, loadErr := s.loadValue(ptr, valueType, expr.Name)
		return value, valueType, loadErr
	}
	if binding, ok := s.lookupBinding(expr.Name); ok {
		value, err := s.loadValue(binding.ptr, binding.typ, expr.Name)
		return value, binding.typ, err
	}
	if sym, ok := s.g.result.GlobalScope.Lookup(expr.Name); ok {
		switch sym.Kind {
		case semantic.SymbolFunc, semantic.SymbolExternFunc:
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok {
				return nil, nil, fmt.Errorf("global function %s is missing function type", expr.Name)
			}
			value, err := s.g.ensureFunctionDeclared(expr.Name, fnType)
			return value, fnType, err
		case semantic.SymbolGlobal, semantic.SymbolExternVar:
			global, err := s.g.ensureGlobalDeclared(expr.Name, sym.Type, sym.Kind == semantic.SymbolExternVar)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(global, sym.Type, expr.Name)
			return value, sym.Type, err
		case semantic.SymbolConst:
			if value, ok := s.g.constValue(expr.Name); ok {
				llvmValue, llvmType, err := s.emitConstValueWithType(value, sym.Type)
				return llvmValue, llvmType, err
			}
		}
	}
	if value, ok := s.g.constValue(expr.Name); ok {
		llvmValue, llvmType, err := s.emitConstValue(value)
		return llvmValue, llvmType, err
	}
	return nil, nil, fmt.Errorf("unknown identifier %q during LLVM lowering", expr.Name)
}

func (s *functionState) errorTagInfo(expr *ast.FieldExpr) (*semantic.ErrorSetType, string, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, "", false
	}
	base, ok := s.g.result.NamedTypes[ident.Name]
	if !ok {
		return nil, "", false
	}
	errSet, ok := base.(*semantic.ErrorSetType)
	if !ok || !errSet.HasQualifiedTag(ident.Name, expr.Field) {
		return nil, "", false
	}
	return errSet, semantic.QualifyErrorTag(ident.Name, expr.Field), true
}

func (s *functionState) constEnumMemberInfo(expr *ast.FieldExpr) (*semantic.ConstEnumType, *semantic.ConstEnumMember, bool) {
	constEnumType, ok := s.constEnumTypeForExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	member, ok := constEnumType.Member(expr.Field)
	if !ok {
		return constEnumType, nil, false
	}
	return constEnumType, member, true
}

func (s *functionState) constEnumTypeForExpr(expr ast.Expr) (*semantic.ConstEnumType, bool) {
	if expr == nil {
		return nil, false
	}
	if constEnumType, ok := s.exprType(expr).(*semantic.ConstEnumType); ok {
		return constEnumType, true
	}
	switch n := expr.(type) {
	case *ast.Ident:
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, false
		}
		constEnumType, ok := base.(*semantic.ConstEnumType)
		return constEnumType, ok
	case *ast.FieldExpr:
		ident, ok := n.Object.(*ast.Ident)
		if !ok || n.Field != "Tag" {
			return nil, false
		}
		base, ok := s.g.result.NamedTypes[ident.Name]
		if !ok {
			return nil, false
		}
		enumType, ok := base.(*semantic.EnumType)
		if !ok || !enumType.Packed || enumType.TagType == nil {
			return nil, false
		}
		return enumType.TagType, true
	case *ast.ParenExpr:
		return s.constEnumTypeForExpr(n.Inner)
	default:
		return nil, false
	}
}

func (s *functionState) emitConstEnumMemberExpr(constEnumType *semantic.ConstEnumType, member *semantic.ConstEnumMember) (C.LLVMValueRef, semantic.Type, error) {
	if constEnumType == nil || member == nil {
		return nil, nil, fmt.Errorf("missing const enum member metadata")
	}
	llvmType, err := s.g.lowerType(constEnumType)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(member.Value), boolToLLVMBool(member.Value < 0)), constEnumType, nil
}

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

func (s *functionState) emitStringLiteral(expr *ast.StringLit) (C.LLVMValueRef, semantic.Type, error) {
	name := cString("str")
	defer C.free(unsafe.Pointer(name))
	text := cString(expr.Value)
	defer C.free(unsafe.Pointer(text))
	value := C.LLVMBuildGlobalStringPtr(s.builder, text, name)
	return value, s.exprType(expr), nil
}

func (s *functionState) emitListLitExpr(expr *ast.ListLitExpr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	arrayType, err := s.listLiteralTargetType(expr, expected)
	if err != nil {
		return nil, nil, err
	}
	if arrayType.HasConstSize && arrayType.ConstSize != int64(len(expr.Elems)) {
		return nil, nil, fmt.Errorf("array literal resolved to %s but has %d elements", arrayType.String(), len(expr.Elems))
	}
	llvmType, err := s.g.lowerType(arrayType)
	if err != nil {
		return nil, nil, err
	}
	current := C.LLVMGetUndef(llvmType)
	for i, elem := range expr.Elems {
		elemValue, _, err := s.emitExpr(elem, arrayType.Elem)
		if err != nil {
			return nil, nil, err
		}
		current = C.LLVMBuildInsertValue(s.builder, current, elemValue, C.unsigned(i), cStringFree("arraylit.elem"))
	}
	return current, arrayType, nil
}

func (s *functionState) listLiteralTargetType(expr *ast.ListLitExpr, expected semantic.Type) (*semantic.ArrayType, error) {
	if expectedArray, ok := expected.(*semantic.ArrayType); ok {
		return expectedArray, nil
	}
	actualArray, ok := s.exprType(expr).(*semantic.ArrayType)
	if !ok {
		return nil, fmt.Errorf("array literal did not resolve to a fixed array type")
	}
	return actualArray, nil
}

func (s *functionState) emitStackTempZeroed(t semantic.Type, name string) (C.LLVMValueRef, error) {
	zero, err := s.zeroValue(t)
	if err != nil {
		return nil, err
	}
	return s.emitStackTempValue(zero, t, name)
}

func (s *functionState) emitStackTempValue(value C.LLVMValueRef, t semantic.Type, name string) (C.LLVMValueRef, error) {
	alloca, err := s.createEntryAlloca(name, t)
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, value, alloca)
	return alloca, nil
}

func (s *functionState) emitBoolLiteral(expr *ast.BoolLit) (C.LLVMValueRef, semantic.Type, error) {
	llvmType, err := s.g.lowerBuiltin("bool")
	if err != nil {
		return nil, nil, err
	}
	var raw C.ulonglong
	if expr.Value {
		raw = 1
	}
	return C.LLVMConstInt(llvmType, raw, 0), s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitNullLiteral() (C.LLVMValueRef, semantic.Type, error) {
	ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	return C.LLVMConstNull(ptrType), &semantic.NullType{}, nil
}

func (s *functionState) emitBinaryExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Op == lexer.TOKEN_AND || expr.Op == lexer.TOKEN_OR {
		return s.emitLogicalExpr(expr)
	}
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(s.exprType(expr.Left), s.exprType(expr.Right)); ok && (expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ) {
		return s.emitRuntimeStringCompareExpr(expr, helperName, firstType, secondType, swap)
	}
	leftType := s.exprType(expr.Left)
	rightType := s.exprType(expr.Right)
	resultType := s.exprType(expr)
	if value, actualType, handled, err := s.emitOptionalCompareExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerCompareExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerArithmeticExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	operandType := s.binaryOperandType(expr.Op, leftType, rightType)

	left, _, err := s.emitExpr(expr.Left, operandType)
	if err != nil {
		return nil, nil, err
	}
	right, _, err := s.emitExpr(expr.Right, operandType)
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := operandType.(*semantic.EnumType); ok && (expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ) {
		return s.emitEnumCompareExpr(expr.Op, enumType, left, right, resultType)
	}

	switch expr.Op {
	case lexer.TOKEN_PLUS:
		if isFloatType(operandType) {
			return C.LLVMBuildFAdd(s.builder, left, right, cStringFree("addtmp")), resultType, nil
		}
		return C.LLVMBuildAdd(s.builder, left, right, cStringFree("addtmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		if isFloatType(operandType) {
			return C.LLVMBuildFSub(s.builder, left, right, cStringFree("subtmp")), resultType, nil
		}
		return C.LLVMBuildSub(s.builder, left, right, cStringFree("subtmp")), resultType, nil
	case lexer.TOKEN_STAR:
		if isFloatType(operandType) {
			return C.LLVMBuildFMul(s.builder, left, right, cStringFree("multmp")), resultType, nil
		}
		return C.LLVMBuildMul(s.builder, left, right, cStringFree("multmp")), resultType, nil
	case lexer.TOKEN_SLASH:
		if isFloatType(operandType) {
			return C.LLVMBuildFDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
		}
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildSDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
		}
		return C.LLVMBuildUDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
	case lexer.TOKEN_PERCENT:
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildSRem(s.builder, left, right, cStringFree("remtmp")), resultType, nil
		}
		return C.LLVMBuildURem(s.builder, left, right, cStringFree("remtmp")), resultType, nil
	case lexer.TOKEN_PIPE:
		return C.LLVMBuildOr(s.builder, left, right, cStringFree("ortmp")), resultType, nil
	case lexer.TOKEN_CARET:
		return C.LLVMBuildXor(s.builder, left, right, cStringFree("xortmp")), resultType, nil
	case lexer.TOKEN_AMPERSAND:
		return C.LLVMBuildAnd(s.builder, left, right, cStringFree("andtmp")), resultType, nil
	case lexer.TOKEN_LSHIFT:
		return C.LLVMBuildShl(s.builder, left, right, cStringFree("shltmp")), resultType, nil
	case lexer.TOKEN_RSHIFT:
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildAShr(s.builder, left, right, cStringFree("shrtmp")), resultType, nil
		}
		return C.LLVMBuildLShr(s.builder, left, right, cStringFree("shrtmp")), resultType, nil
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		if isFloatType(operandType) {
			pred, err := llvmFloatPredicate(expr.Op)
			if err != nil {
				return nil, nil, err
			}
			return C.LLVMBuildFCmp(s.builder, pred, left, right, cStringFree("cmptmp")), resultType, nil
		}
		pred, err := llvmIntPredicate(expr.Op, operandType)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("cmptmp")), resultType, nil
	default:
		return nil, nil, fmt.Errorf("unsupported binary operator %s", lexer.TokenName(expr.Op))
	}
}

func (s *functionState) emitEnumCompareExpr(op lexer.TokenKind, enumType *semantic.EnumType, left C.LLVMValueRef, right C.LLVMValueRef, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil {
		return nil, nil, fmt.Errorf("missing enum type for comparison")
	}
	if enumType.Packed {
		pred := C.LLVMIntPredicate(C.LLVMIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = C.LLVMIntPredicate(C.LLVMIntNE)
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.packed")), resultType, nil
	}
	if enumIsTagOnly(enumType) {
		pred := C.LLVMIntPredicate(C.LLVMIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = C.LLVMIntPredicate(C.LLVMIntNE)
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.tagonly")), resultType, nil
	}
	leftTag := C.LLVMBuildExtractValue(s.builder, left, 0, cStringFree("enumcmp.left.tag"))
	rightTag := C.LLVMBuildExtractValue(s.builder, right, 0, cStringFree("enumcmp.right.tag"))
	equal := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftTag, rightTag, cStringFree("enumcmp.tag.eq"))

	payloadSlots, err := s.enumPayloadWordCount(enumType)
	if err != nil {
		return nil, nil, err
	}
	if payloadSlots > 0 {
		leftPayload := C.LLVMBuildExtractValue(s.builder, left, 1, cStringFree("enumcmp.left.payload"))
		rightPayload := C.LLVMBuildExtractValue(s.builder, right, 1, cStringFree("enumcmp.right.payload"))
		for i := uint64(0); i < payloadSlots; i++ {
			nameSuffix := fmt.Sprintf(".%d", i)
			leftWord := C.LLVMBuildExtractValue(s.builder, leftPayload, C.unsigned(i), cStringFree("enumcmp.left.word"+nameSuffix))
			rightWord := C.LLVMBuildExtractValue(s.builder, rightPayload, C.unsigned(i), cStringFree("enumcmp.right.word"+nameSuffix))
			wordEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftWord, rightWord, cStringFree("enumcmp.word.eq"+nameSuffix))
			equal = C.LLVMBuildAnd(s.builder, equal, wordEqual, cStringFree("enumcmp.and"+nameSuffix))
		}
	}
	if op == lexer.TOKEN_BANGEQ {
		return C.LLVMBuildNot(s.builder, equal, cStringFree("enumcmp.ne")), resultType, nil
	}
	return equal, resultType, nil
}

func (s *functionState) encodePackedEnumHandle(rowPtr C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	return s.encodePackedEnumHandleWithStore(rowPtr, enumType, nil)
}

func (s *functionState) encodePackedEnumHandleWithStore(rowPtr C.LLVMValueRef, enumType *semantic.EnumType, storeValue C.LLVMValueRef) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	if s.g.packedModeForEnum(enumType) == packedEnumABIRowHandle {
		return rowPtr, nil
	}
	storeType := enumType.StoreType
	if storeType == nil {
		return nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
	}
	if storeValue == nil {
		return nil, fmt.Errorf("packed enum %s word-handle encode requires store context", enumType.Name)
	}
	return (&packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}).encodeHandle(rowPtr, enumType, "packed.encode.store")
}

func (s *functionState) decodePackedEnumHandle(handleValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	return s.decodePackedEnumHandleWithStore(handleValue, enumType, nil)
}

func (s *functionState) decodePackedEnumHandleWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	if s.g.packedModeForEnum(enumType) == packedEnumABIRowHandle {
		return handleValue, nil
	}
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s word-handle decode requires store context", enumType.Name)
	}
	return ops.decodeHandle(handleValue, enumType, "packed.decode.store")
}

func (s *functionState) enumPayloadWordCount(enumType *semantic.EnumType) (uint64, error) {
	if enumType == nil {
		return 0, nil
	}
	maxSlots := uint64(0)
	for _, variant := range enumType.Variants {
		slots, err := s.g.enumVariantPayloadSlots(variant)
		if err != nil {
			return 0, err
		}
		if slots > maxSlots {
			maxSlots = slots
		}
	}
	return maxSlots, nil
}

func (s *functionState) emitPointerCompareExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr.Op != lexer.TOKEN_EQEQ && expr.Op != lexer.TOKEN_BANGEQ {
		return nil, nil, false, nil
	}
	leftPointerish := isPointerLikeType(leftType) || semantic.IsNullType(leftType)
	rightPointerish := isPointerLikeType(rightType) || semantic.IsNullType(rightType)
	if !leftPointerish || !rightPointerish {
		return nil, nil, false, nil
	}
	operandType := s.binaryOperandType(expr.Op, leftType, rightType)
	left, _, err := s.emitExpr(expr.Left, operandType)
	if err != nil {
		return nil, nil, true, err
	}
	right, _, err := s.emitExpr(expr.Right, operandType)
	if err != nil {
		return nil, nil, true, err
	}
	pred := C.LLVMIntPredicate(C.LLVMIntEQ)
	if expr.Op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntNE)
	}
	cmp := C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("ptrcmptmp"))
	return cmp, resultType, true, nil
}

func (s *functionState) emitOptionalCompareExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr.Op != lexer.TOKEN_EQEQ && expr.Op != lexer.TOKEN_BANGEQ {
		return nil, nil, false, nil
	}
	var (
		optionalExpr ast.Expr
		optionalType *semantic.OptionalType
	)
	switch leftOptional := leftType.(type) {
	case *semantic.OptionalType:
		if semantic.IsNullType(rightType) {
			optionalExpr = expr.Left
			optionalType = leftOptional
		}
	}
	if optionalType == nil {
		if rightOptional, ok := rightType.(*semantic.OptionalType); ok && semantic.IsNullType(leftType) {
			optionalExpr = expr.Right
			optionalType = rightOptional
		}
	}
	if optionalType == nil {
		return nil, nil, false, nil
	}
	optionalValue, _, err := s.emitExpr(optionalExpr, optionalType)
	if err != nil {
		return nil, nil, true, err
	}
	presentValue, err := s.extractOptionalPresent(optionalValue, optionalType)
	if err != nil {
		return nil, nil, true, err
	}
	if expr.Op == lexer.TOKEN_EQEQ {
		return C.LLVMBuildNot(s.builder, presentValue, cStringFree("optionalisnull")), resultType, true, nil
	}
	return presentValue, resultType, true, nil
}

func (s *functionState) emitPointerArithmeticExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	leftRef, leftIsRef := leftType.(*semantic.RefType)
	rightRef, rightIsRef := rightType.(*semantic.RefType)
	leftIsNumeric := isNumericType(leftType)
	rightIsNumeric := isNumericType(rightType)

	var (
		baseExpr  ast.Expr
		baseType  *semantic.RefType
		indexExpr ast.Expr
	)

	switch {
	case leftIsRef && rightIsNumeric && (expr.Op == lexer.TOKEN_PLUS || expr.Op == lexer.TOKEN_MINUS):
		baseExpr, baseType, indexExpr = expr.Left, leftRef, expr.Right
	case expr.Op == lexer.TOKEN_PLUS && leftIsNumeric && rightIsRef:
		baseExpr, baseType, indexExpr = expr.Right, rightRef, expr.Left
	default:
		return nil, nil, false, nil
	}

	baseValue, _, err := s.emitExpr(baseExpr, baseType)
	if err != nil {
		return nil, nil, true, err
	}
	indexValue, _, err := s.emitExpr(indexExpr, nil)
	if err != nil {
		return nil, nil, true, err
	}
	if expr.Op == lexer.TOKEN_MINUS {
		indexValue = C.LLVMBuildNeg(s.builder, indexValue, cStringFree("ptridx.neg"))
	}
	elemLLVMType, err := s.g.lowerType(baseType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	indices := []C.LLVMValueRef{indexValue}
	ptr := C.LLVMBuildGEP2(s.builder, elemLLVMType, baseValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("ptrarith"))
	return ptr, resultType, true, nil
}

func (s *functionState) emitRuntimeStringCompareExpr(expr *ast.BinaryExpr, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (C.LLVMValueRef, semantic.Type, error) {
	firstExpr := expr.Left
	secondExpr := expr.Right
	if swap {
		firstExpr, secondExpr = secondExpr, firstExpr
	}
	if helperName == "ctx_streq" {
		if literalText, ok := s.staticCStringLiteral(secondExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(firstExpr, firstType, secondExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("dstrlit.eq.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
		if literalText, ok := s.staticCStringLiteral(firstExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(secondExpr, secondType, firstExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("dstrlit.eq.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
	}
	if helperName == "ctx_string_view_eq" {
		if literalText, ok := s.staticCStringLiteral(secondExpr); ok {
			cmp, err := s.emitStringViewStaticLiteralEqual(firstExpr, firstType, secondExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("strcmp.lit.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
	}
	if cmp, ok, err := s.emitSameExtentRuntimeStringCompareExpr(expr.Op, firstExpr, firstType, secondExpr, secondType); ok {
		if err != nil {
			return nil, nil, err
		}
		return cmp, s.g.result.NamedTypes["bool"], nil
	}
	if cmp, ok, err := s.emitDisjointRuntimeStringCompareExpr(expr.Op, firstExpr, firstType, secondExpr, secondType); ok {
		if err != nil {
			return nil, nil, err
		}
		return cmp, s.g.result.NamedTypes["bool"], nil
	}
	firstValue, _, err := s.emitExpr(firstExpr, firstType)
	if err != nil {
		return nil, nil, err
	}
	secondValue, _, err := s.emitExpr(secondExpr, secondType)
	if err != nil {
		return nil, nil, err
	}
	helperReturn := s.g.result.NamedTypes["int"]
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{firstType, secondType},
		Return: helperReturn,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{firstValue, secondValue}
	call := s.buildCall(llvmFnType, callee, args, "streqtmp")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, nil, err
	}
	zero := C.LLVMConstInt(helperLLVMType, 0, 0)
	pred := C.LLVMIntPredicate(C.LLVMIntNE)
	if expr.Op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntEQ)
	}
	cmp := C.LLVMBuildICmp(s.builder, pred, call, zero, cStringFree("strcmp"))
	return cmp, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitSameExtentRuntimeStringCompareExpr(op lexer.TokenKind, firstExpr ast.Expr, firstType semantic.Type, secondExpr ast.Expr, secondType semantic.Type) (C.LLVMValueRef, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.ExprsHaveSameExtent(firstExpr, secondExpr) {
		return nil, false, nil
	}
	firstData, firstLen, firstLenType, firstKind, err := s.emitRuntimeStringCompareOperand(firstExpr, firstType)
	if err != nil {
		return nil, true, err
	}
	secondData, secondLen, secondLenType, secondKind, err := s.emitRuntimeStringCompareOperand(secondExpr, secondType)
	if err != nil {
		return nil, true, err
	}
	if firstKind == runtimeStringCompareNone || secondKind == runtimeStringCompareNone {
		return nil, false, nil
	}
	lenValue := firstLen
	lenType := firstLenType
	if lenValue == nil {
		lenValue = secondLen
		lenType = secondLenType
	}
	if lenValue == nil || lenType == nil {
		return nil, false, nil
	}
	usizeType := s.g.result.NamedTypes["usize"]
	coercedLen, err := s.coerceValue(lenValue, lenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	disjoint := s.g.result.ExprsAreDisjoint(firstExpr, secondExpr)
	cmp, err := s.emitMemcmpEqualValue(firstData, secondData, coercedLen, "streq.memcmp", disjoint)
	if err != nil {
		return nil, true, err
	}
	if op == lexer.TOKEN_BANGEQ {
		cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("streq.memcmp.not"))
	}
	return cmp, true, nil
}

func (s *functionState) emitDisjointRuntimeStringCompareExpr(op lexer.TokenKind, firstExpr ast.Expr, firstType semantic.Type, secondExpr ast.Expr, secondType semantic.Type) (C.LLVMValueRef, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.ExprsAreDisjoint(firstExpr, secondExpr) {
		return nil, false, nil
	}
	firstData, firstLen, firstLenType, firstKind, err := s.emitRuntimeStringCompareOperand(firstExpr, firstType)
	if err != nil {
		return nil, true, err
	}
	secondData, secondLen, secondLenType, secondKind, err := s.emitRuntimeStringCompareOperand(secondExpr, secondType)
	if err != nil {
		return nil, true, err
	}
	if firstKind == runtimeStringCompareNone || secondKind == runtimeStringCompareNone {
		return nil, false, nil
	}
	if firstLen == nil || firstLenType == nil || secondLen == nil || secondLenType == nil {
		return nil, false, nil
	}
	usizeType := s.g.result.NamedTypes["usize"]
	firstCoercedLen, err := s.coerceValue(firstLen, firstLenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	secondCoercedLen, err := s.coerceValue(secondLen, secondLenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), firstCoercedLen, secondCoercedLen, cStringFree("streq.disjoint.len.eq"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("streq.disjoint.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("streq.disjoint.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, memcmpBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	cmp, err := s.emitMemcmpEqualValue(firstData, secondData, firstCoercedLen, "streq.disjoint.memcmp", true)
	if err != nil {
		return nil, true, err
	}
	if op == lexer.TOKEN_BANGEQ {
		cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("streq.disjoint.memcmp.not"))
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("streq.disjoint.result"))
	fallbackRaw := C.ulonglong(0)
	if op == lexer.TOKEN_BANGEQ {
		fallbackRaw = 1
	}
	fallback := C.LLVMConstInt(boolType, fallbackRaw, 0)
	values := []C.LLVMValueRef{fallback, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, true, nil
}

func (s *functionState) emitRuntimeStringCompareOperand(expr ast.Expr, exprType semantic.Type) (C.LLVMValueRef, C.LLVMValueRef, semantic.Type, runtimeStringCompareKind, error) {
	kind := classifyRuntimeStringCompareKind(exprType)
	if kind == runtimeStringCompareNone {
		return nil, nil, nil, kind, nil
	}
	value, _, err := s.emitExpr(expr, exprType)
	if err != nil {
		return nil, nil, nil, kind, err
	}
	switch kind {
	case runtimeStringCompareView:
		lenType := s.g.result.NamedTypes["i64"]
		data := C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("streq.view.data"))
		length := C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("streq.view.len"))
		return data, length, lenType, kind, nil
	case runtimeStringCompareDStr:
		lenType := s.g.result.NamedTypes["i64"]
		length, err := s.emitRuntimeStringLengthValue(value, exprType, lenType, "streq.len")
		if err != nil {
			return nil, nil, nil, kind, err
		}
		return value, length, lenType, kind, nil
	case runtimeStringCompareRaw:
		return value, nil, nil, kind, nil
	default:
		return nil, nil, nil, kind, nil
	}
}

func (s *functionState) emitRuntimeStringLengthValue(stringValue C.LLVMValueRef, stringType semantic.Type, resultType semantic.Type, name string) (C.LLVMValueRef, error) {
	helperType := &semantic.FuncType{
		Name:   "ctx_strlen",
		Params: []semantic.Type{stringType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared("ctx_strlen", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stringValue}, name), nil
}

func (s *functionState) emitSpecializedRuntimeCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if value, actualType, handled, err := s.emitSpecializedStringViewLiteralCall(expr); handled {
		return value, actualType, true, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSliceEqCall(expr); handled {
		return value, actualType, true, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSlicesEqCall(expr); handled {
		return value, actualType, true, err
	}
	if value, actualType, handled, err := s.emitSpecializedRuntimeStringCompareCall(expr); handled {
		return value, actualType, true, err
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if ident.Name != "string_view_eq" && ident.Name != "ctx_string_view_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, false, nil
	}
	literalText, ok := s.staticCStringLiteral(expr.Args[1])
	if !ok {
		return nil, nil, false, nil
	}
	firstType := s.exprType(expr.Args[0])
	if classifyRuntimeStringCompareKind(firstType) != runtimeStringCompareView {
		return nil, nil, false, nil
	}
	cmp, err := s.emitStringViewStaticLiteralEqual(expr.Args[0], firstType, expr.Args[1], literalText)
	if err != nil {
		return nil, nil, true, err
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
}

func (s *functionState) staticIntLiteral(expr ast.Expr) (int64, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		value := n.Value
		if n.Suffix != "" {
			value += n.Suffix
		}
		return parseOptimizationExtentConstInt(value)
	case *ast.ParenExpr:
		return s.staticIntLiteral(n.Inner)
	case *ast.CastExpr:
		return s.staticIntLiteral(n.Operand)
	case *ast.CanExpr:
		return s.staticIntLiteral(n.Expr)
	default:
		return 0, false
	}
}

func (s *functionState) emitMinInt64Value(left C.LLVMValueRef, right C.LLVMValueRef, namePrefix string) C.LLVMValueRef {
	chooseLeft := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLE), left, right, cStringFree(namePrefix+".chooseleft"))
	leftBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".left"))
	rightBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".right"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".merge"))
	C.LLVMBuildCondBr(s.builder, chooseLeft, leftBB, rightBB)

	C.LLVMPositionBuilderAtEnd(s.builder, leftBB)
	leftEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, rightBB)
	rightEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt64TypeInContext(s.g.context), cStringFree(namePrefix))
	values := []C.LLVMValueRef{left, right}
	blocks := []C.LLVMBasicBlockRef{leftEnd, rightEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi
}

func (s *functionState) emitConstantClampedStringSliceOperand(expr ast.Expr, exprType semantic.Type, start int64, end int64, namePrefix string) (C.LLVMValueRef, C.LLVMValueRef, error) {
	if classifyRuntimeStringCompareKind(exprType) != runtimeStringCompareDStr {
		return nil, nil, fmt.Errorf("constant string slice specialization requires dstr operand")
	}
	stringValue, _, err := s.emitExpr(expr, exprType)
	if err != nil {
		return nil, nil, err
	}
	i64Type := s.g.result.NamedTypes["i64"]
	stringLen, err := s.emitRuntimeStringLengthValue(stringValue, exprType, i64Type, namePrefix+".len")
	if err != nil {
		return nil, nil, err
	}
	i64LLVMType := C.LLVMInt64TypeInContext(s.g.context)
	zeroI64 := C.LLVMConstInt(i64LLVMType, 0, 0)
	clampedStart := zeroI64
	if start > 0 {
		startValue := C.LLVMConstInt(i64LLVMType, C.ulonglong(start), 0)
		clampedStart = s.emitMinInt64Value(startValue, stringLen, namePrefix+".start")
	}
	clampedEnd := stringLen
	if end >= 0 {
		endValue := C.LLVMConstInt(i64LLVMType, C.ulonglong(end), 0)
		clampedEnd = s.emitMinInt64Value(endValue, stringLen, namePrefix+".end")
	}
	sliceLen := C.LLVMBuildSub(s.builder, clampedEnd, clampedStart, cStringFree(namePrefix+".slice.len"))
	sliceData := stringValue
	if start > 0 {
		usizeType := s.g.result.NamedTypes["usize"]
		clampedStartUsize, err := s.coerceValue(clampedStart, i64Type, usizeType)
		if err != nil {
			return nil, nil, err
		}
		i8LLVMType, err := s.g.lowerBuiltin("u8")
		if err != nil {
			return nil, nil, err
		}
		indices := []C.LLVMValueRef{clampedStartUsize}
		sliceData = C.LLVMBuildGEP2(s.builder, i8LLVMType, stringValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(namePrefix+".data"))
	}
	return sliceData, sliceLen, nil
}

func (s *functionState) constantDStrSliceCall(expr ast.Expr) (ast.Expr, semantic.Type, int64, int64, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.constantDStrSliceCall(n.Inner)
	case *ast.CastExpr:
		return s.constantDStrSliceCall(n.Operand)
	case *ast.CanExpr:
		return s.constantDStrSliceCall(n.Expr)
	case *ast.CallExpr:
		ident, ok := n.Func.(*ast.Ident)
		if !ok || ident.Name != "ctx_string_slice" || len(n.Args) != 3 {
			return nil, nil, 0, 0, false
		}
		baseExpr := n.Args[0]
		baseType := s.exprType(baseExpr)
		if classifyRuntimeStringCompareKind(baseType) != runtimeStringCompareDStr {
			return nil, nil, 0, 0, false
		}
		start, ok := s.staticIntLiteral(n.Args[1])
		if !ok || start < 0 {
			return nil, nil, 0, 0, false
		}
		end, ok := s.staticIntLiteral(n.Args[2])
		if !ok || end < start {
			return nil, nil, 0, 0, false
		}
		return baseExpr, baseType, start, end, true
	default:
		return nil, nil, 0, 0, false
	}
}

func (s *functionState) emitSpecializedStringSliceEqCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slice_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 4 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	leftType := s.exprType(leftExpr)
	if classifyRuntimeStringCompareKind(leftType) != runtimeStringCompareDStr {
		return nil, nil, false, nil
	}
	start, ok := s.staticIntLiteral(expr.Args[1])
	if !ok || start < 0 {
		return nil, nil, false, nil
	}
	end, ok := s.staticIntLiteral(expr.Args[2])
	if !ok || end < start {
		return nil, nil, false, nil
	}
	rightExpr := expr.Args[3]
	rightType := s.exprType(rightExpr)
	if classifyRuntimeStringCompareKind(rightType) != runtimeStringCompareDStr {
		return nil, nil, false, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData, leftSliceLen, err := s.emitConstantClampedStringSliceOperand(leftExpr, leftType, start, end, "strsliceeq.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightData, rightLen, rightLenType, rightKind, err := s.emitRuntimeStringCompareOperand(rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	if rightKind != runtimeStringCompareDStr || rightLen == nil || rightLenType == nil {
		return nil, nil, false, nil
	}
	rightLenI64, err := s.coerceValue(rightLen, rightLenType, s.g.result.NamedTypes["i64"])
	if err != nil {
		return nil, nil, true, err
	}
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, rightLenI64, cStringFree("strsliceeq.len.eq"))
	sliceZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), 0, 0), cStringFree("strsliceeq.len.zero"))
	dataEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftData, rightData, cStringFree("strsliceeq.data.eq"))
	usizeType := s.g.result.NamedTypes["usize"]
	lenValue, err := s.coerceValue(leftSliceLen, s.g.result.NamedTypes["i64"], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	zeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.zero"))
	nonZeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.nonzero"))
	sameBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.same"))
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, zeroBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, zeroBB)
	zeroEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildCondBr(s.builder, sliceZero, mergeBB, nonZeroBB)

	C.LLVMPositionBuilderAtEnd(s.builder, nonZeroBB)
	C.LLVMBuildCondBr(s.builder, dataEqual, sameBB, memcmpBB)

	C.LLVMPositionBuilderAtEnd(s.builder, sameBB)
	sameEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	disjoint := s.g.result.ExprsAreDisjoint(leftExpr, rightExpr)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, lenValue, "strsliceeq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("strsliceeq.result"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	trueValue := C.LLVMConstInt(boolType, 1, 0)
	values := []C.LLVMValueRef{falseValue, trueValue, trueValue, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, zeroEnd, sameEnd, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return C.LLVMBuildZExt(s.builder, phi, intLLVMType, cStringFree("strsliceeq.int")), intType, true, nil
}

func (s *functionState) emitSpecializedStringSlicesEqCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slices_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 6 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	leftStartExpr := expr.Args[1]
	leftEndExpr := expr.Args[2]
	rightExpr := expr.Args[3]
	rightStartExpr := expr.Args[4]
	rightEndExpr := expr.Args[5]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	if classifyRuntimeStringCompareKind(leftType) != runtimeStringCompareDStr || classifyRuntimeStringCompareKind(rightType) != runtimeStringCompareDStr {
		return nil, nil, false, nil
	}
	leftStart, ok := s.staticIntLiteral(leftStartExpr)
	if !ok || leftStart < 0 {
		return nil, nil, false, nil
	}
	leftEnd, ok := s.staticIntLiteral(leftEndExpr)
	if !ok || leftEnd < leftStart {
		return nil, nil, false, nil
	}
	rightStart, ok := s.staticIntLiteral(rightStartExpr)
	if !ok || rightStart < 0 {
		return nil, nil, false, nil
	}
	rightEnd, ok := s.staticIntLiteral(rightEndExpr)
	if !ok || rightEnd < rightStart {
		return nil, nil, false, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData, leftSliceLen, err := s.emitConstantClampedStringSliceOperand(leftExpr, leftType, leftStart, leftEnd, "strsliceseq.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightData, rightSliceLen, err := s.emitConstantClampedStringSliceOperand(rightExpr, rightType, rightStart, rightEnd, "strsliceseq.right")
	if err != nil {
		return nil, nil, true, err
	}
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, rightSliceLen, cStringFree("strsliceseq.len.eq"))
	sliceZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), 0, 0), cStringFree("strsliceseq.len.zero"))
	dataEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftData, rightData, cStringFree("strsliceseq.data.eq"))
	usizeType := s.g.result.NamedTypes["usize"]
	lenValue, err := s.coerceValue(leftSliceLen, s.g.result.NamedTypes["i64"], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	zeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.zero"))
	nonZeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.nonzero"))
	sameBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.same"))
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, zeroBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, zeroBB)
	zeroEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildCondBr(s.builder, sliceZero, mergeBB, nonZeroBB)

	C.LLVMPositionBuilderAtEnd(s.builder, nonZeroBB)
	C.LLVMBuildCondBr(s.builder, dataEqual, sameBB, memcmpBB)

	C.LLVMPositionBuilderAtEnd(s.builder, sameBB)
	sameEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	disjoint := s.g.result.ExprsAreDisjoint(leftExpr, rightExpr)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, lenValue, "strsliceseq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("strsliceseq.result"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	trueValue := C.LLVMConstInt(boolType, 1, 0)
	values := []C.LLVMValueRef{falseValue, trueValue, trueValue, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, zeroEnd, sameEnd, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return C.LLVMBuildZExt(s.builder, phi, intLLVMType, cStringFree("strsliceseq.int")), intType, true, nil
}

func (s *functionState) emitSpecializedRuntimeStringCompareCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	switch ident.Name {
	case "ctx_streq", "ctx_string_view_eq", "string_view_eq", "ctx_string_views_eq", "string_views_eq":
	default:
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	rightExpr := expr.Args[1]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	if ident.Name == "ctx_streq" {
		if literalText, ok := s.staticCStringLiteral(rightExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(leftExpr, leftType, rightExpr, literalText)
			if err != nil {
				return nil, nil, true, err
			}
			intType := s.g.result.NamedTypes["int"]
			intLLVMType, err := s.g.lowerType(intType)
			if err != nil {
				return nil, nil, true, err
			}
			return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("dstrlit.direct.int")), intType, true, nil
		}
		if literalText, ok := s.staticCStringLiteral(leftExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(rightExpr, rightType, leftExpr, literalText)
			if err != nil {
				return nil, nil, true, err
			}
			intType := s.g.result.NamedTypes["int"]
			intLLVMType, err := s.g.lowerType(intType)
			if err != nil {
				return nil, nil, true, err
			}
			return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("dstrlit.direct.int")), intType, true, nil
		}
	}
	cmp, ok, err := s.emitSameExtentRuntimeStringCompareExpr(lexer.TOKEN_EQEQ, leftExpr, leftType, rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		cmp, ok, err = s.emitDisjointRuntimeStringCompareExpr(lexer.TOKEN_EQEQ, leftExpr, leftType, rightExpr, rightType)
		if err != nil {
			return nil, nil, true, err
		}
		if !ok {
			return nil, nil, false, nil
		}
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("streq.direct.int")), intType, true, nil
}

func isStringViewCarrierType(t semantic.Type) bool {
	return classifyRuntimeStringCompareKind(t) == runtimeStringCompareView
}

func (s *functionState) emitGlobalCStringLiteral(text string, name string) C.LLVMValueRef {
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	textC := cString(text)
	defer C.free(unsafe.Pointer(textC))
	return C.LLVMBuildGlobalStringPtr(s.builder, textC, nameC)
}

func (s *functionState) emitInternSmallStringCall(data C.LLVMValueRef, lenValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	u8Type := s.g.result.NamedTypes["u8"]
	usizeType := s.g.result.NamedTypes["usize"]
	srcType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	retType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "intern_small_string", Params: []semantic.Type{srcType, usizeType}, Return: retType}
	callee, err := s.g.ensureFunctionDeclared("intern_small_string", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{data, lenValue}, name), nil
}

func (s *functionState) emitDirectStringViewCopyLarge(viewData C.LLVMValueRef, viewLen C.LLVMValueRef) (C.LLVMValueRef, error) {
	i64Type := s.g.result.NamedTypes["i64"]
	usizeType := s.g.result.NamedTypes["usize"]
	voidType := s.g.result.NamedTypes["void"]
	u8Type := s.g.result.NamedTypes["u8"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	nullableU8RefType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	heapVoidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	allocType := &semantic.FuncType{Name: "alloc_perm", Params: []semantic.Type{i64Type}, Return: heapVoidRefType}
	allocCallee, err := s.g.ensureFunctionDeclared("alloc_perm", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	oneValue := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), 1, 0)
	allocSize := C.LLVMBuildAdd(s.builder, viewLen, oneValue, cStringFree("svcopy.alloc.size"))
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{allocSize}, "svcopy.alloc")

	lenUsize, err := s.coerceValue(viewLen, i64Type, usizeType)
	if err != nil {
		return nil, err
	}
	memcpyType := &semantic.FuncType{Name: "memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("memcpy", memcpyType)
	if err != nil {
		return nil, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, err
	}
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{allocPtr, viewData, lenUsize}, "svcopy.memcpy")

	i8LLVMType, err := s.g.lowerBuiltin("u8")
	if err != nil {
		return nil, err
	}
	bytePtr := C.LLVMBuildGEP2(s.builder, i8LLVMType, allocPtr, llvmValueSlicePtr([]C.LLVMValueRef{lenUsize}), 1, cStringFree("svcopy.term.ptr"))
	zeroByte := C.LLVMConstInt(i8LLVMType, 0, 0)
	C.LLVMBuildStore(s.builder, zeroByte, bytePtr)

	registerType := &semantic.FuncType{Name: "register_perm_string_len", Params: []semantic.Type{nullableU8RefType, usizeType}, Return: voidType}
	registerCallee, err := s.g.ensureFunctionDeclared("register_perm_string_len", registerType)
	if err != nil {
		return nil, err
	}
	registerLLVMType, err := s.g.lowerFunctionType(registerType)
	if err != nil {
		return nil, err
	}
	_ = s.buildCall(registerLLVMType, registerCallee, []C.LLVMValueRef{allocPtr, lenUsize}, "")
	return allocPtr, nil
}

func (s *functionState) emitSpecializedStringSliceCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slice" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	resultType := s.exprType(expr)
	inputExpr := expr.Args[0]
	inputType := s.exprType(inputExpr)
	if _, ok := inputType.(*semantic.DStrType); !ok {
		return nil, nil, false, nil
	}
	if s.g.result.ExprsHaveSameExtent(expr, inputExpr) {
		value, _, err := s.emitExpr(inputExpr, inputType)
		return value, resultType, true, err
	}
	sliceFacts, ok := s.g.result.ExprOptimizationFacts(expr)
	if !ok || !sliceFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	exactLen, ok := constOptimizationExtentSize(sliceFacts.Extent)
	if !ok {
		return nil, nil, false, nil
	}
	begin, ok := parseOptimizationExtentConstInt(sliceFacts.Extent.Begin)
	if !ok || begin < 0 {
		return nil, nil, false, nil
	}
	inputValue, _, err := s.emitExpr(inputExpr, inputType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	beginValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(begin), 0)
	sliceData := inputValue
	if begin != 0 {
		i8LLVMType, err := s.g.lowerBuiltin("u8")
		if err != nil {
			return nil, nil, true, err
		}
		indices := []C.LLVMValueRef{beginValue}
		sliceData = C.LLVMBuildGEP2(s.builder, i8LLVMType, inputValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("strslice.data"))
	}
	lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(exactLen), 0)
	if exactLen == 0 {
		emptyPtr := s.emitGlobalCStringLiteral("", "strslice.empty")
		value, err := s.emitInternSmallStringCall(emptyPtr, lenValue, "strslice.zero.small")
		return value, resultType, true, err
	}
	if exactLen <= 8 {
		value, err := s.emitInternSmallStringCall(sliceData, lenValue, "strslice.small")
		return value, resultType, true, err
	}
	largeLen := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), C.ulonglong(exactLen), 0)
	value, err := s.emitDirectStringViewCopyLarge(sliceData, largeLen)
	return value, resultType, true, err
}

func (s *functionState) emitSpecializedStringViewCopyCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || (ident.Name != "string_view_copy" && ident.Name != "ctx_string_from_view") {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	viewExpr := expr.Args[0]
	viewType := s.exprType(viewExpr)
	resultType := s.exprType(expr)
	if !isStringViewCarrierType(viewType) {
		return nil, nil, false, nil
	}
	viewFacts, ok := s.g.result.ExprOptimizationFacts(viewExpr)
	if !ok || !viewFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, nil, true, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("svcopy.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("svcopy.len"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	if exactLen, ok := constOptimizationExtentSize(viewFacts.Extent); ok {
		lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(exactLen), 0)
		if exactLen <= 8 {
			dataValue := viewData
			if exactLen == 0 {
				dataValue = s.emitGlobalCStringLiteral("", "svcopy.empty")
			}
			value, err := s.emitInternSmallStringCall(dataValue, lenValue, "svcopy.small")
			return value, resultType, true, err
		}
		largeLen := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), C.ulonglong(exactLen), 0)
		value, err := s.emitDirectStringViewCopyLarge(viewData, largeLen)
		return value, resultType, true, err
	}

	i64LLVMType := C.LLVMInt64TypeInContext(s.g.context)
	zeroLen := C.LLVMConstInt(i64LLVMType, 0, 0)
	eightLen := C.LLVMConstInt(i64LLVMType, 8, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLE), viewLen, zeroLen, cStringFree("svcopy.len.zero"))
	positiveBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.positive"))
	zeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.zero"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.merge"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildCondBr(s.builder, zeroCond, zeroBB, positiveBB)

	C.LLVMPositionBuilderAtEnd(s.builder, zeroBB)
	emptyPtr := s.emitGlobalCStringLiteral("", "svcopy.empty")
	zeroSmall, err := s.emitInternSmallStringCall(emptyPtr, C.LLVMConstInt(usizeLLVMType, 0, 0), "svcopy.zero.small")
	if err != nil {
		return nil, nil, true, err
	}
	zeroEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, positiveBB)
	smallCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLE), viewLen, eightLen, cStringFree("svcopy.len.small"))
	smallBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.small"))
	largeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.large"))
	C.LLVMBuildCondBr(s.builder, smallCond, smallBB, largeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, smallBB)
	viewLenUsize, err := s.coerceValue(viewLen, s.g.result.NamedTypes["i64"], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	smallValue, err := s.emitInternSmallStringCall(viewData, viewLenUsize, "svcopy.small")
	if err != nil {
		return nil, nil, true, err
	}
	smallEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, largeBB)
	largeValue, err := s.emitDirectStringViewCopyLarge(viewData, viewLen)
	if err != nil {
		return nil, nil, true, err
	}
	largeEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmResultType, cStringFree("svcopy.result"))
	values := []C.LLVMValueRef{zeroSmall, smallValue, largeValue}
	blocks := []C.LLVMBasicBlockRef{zeroEnd, smallEnd, largeEnd}
	_ = entryBlock
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}

func (s *functionState) emitSpecializedStringViewLiteralCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, false, nil
	}
	viewArgIndex, literalArgIndex, returnsInt, ok := s.specializedStringViewLiteralCallShape(ident.Name)
	if !ok {
		return nil, nil, false, nil
	}
	literalText, ok := s.staticCStringLiteral(expr.Args[literalArgIndex])
	if !ok {
		return nil, nil, false, nil
	}
	viewType := s.exprType(expr.Args[viewArgIndex])
	if classifyRuntimeStringCompareKind(viewType) != runtimeStringCompareView {
		return nil, nil, false, nil
	}
	cmp, err := s.emitStringViewStaticLiteralEqual(expr.Args[viewArgIndex], viewType, expr.Args[literalArgIndex], literalText)
	if err != nil {
		return nil, nil, true, err
	}
	if !returnsInt {
		return cmp, s.exprType(expr), true, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
}

func (s *functionState) specializedStringViewLiteralCallShape(funcName string) (int, int, bool, bool) {
	if funcName == "string_view_eq" || funcName == "ctx_string_view_eq" {
		return 0, 1, true, true
	}
	sym, ok := s.g.result.GlobalScope.Lookup(funcName)
	if !ok {
		return 0, 0, false, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || len(decl.Params) != 2 || len(decl.Body) != 1 {
		return 0, 0, false, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		return 0, 0, false, false
	}
	callExpr, ok := s.boolStringViewLiteralWrapperCall(ret.Value, decl.Params[0].Name, decl.Params[1].Name)
	if !ok || callExpr == nil {
		return 0, 0, false, false
	}
	callee, ok := callExpr.Func.(*ast.Ident)
	if !ok {
		return 0, 0, false, false
	}
	if callee.Name != "string_view_eq" && callee.Name != "ctx_string_view_eq" {
		return 0, 0, false, false
	}
	return 0, 1, false, true
}

func (s *functionState) boolStringViewLiteralWrapperCall(expr ast.Expr, viewParam string, literalParam string) (*ast.CallExpr, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_EQEQ && binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	callExpr, intLit, ok := unwrapCallComparedToZero(binary.Left, binary.Right)
	if !ok {
		callExpr, intLit, ok = unwrapCallComparedToZero(binary.Right, binary.Left)
	}
	if !ok || intLit == nil || intLit.Value != "0" {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	if !matchesStringViewLiteralWrapperArgs(callExpr, viewParam, literalParam) {
		return nil, false
	}
	return callExpr, true
}

func unwrapCallComparedToZero(left ast.Expr, right ast.Expr) (*ast.CallExpr, *ast.IntLit, bool) {
	callExpr, ok := left.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	intLit, ok := right.(*ast.IntLit)
	if !ok {
		return nil, nil, false
	}
	return callExpr, intLit, true
}

func matchesStringViewLiteralWrapperArgs(callExpr *ast.CallExpr, viewParam string, literalParam string) bool {
	if callExpr == nil || len(callExpr.Args) != 2 {
		return false
	}
	viewIdent, ok := callExpr.Args[0].(*ast.Ident)
	if !ok || viewIdent.Name != viewParam {
		return false
	}
	return exprIsParamOrCastOfParam(callExpr.Args[1], literalParam)
}

func exprIsParamOrCastOfParam(expr ast.Expr, paramName string) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name == paramName
	case *ast.ParenExpr:
		return exprIsParamOrCastOfParam(n.Inner, paramName)
	case *ast.CastExpr:
		return exprIsParamOrCastOfParam(n.Operand, paramName)
	default:
		return false
	}
}

func stripMemcpyOperandExpr(expr ast.Expr) ast.Expr {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.CanExpr:
			expr = n.Expr
		default:
			return expr
		}
	}
	return nil
}

func isMemcpyViewCarrierType(t semantic.Type) bool {
	switch tt := t.(type) {
	case *semantic.ViewType, *semantic.DArrayViewType, *semantic.SViewType:
		return true
	case *semantic.StructType:
		return tt != nil && (tt.Name == "DynArrayView" || tt.Name == "StringView")
	default:
		return false
	}
}

func isDynArrayViewCarrierType(t semantic.Type) bool {
	switch tt := t.(type) {
	case *semantic.ViewType, *semantic.DArrayViewType:
		return true
	case *semantic.StructType:
		return tt != nil && tt.Name == "DynArrayView"
	default:
		return false
	}
}

func isDynArrayCarrierType(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.DArrayType:
		return true
	default:
		return false
	}
}

func (s *functionState) memcpyDisjointCarrierExpr(expr ast.Expr) ast.Expr {
	stripped := stripMemcpyOperandExpr(expr)
	fieldExpr, ok := stripped.(*ast.FieldExpr)
	if !ok || fieldExpr.Field != "data" {
		return nil
	}
	if !isMemcpyViewCarrierType(s.exprType(fieldExpr.Object)) {
		return nil
	}
	return fieldExpr.Object
}

func (s *functionState) memcpyOperandsAreDisjoint(destExpr ast.Expr, srcExpr ast.Expr) bool {
	if s == nil || s.g == nil || s.g.result == nil {
		return false
	}
	if s.g.result.ExprsAreDisjoint(destExpr, srcExpr) {
		return true
	}
	destCarrier := s.memcpyDisjointCarrierExpr(destExpr)
	srcCarrier := s.memcpyDisjointCarrierExpr(srcExpr)
	if destCarrier == nil || srcCarrier == nil {
		return false
	}
	return s.g.result.ExprsAreDisjoint(destCarrier, srcCarrier)
}

func (s *functionState) emitSpecializedMemcpyCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if ident.Name != "memcpy" && ident.Name != "arena_memcpy" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 {
		return nil, nil, false, nil
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil {
		return nil, nil, true, fmt.Errorf("copy helper target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, true, err
	}
	args := make([]C.LLVMValueRef, 0, len(expr.Args))
	for i, arg := range expr.Args {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, true, err
		}
		args = append(args, value)
	}
	callName := "calltmp"
	if isVoidType(funcType.Return) {
		callName = ""
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	if s.memcpyOperandsAreDisjoint(expr.Args[0], expr.Args[1]) {
		s.addCallSiteEnumAttribute(call, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(call, C.uint(2), "noalias")
	}
	return call, funcType.Return, true, nil
}

func (s *functionState) emitSpecializedArenaViewCopyCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_copy_exact" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	dstExpr := expr.Args[0]
	srcExpr := expr.Args[1]
	dstType := s.exprType(dstExpr)
	srcType := s.exprType(srcExpr)
	if !isDynArrayViewCarrierType(dstType) || !isDynArrayViewCarrierType(srcType) {
		return nil, nil, false, nil
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil {
		return nil, nil, true, fmt.Errorf("arena_da_copy_exact target does not have a function type")
	}
	exactCopyCount := uint64(0)
	hasSmallExactCopyCount := false
	if dstFacts, ok := s.g.result.ExprOptimizationFacts(dstExpr); ok {
		if dstCount, ok := constOptimizationExtentSize(dstFacts.Extent); ok && dstCount <= smallExactArenaCopyUnrollLimit {
			if srcFacts, ok := s.g.result.ExprOptimizationFacts(srcExpr); ok {
				if srcCount, ok := constOptimizationExtentSize(srcFacts.Extent); ok && srcCount == dstCount {
					exactCopyCount = dstCount
					hasSmallExactCopyCount = true
				}
			}
		}
	}
	disjoint := s.g.result.ExprsAreDisjoint(dstExpr, srcExpr)
	if !hasSmallExactCopyCount && !disjoint {
		return nil, nil, false, nil
	}
	dstValue, _, err := s.emitExpr(dstExpr, dstType)
	if err != nil {
		return nil, nil, true, err
	}
	srcValue, _, err := s.emitExpr(srcExpr, srcType)
	if err != nil {
		return nil, nil, true, err
	}
	if hasSmallExactCopyCount && !disjoint {
		if exactCopyCount == 0 {
			return nil, funcType.Return, true, nil
		}
		var elemType semantic.Type
		switch viewType := funcType.Params[0].(type) {
		case *semantic.ViewType:
			elemType = viewType.Elem
		case *semantic.DArrayViewType:
			elemType = viewType.Elem
		default:
			return nil, nil, true, fmt.Errorf("arena_da_copy_exact specialization expected dview parameter, got %T", funcType.Params[0])
		}
		elemLLVMType, err := s.g.lowerType(elemType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		dstData := C.LLVMBuildExtractValue(s.builder, dstValue, 0, cStringFree("dview.copy.dst.data"))
		srcData := C.LLVMBuildExtractValue(s.builder, srcValue, 0, cStringFree("dview.copy.src.data"))
		for i := uint64(0); i < exactCopyCount; i++ {
			indexValue := C.LLVMConstInt(usizeType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			srcPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, srcData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.copy.src.elem.ptr"))
			elemValue := C.LLVMBuildLoad2(s.builder, elemLLVMType, srcPtr, cStringFree("dview.copy.elem"))
			dstPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dstData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.copy.dst.elem.ptr"))
			C.LLVMBuildStore(s.builder, elemValue, dstPtr)
		}
		return nil, funcType.Return, true, nil
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, true, err
	}
	dstData := C.LLVMBuildExtractValue(s.builder, dstValue, 0, cStringFree("dview.copy.dst.data"))
	dstLen := C.LLVMBuildExtractValue(s.builder, dstValue, 1, cStringFree("dview.copy.dst.len"))
	dstElemSize := C.LLVMBuildExtractValue(s.builder, dstValue, 2, cStringFree("dview.copy.dst.elem_size"))
	srcData := C.LLVMBuildExtractValue(s.builder, srcValue, 0, cStringFree("dview.copy.src.data"))
	srcLen := C.LLVMBuildExtractValue(s.builder, srcValue, 1, cStringFree("dview.copy.src.len"))
	srcElemSize := C.LLVMBuildExtractValue(s.builder, srcValue, 2, cStringFree("dview.copy.src.elem_size"))
	dstBytes := C.LLVMBuildMul(s.builder, dstLen, dstElemSize, cStringFree("dview.copy.dst.bytes"))
	srcBytes := C.LLVMBuildMul(s.builder, srcLen, srcElemSize, cStringFree("dview.copy.src.bytes"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeType, 0, 0)
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	buildMemcpyNoAlias := func(byteCount C.LLVMValueRef) {
		memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstData, srcData, byteCount}, "dview.copy.memcpy")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	}

	if s.g.result.ExprsHaveEqualExtentSize(dstExpr, srcExpr) {
		zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, zeroBytes, cStringFree("dview.copy.bytes.zero"))
		copyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.exact.fast"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.exact.merge"))
		C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, copyBB)

		C.LLVMPositionBuilderAtEnd(s.builder, copyBB)
		buildMemcpyNoAlias(dstBytes)
		C.LLVMBuildBr(s.builder, mergeBB)

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		return nil, funcType.Return, true, nil
	}

	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, srcBytes, cStringFree("dview.copy.bytes.eq"))
	copyCheckBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.fast.check"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, copyCheckBB, fallbackBB)

	C.LLVMPositionBuilderAtEnd(s.builder, copyCheckBB)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, zeroBytes, cStringFree("dview.copy.bytes.zero"))
	copyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.fast"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, copyBB)

	C.LLVMPositionBuilderAtEnd(s.builder, copyBB)
	buildMemcpyNoAlias(dstBytes)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackCall := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{dstValue, srcValue}, "")
	_ = fallbackCall
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return nil, funcType.Return, true, nil
}

func (s *functionState) emitSpecializedArenaViewEqCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_eq_exact" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	rightExpr := expr.Args[1]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	resultType := s.exprType(expr)
	if resultType == nil {
		resultType = s.g.result.NamedTypes["bool"]
	}
	if !isDynArrayViewCarrierType(leftType) || !isDynArrayViewCarrierType(rightType) {
		return nil, nil, false, nil
	}
	if !s.g.result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		return nil, nil, false, nil
	}
	disjoint := s.g.result.ExprsAreDisjoint(leftExpr, rightExpr)

	leftValue, _, err := s.emitExpr(leftExpr, leftType)
	if err != nil {
		return nil, nil, true, err
	}
	rightValue, _, err := s.emitExpr(rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData := C.LLVMBuildExtractValue(s.builder, leftValue, 0, cStringFree("dview.eq.left.data"))
	leftLen := C.LLVMBuildExtractValue(s.builder, leftValue, 1, cStringFree("dview.eq.left.len"))
	leftElemSize := C.LLVMBuildExtractValue(s.builder, leftValue, 2, cStringFree("dview.eq.left.elem_size"))
	rightData := C.LLVMBuildExtractValue(s.builder, rightValue, 0, cStringFree("dview.eq.right.data"))
	_ = C.LLVMBuildExtractValue(s.builder, rightValue, 1, cStringFree("dview.eq.right.len"))
	_ = C.LLVMBuildExtractValue(s.builder, rightValue, 2, cStringFree("dview.eq.right.elem_size"))
	byteCount := C.LLVMBuildMul(s.builder, leftLen, leftElemSize, cStringFree("dview.eq.bytes"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteCount, zeroBytes, cStringFree("dview.eq.bytes.zero"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.eq.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.eq.merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, memcmpBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, byteCount, "dview.eq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("dview.eq.result"))
	trueValue := C.LLVMConstInt(boolType, 1, 0)
	values := []C.LLVMValueRef{trueValue, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}

func (s *functionState) emitSpecializedArenaFromViewCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_from_view" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	arenaExpr := expr.Args[0]
	viewExpr := expr.Args[1]
	arenaType := s.exprType(arenaExpr)
	viewType := s.exprType(viewExpr)
	resultType := s.exprType(expr)
	if !isDynArrayViewCarrierType(viewType) || !isDynArrayCarrierType(resultType) {
		return nil, nil, false, nil
	}
	viewFacts, ok := s.g.result.ExprOptimizationFacts(viewExpr)
	if !ok || !viewFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	arenaValue, _, err := s.emitExpr(arenaExpr, arenaType)
	if err != nil {
		return nil, nil, true, err
	}
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, nil, true, err
	}
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroResult, err := s.zeroValue(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("dview.materialize.src.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("dview.materialize.src.len"))
	viewElemSize := C.LLVMBuildExtractValue(s.builder, viewValue, 2, cStringFree("dview.materialize.src.elem_size"))
	byteCount := C.LLVMBuildMul(s.builder, viewLen, viewElemSize, cStringFree("dview.materialize.bytes"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeLLVMType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteCount, zeroBytes, cStringFree("dview.materialize.bytes.zero"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.materialize.alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.materialize.merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	if _, ok := resultType.(*semantic.DArrayType); !ok {
		return nil, nil, true, fmt.Errorf("arena_da_from_view specialization expected darray result type, got %T", resultType)
	}
	allocType := &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaType, usizeType}, Return: voidRefType}
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaValue, byteCount}, "dview.materialize.alloc")

	memcpyType := &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{allocPtr, viewData, byteCount}, "dview.materialize.memcpy")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")

	materialized := C.LLVMGetUndef(llvmResultType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, allocPtr, 0, cStringFree("dview.materialize.items"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 1, cStringFree("dview.materialize.count"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 2, cStringFree("dview.materialize.capacity"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, llvmResultType, cStringFree("dview.materialize.result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}

func (s *functionState) emitSpecializedArenaViewFillCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_fill" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	dstExpr := expr.Args[0]
	dstType := s.exprType(dstExpr)
	resultType := s.exprType(expr)
	fillExpr := expr.Args[1]
	_, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil || len(funcType.Params) != 2 {
		return nil, nil, true, fmt.Errorf("arena_da_fill target does not have the expected function type")
	}
	fillType := funcType.Params[1]
	fillByte, constByte := staticRepeatedByteFillValueForType(s, fillExpr, fillType)
	dynamicByte := !constByte && isSingleByteScalarFillType(s, fillType)
	if !isDynArrayViewCarrierType(dstType) || !s.g.result.ExprSupportsDenseWrite(dstExpr) {
		return nil, nil, false, nil
	}
	exactFillCount := uint64(0)
	hasSmallExactFillCount := false
	if !constByte && !dynamicByte {
		if facts, ok := s.g.result.ExprOptimizationFacts(dstExpr); ok {
			if count, ok := constOptimizationExtentSize(facts.Extent); ok && count <= smallExactArenaFillUnrollLimit {
				exactFillCount = count
				hasSmallExactFillCount = true
			}
		}
		if !hasSmallExactFillCount {
			return nil, nil, false, nil
		}
	}
	dstValue, _, err := s.emitExpr(dstExpr, dstType)
	if err != nil {
		return nil, nil, true, err
	}
	var fillValue C.LLVMValueRef
	if constByte {
		fillValue = C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), C.ulonglong(fillByte), 0)
	} else {
		fillRawValue, actualFillType, err := s.emitExpr(fillExpr, fillType)
		if err != nil {
			return nil, nil, true, err
		}
		fillValue, err = s.coerceValue(fillRawValue, actualFillType, s.g.result.NamedTypes["i32"])
		if err != nil {
			return nil, nil, true, err
		}
	}
	dstData := C.LLVMBuildExtractValue(s.builder, dstValue, 0, cStringFree("dview.fill.dst.data"))
	if !(constByte || dynamicByte) {
		if exactFillCount == 0 {
			return nil, resultType, true, nil
		}
		elemLLVMType, err := s.g.lowerType(fillType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		for i := uint64(0); i < exactFillCount; i++ {
			indexValue := C.LLVMConstInt(usizeType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dstData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.fill.elem.ptr"))
			C.LLVMBuildStore(s.builder, fillValue, elemPtr)
		}
		return nil, resultType, true, nil
	}
	dstLen := C.LLVMBuildExtractValue(s.builder, dstValue, 1, cStringFree("dview.fill.dst.len"))
	dstElemSize := C.LLVMBuildExtractValue(s.builder, dstValue, 2, cStringFree("dview.fill.dst.elem_size"))
	dstBytes := C.LLVMBuildMul(s.builder, dstLen, dstElemSize, cStringFree("dview.fill.dst.bytes"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, zeroBytes, cStringFree("dview.fill.bytes.zero"))
	fillBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.fill.fast"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.fill.merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, fillBB)

	C.LLVMPositionBuilderAtEnd(s.builder, fillBB)
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memsetValueType := s.g.result.NamedTypes["int"]
	memsetType := &semantic.FuncType{Name: "memset", Params: []semantic.Type{voidRefType, memsetValueType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	memsetCallee, err := s.g.ensureFunctionDeclared("memset", memsetType)
	if err != nil {
		return nil, nil, true, err
	}
	fillValue, err = s.coerceValue(fillValue, s.g.result.NamedTypes["i32"], memsetValueType)
	if err != nil {
		return nil, nil, true, err
	}
	memsetLLVMType, err := s.g.lowerFunctionType(memsetType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(memsetLLVMType, memsetCallee, []C.LLVMValueRef{dstData, fillValue, dstBytes}, "dview.fill.memset")
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return nil, resultType, true, nil
}

func (s *functionState) emitStringViewStaticLiteralEqual(viewExpr ast.Expr, viewType semantic.Type, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("svlit.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("svlit.len"))
	literalLen := len([]byte(literalText))
	lenLLVMType, err := s.g.lowerBuiltin("i64")
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMConstInt(lenLLVMType, C.ulonglong(literalLen), 0)
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), viewLen, lenValue, cStringFree("svlit.len.eq"))
	if literalLen == 0 {
		return lenEqual, nil
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	compareBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.compare"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, compareBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, compareBB)
	var compareValue C.LLVMValueRef
	if literalLen <= 8 {
		compareValue, err = s.emitStringViewLiteralBytesEqual(viewData, literalText)
	} else {
		literalValue, _, emitErr := s.emitExpr(literalExpr, nil)
		if emitErr != nil {
			return nil, emitErr
		}
		compareValue, err = s.emitMemcmpEqual(viewData, literalValue, literalLen)
	}
	if err != nil {
		return nil, err
	}
	compareEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("svlit.eq"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	values := []C.LLVMValueRef{falseValue, compareValue}
	blocks := []C.LLVMBasicBlockRef{entryBlock, compareEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}

func (s *functionState) emitDStrStaticLiteralEqual(textExpr ast.Expr, textType semantic.Type, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	if classifyRuntimeStringCompareKind(textType) != runtimeStringCompareDStr {
		return nil, fmt.Errorf("dstr literal specialization requires dstr operand")
	}
	lenType := s.g.result.NamedTypes["i64"]
	var (
		textData C.LLVMValueRef
		textLen  C.LLVMValueRef
		err      error
	)
	if baseExpr, baseType, start, end, ok := s.constantDStrSliceCall(textExpr); ok {
		textData, textLen, err = s.emitConstantClampedStringSliceOperand(baseExpr, baseType, start, end, "dstrlit.slice")
		if err != nil {
			return nil, err
		}
	} else {
		textValue, _, err := s.emitExpr(textExpr, textType)
		if err != nil {
			return nil, err
		}
		textData = textValue
		textLen, err = s.emitRuntimeStringLengthValue(textValue, textType, lenType, "dstrlit.len")
		if err != nil {
			return nil, err
		}
	}
	literalLen := len([]byte(literalText))
	lenLLVMType, err := s.g.lowerBuiltin("i64")
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMConstInt(lenLLVMType, C.ulonglong(literalLen), 0)
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), textLen, lenValue, cStringFree("dstrlit.len.eq"))
	if literalLen == 0 {
		return lenEqual, nil
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	compareBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dstrlit.compare"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dstrlit.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, compareBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, compareBB)
	var compareValue C.LLVMValueRef
	if literalLen <= 8 {
		compareValue, err = s.emitStringViewLiteralBytesEqual(textData, literalText)
	} else {
		literalValue, _, emitErr := s.emitExpr(literalExpr, nil)
		if emitErr != nil {
			return nil, emitErr
		}
		compareValue, err = s.emitMemcmpEqual(textData, literalValue, literalLen)
	}
	if err != nil {
		return nil, err
	}
	compareEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("dstrlit.eq"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	values := []C.LLVMValueRef{falseValue, compareValue}
	blocks := []C.LLVMBasicBlockRef{entryBlock, compareEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}

func (s *functionState) emitStringViewLiteralBytesEqual(viewData C.LLVMValueRef, literalText string) (C.LLVMValueRef, error) {
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	byteType := C.LLVMInt8TypeInContext(s.g.context)
	indexType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	result := C.LLVMConstInt(boolType, 1, 0)
	for i, b := range []byte(literalText) {
		indexValue := C.LLVMConstInt(indexType, C.ulonglong(i), 0)
		indices := []C.LLVMValueRef{indexValue}
		bytePtr := C.LLVMBuildGEP2(s.builder, byteType, viewData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("svlit.byte.ptr"))
		byteValue := C.LLVMBuildLoad2(s.builder, byteType, bytePtr, cStringFree("svlit.byte"))
		literalValue := C.LLVMConstInt(byteType, C.ulonglong(b), 0)
		byteEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteValue, literalValue, cStringFree("svlit.byte.eq"))
		result = C.LLVMBuildAnd(s.builder, result, byteEqual, cStringFree("svlit.bytes.and"))
	}
	return result, nil
}

func (s *functionState) emitMemcmpEqual(left C.LLVMValueRef, right C.LLVMValueRef, length int) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	lengthValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(length), 0)
	return s.emitMemcmpEqualValue(left, right, lengthValue, "svlit.memcmp", false)
}

func (s *functionState) emitMemcmpEqualValue(left C.LLVMValueRef, right C.LLVMValueRef, lengthValue C.LLVMValueRef, callName string, noAliasArgs bool) (C.LLVMValueRef, error) {
	voidType := s.g.result.NamedTypes["void"]
	usizeType := s.g.result.NamedTypes["usize"]
	intType := s.g.result.NamedTypes["int"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{
		Name:   "memcmp",
		Params: []semantic.Type{voidRefType, voidRefType, usizeType},
		Return: intType,
	}
	callee, err := s.g.ensureFunctionDeclared("memcmp", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{left, right, lengthValue}, callName)
	if noAliasArgs {
		s.addCallSiteEnumAttribute(call, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(call, C.uint(2), "noalias")
	}
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(intLLVMType, 0, 0)
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), call, zero, cStringFree(callName+".eq")), nil
}

func (s *functionState) staticCStringLiteral(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.StringLit:
		return n.Value, true
	case *ast.ParenExpr:
		return s.staticCStringLiteral(n.Inner)
	case *ast.CastExpr:
		return s.staticCStringLiteral(n.Operand)
	case *ast.Ident:
		value, ok := s.g.constValue(n.Name)
		if !ok || value.Kind != semantic.ConstString {
			return "", false
		}
		return value.String, true
	default:
		return "", false
	}
}

func (s *functionState) emitLogicalExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	left, _, err := s.emitExpr(expr.Left, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	rhsName := cString("logic.rhs")
	defer C.free(unsafe.Pointer(rhsName))
	rhsBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, rhsName)
	mergeName := cString("logic.end")
	defer C.free(unsafe.Pointer(mergeName))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, mergeName)

	if expr.Op == lexer.TOKEN_AND {
		C.LLVMBuildCondBr(s.builder, left, rhsBB, mergeBB)
	} else {
		C.LLVMBuildCondBr(s.builder, left, mergeBB, rhsBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, rhsBB)
	right, _, err := s.emitExpr(expr.Right, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	rhsEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(rhsEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("logicphi"))
	fallback := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
	if expr.Op == lexer.TOKEN_OR {
		fallback = C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
	}
	values := []C.LLVMValueRef{fallback, right}
	blocks := []C.LLVMBasicBlockRef{parentBlock, rhsEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitUnaryExpr(expr *ast.UnaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	operandType := s.exprType(expr.Operand)
	value, _, err := s.emitExpr(expr.Operand, operandType)
	if err != nil {
		return nil, nil, err
	}
	resultType := s.exprType(expr)
	switch expr.Op {
	case lexer.TOKEN_NOT:
		return C.LLVMBuildNot(s.builder, value, cStringFree("nottmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		if isFloatType(operandType) {
			return C.LLVMBuildFNeg(s.builder, value, cStringFree("negtmp")), resultType, nil
		}
		return C.LLVMBuildNeg(s.builder, value, cStringFree("negtmp")), resultType, nil
	case lexer.TOKEN_TILDE:
		return C.LLVMBuildNot(s.builder, value, cStringFree("invt")), resultType, nil
	default:
		return nil, nil, fmt.Errorf("unsupported unary operator %s", lexer.TokenName(expr.Op))
	}
}

func (s *functionState) emitAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Owner == nil {
		return s.emitScopedPackedAllocExpr(expr)
	}
	if _, ok := s.exprType(expr.Owner).(*semantic.PackedEnumStoreType); ok {
		return s.emitPackedAllocExpr(expr)
	}
	ownerIdent, ok := expr.Owner.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("only region-backed new[...] is lowered so far")
	}
	binding, ok := s.lookupBinding(ownerIdent.Name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown region %q during LLVM lowering", ownerIdent.Name)
	}
	valueType := s.exprType(expr.Value)
	if valueType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for region allocation value in %q", ownerIdent.Name)
	}
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	sizeBytes, err := s.sizeOfType(valueType)
	if err != nil {
		return nil, nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
	arenaRefType := &semantic.RefType{Elem: binding.typ, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{binding.ptr, sizeValue}, "region.alloc")
	C.LLVMBuildStore(s.builder, value, allocPtr)
	return allocPtr, s.exprType(expr), nil
}

func (s *functionState) emitScopedPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.Value.(type) {
	case *ast.FieldExpr:
		enumType, variant, ok := s.enumConstructorInfoFromField(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(store.value, enumType, variant, nil, nil)
	case *ast.CallExpr:
		enumType, variant, ok := s.enumConstructorInfo(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(store.value, enumType, variant, n.Args, n.ArgNames)
	default:
		return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
	}
}

func (s *functionState) emitPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	storeValue, _, err := s.emitExpr(expr.Owner, nil)
	if err != nil {
		return nil, nil, err
	}
	if fieldExpr, ok := expr.Value.(*ast.FieldExpr); ok {
		enumType, variant, ok := s.enumConstructorInfoFromField(fieldExpr)
		if ok && enumType != nil && variant != nil && enumType.Packed && len(variant.Payload) == 0 {
			return s.emitPackedEnumConstructorAlloc(storeValue, enumType, variant, nil, nil)
		}
	}
	callExpr, ok := expr.Value.(*ast.CallExpr)
	if !ok {
		return nil, nil, fmt.Errorf("packed enum allocation expects a constructor call")
	}
	enumType, variant, ok := s.enumConstructorInfo(callExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum allocation expects a packed enum constructor call")
	}
	return s.emitPackedEnumConstructorAlloc(storeValue, enumType, variant, callExpr.Args, callExpr.ArgNames)
}

func (s *functionState) nodeTableFillTypeArgs(expr *ast.CallExpr) (*semantic.EnumType, semantic.Type, error) {
	if expr == nil || callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, fmt.Errorf("node_table_fill expects explicit specialization")
	}
	_, specialize, ok := callSpecializedIdent(expr.Func)
	if !ok || specialize == nil || len(specialize.TypeArgs) != 2 {
		return nil, nil, fmt.Errorf("node_table_fill expects exactly 2 type arguments")
	}
	enumArg, err := s.resolveTypeExpr(specialize.TypeArgs[0])
	if err != nil {
		return nil, nil, err
	}
	enumType, ok := semantic.StripAggregateStateType(enumArg).(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("node_table_fill expects a packed enum type argument")
	}
	elemType, err := s.resolveTypeExpr(specialize.TypeArgs[1])
	if err != nil {
		return nil, nil, err
	}
	return enumType, elemType, nil
}

func (s *functionState) emitNodeKeyIndexValue(expr ast.Expr) (C.LLVMValueRef, *semantic.EnumType, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	keyType := s.exprType(expr)
	enumType, ok := semantic.NodeKeyEnumType(keyType)
	if !ok || enumType == nil {
		return nil, nil, false, nil
	}
	value, actualType, err := s.emitExpr(expr, keyType)
	if err != nil {
		return nil, nil, true, err
	}
	if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
		loaded, loadErr := s.loadValue(value, refType.Elem, "nodekey.load")
		if loadErr != nil {
			return nil, nil, true, loadErr
		}
		value = loaded
	}
	return C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("nodekey.index")), enumType, true, nil
}

func (s *functionState) emitDenseKeyHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callIdentName(expr) != "dense_key" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("dense_key expects 2 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	storeValue, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("dense_key requires frozen packed-store metadata")
	}
	var handleValue C.LLVMValueRef
	actualNodeType := s.exprType(expr.Args[0])
	sourceEnum, ok := denseKeySourceEnumType(actualNodeType)
	if !ok || sourceEnum == nil {
		return nil, nil, true, fmt.Errorf("dense_key expects a packed enum value or packedview")
	}
	if viewType, ok := actualNodeType.(*semantic.PackedVariantViewType); ok && viewType != nil {
		viewValue, _, err := s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		handleValue = C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("nodekey.view.handle"))
	} else {
		var actualType semantic.Type
		handleValue, actualType, err = s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
			handleValue, err = s.loadValue(handleValue, refType.Elem, "nodekey.handle")
			if err != nil {
				return nil, nil, true, err
			}
		}
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
	var indexValue C.LLVMValueRef
	switch s.g.packedModeForEnum(sourceEnum) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		indexValue, err = s.coerceValue(handleValue, sourceEnum, s.g.result.NamedTypes["u32"])
		if err != nil {
			return nil, nil, true, err
		}
	case packedEnumABIRowHandle:
		rowPtr, err := s.coerceValue(handleValue, sourceEnum, ops.voidRefType())
		if err != nil {
			return nil, nil, true, err
		}
		indexValue, err = ops.encodeDenseIndex(rowPtr, "nodekey.encode_index")
		if err != nil {
			return nil, nil, true, err
		}
	case packedEnumABIWordHandle:
		rowPtr, err := ops.decodeHandle(handleValue, sourceEnum, "nodekey.decode")
		if err != nil {
			return nil, nil, true, err
		}
		indexValue, err = ops.encodeDenseIndex(rowPtr, "nodekey.encode_index")
		if err != nil {
			return nil, nil, true, err
		}
	default:
		return nil, nil, true, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedModeForEnum(sourceEnum))
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, indexValue, 0, cStringFree("nodekey.index.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitNodeTableFillHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 {
		return nil, nil, true, fmt.Errorf("node_table_fill expects 3 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	_, elemType, err := s.nodeTableFillTypeArgs(expr)
	if err != nil {
		return nil, nil, true, err
	}
	storeValue, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("node_table_fill requires frozen packed-store metadata")
	}
	countValue, err := s.emitPackedStoreCountValue(storeValue, storeType, "node.table.count")
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroCount := C.LLVMConstInt(usizeLLVMType, 0, 0)
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.merge"))
	isZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, zeroCount, cStringFree("node.table.count.zero"))
	C.LLVMBuildCondBr(s.builder, isZero, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	arenaPtr, _, err := s.emitAddressOrTemp(expr.Args[0])
	if err != nil {
		return nil, nil, true, err
	}
	elemSize, err := s.sizeOfType(elemType)
	if err != nil {
		return nil, nil, true, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	byteCount := C.LLVMBuildMul(s.builder, countValue, elemSizeValue, cStringFree("node.table.bytes"))
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaPtr, byteCount}, "node.table.alloc.ptr")
	viewType := &semantic.DArrayViewType{Elem: elemType, SurfaceName: "dview"}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, nil, true, err
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, allocPtr, 0, cStringFree("node.table.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, countValue, 1, cStringFree("node.table.view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, elemSizeValue, 2, cStringFree("node.table.view.elem_size"))
	initValue, actualInitType, err := s.emitExpr(expr.Args[2], elemType)
	if err != nil {
		return nil, nil, true, err
	}
	initValue, err = s.coerceValue(initValue, actualInitType, elemType)
	if err != nil {
		return nil, nil, true, err
	}
	fillType := &semantic.FuncType{Name: "arena_da_fill", Params: []semantic.Type{viewType, elemType}, Return: s.g.result.NamedTypes["void"]}
	fillCallee, err := s.g.ensureFunctionDeclared("arena_da_fill", fillType)
	if err != nil {
		return nil, nil, true, err
	}
	fillLLVMType, err := s.g.lowerFunctionType(fillType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(fillLLVMType, fillCallee, []C.LLVMValueRef{viewValue, initValue}, "")
	materialized := C.LLVMGetUndef(resultLLVMType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewValue, 0, cStringFree("node.table.values.insert"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	zeroResult := C.LLVMConstNull(resultLLVMType)
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("node.table.result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}

func (s *functionState) emitCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
	if storeType, ok := s.packedStoreConstructorCall(expr); ok {
		return s.emitPackedStoreConstructorValue(expr, storeType)
	}
	if callIdentName(expr) == "freeze" {
		if len(expr.Args) != 1 {
			return nil, nil, fmt.Errorf("freeze expects 1 argument, got %d", len(expr.Args))
		}
		frozenType := s.exprType(expr)
		return s.emitExpr(expr.Args[0], frozenType)
	}
	if value, actualType, handled, err := s.emitDenseKeyHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitNodeTableFillHelperCall(expr); handled {
		return value, actualType, err
	}
	if enumType, variant, ok := s.enumConstructorInfo(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor")
		}
		return s.emitEnumConstructorValue(enumType, variant, expr.Args, expr.ArgNames)
	}
	if value, actualType, handled, err := s.emitProofCarryingViewHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedRuntimeCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSliceCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringViewCopyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewCopyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewEqCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaFromViewCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewFillCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedMemcpyCall(expr); handled {
		return value, actualType, err
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, err
	}
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, err
	}
	args := make([]C.LLVMValueRef, 0, len(expr.Args))
	for i, arg := range expr.Args {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, value)
	}
	if retUnion, ok := nonVoidErrorUnion(funcType.Return); ok {
		resultSlot, err := s.emitStackTempZeroed(retUnion.Value, "call.result")
		if err != nil {
			return nil, nil, err
		}
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		call := s.buildCall(llvmFnType, callee, callArgs, "calltmp")
		payload, err := s.loadValue(resultSlot, retUnion.Value, "call.payload")
		if err != nil {
			return nil, nil, err
		}
		unionValue, err := s.buildErrorUnionValue(retUnion, call, payload)
		if err != nil {
			return nil, nil, err
		}
		return unionValue, funcType.Return, nil
	}
	callName := ""
	if !isVoidType(funcType.Return) {
		callName = "calltmp"
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	return call, funcType.Return, nil
}

func (s *functionState) emitProofCarryingViewHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	switch callIdentName(expr) {
	case "readonly":
		return s.emitReadonlyHelperCall(expr)
	case "split_at":
		return s.emitSplitAtHelperCall(expr)
	case "chunks_exact":
		return s.emitChunksExactHelperCall(expr)
	case "reduce_sum":
		return s.emitReduceSumHelperCall(expr)
	case "zip_map":
		return s.emitZipMapHelperCall(expr)
	default:
		return nil, nil, false, nil
	}
}

func (s *functionState) emitReadonlyHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("readonly expects 1 argument, got %d", len(expr.Args))
	}
	value, actualType, err := s.emitExpr(expr.Args[0], s.exprType(expr.Args[0]))
	if err != nil {
		return nil, nil, true, err
	}
	return value, actualType, true, nil
}

func (s *functionState) emitSplitAtHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("split_at expects 2 arguments, got %d", len(expr.Args))
	}
	viewType, ok := s.exprType(expr.Args[0]).(*semantic.DArrayViewType)
	if !ok || viewType == nil {
		return nil, nil, true, fmt.Errorf("split_at expects a dview source")
	}
	resultType := s.exprType(expr)
	viewValue, _, err := s.emitExpr(expr.Args[0], viewType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	indexValue, _, err := s.emitExpr(expr.Args[1], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("split.view.len"))
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	leftValue, err := s.emitArenaViewSliceValue(viewValue, viewType, zero, indexValue, "split.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightValue, err := s.emitArenaViewSliceValue(viewValue, viewType, indexValue, viewLen, "split.right")
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, leftValue, 0, cStringFree("split.left.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, rightValue, 1, cStringFree("split.right.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitChunksExactHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("chunks_exact expects 2 arguments, got %d", len(expr.Args))
	}
	viewType, ok := s.exprType(expr.Args[0]).(*semantic.DArrayViewType)
	if !ok || viewType == nil {
		return nil, nil, true, fmt.Errorf("chunks_exact expects a dview source")
	}
	resultType := s.exprType(expr)
	viewValue, _, err := s.emitExpr(expr.Args[0], viewType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	chunkSizeValue, _, err := s.emitExpr(expr.Args[1], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("chunks.view.len"))
	if err := s.emitChunksExactValidation(chunkSizeValue, viewLen, "chunks_exact"); err != nil {
		return nil, nil, true, err
	}
	chunksLen := C.LLVMBuildUDiv(s.builder, viewLen, chunkSizeValue, cStringFree("chunks.len"))
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, viewValue, 0, cStringFree("chunks.source.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, chunkSizeValue, 1, cStringFree("chunks.chunk_size.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, chunksLen, 2, cStringFree("chunks.len.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitReduceSumHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) < 2 {
		return nil, nil, true, fmt.Errorf("reduce_sum expects at least 2 arguments, got %d", len(expr.Args))
	}
	srcType, srcElemType, ok := zipMapViewInfo(s.exprType(expr.Args[0]))
	if !ok {
		return nil, nil, true, fmt.Errorf("reduce_sum source expects a dense view")
	}
	callbackType, ok := s.exprType(expr.Args[1]).(*semantic.FuncType)
	if !ok || callbackType == nil {
		return nil, nil, true, fmt.Errorf("reduce_sum callback expects a function value")
	}
	resultType := s.exprType(expr)
	srcValue, _, err := s.emitExpr(expr.Args[0], srcType)
	if err != nil {
		return nil, nil, true, err
	}
	callbackValue, _, err := s.emitExpr(expr.Args[1], callbackType)
	if err != nil {
		return nil, nil, true, err
	}
	extraArgs := make([]C.LLVMValueRef, 0, len(expr.Args)-2)
	for i, arg := range expr.Args[2:] {
		var expected semantic.Type
		if i+1 < len(callbackType.Params) {
			expected = callbackType.Params[i+1]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, true, err
		}
		extraArgs = append(extraArgs, value)
	}
	srcPtr, err := s.emitStackTempValue(srcValue, srcType, "reduce_sum.src")
	if err != nil {
		return nil, nil, true, err
	}
	totalValue := C.LLVMBuildExtractValue(s.builder, srcValue, 1, cStringFree("reduce_sum.total"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	indexAlloca, err := s.createEntryAlloca("reduce_sum.index", usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	C.LLVMBuildStore(s.builder, zeroIndex, indexAlloca)
	accZero, err := s.zeroValue(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	accAlloca, err := s.createEntryAlloca("reduce_sum.acc", resultType)
	if err != nil {
		return nil, nil, true, err
	}
	C.LLVMBuildStore(s.builder, accZero, accAlloca)

	loopCondBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.cond"))
	loopBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.body"))
	loopEndBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.end"))
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue, err := s.loadValue(indexAlloca, usizeType, "reduce_sum.index")
	if err != nil {
		return nil, nil, true, err
	}
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, totalValue, cStringFree("reduce_sum.has_more"))
	C.LLVMBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBodyBB)
	srcElemPtr, _, err := s.emitRuntimeIndexedAddress(srcPtr, srcType, srcElemType, indexValue)
	if err != nil {
		return nil, nil, true, err
	}
	srcElem, err := s.loadValue(srcElemPtr, srcElemType, "reduce_sum.src.elem")
	if err != nil {
		return nil, nil, true, err
	}
	callbackLLVMType, err := s.g.lowerFunctionType(callbackType)
	if err != nil {
		return nil, nil, true, err
	}
	callArgs := make([]C.LLVMValueRef, 0, len(extraArgs)+1)
	callArgs = append(callArgs, srcElem)
	callArgs = append(callArgs, extraArgs...)
	mappedValue := s.buildCall(callbackLLVMType, callbackValue, callArgs, "reduce_sum.call")
	coercedValue, err := s.coerceValue(mappedValue, callbackType.Return, resultType)
	if err != nil {
		return nil, nil, true, err
	}
	accValue, err := s.loadValue(accAlloca, resultType, "reduce_sum.acc")
	if err != nil {
		return nil, nil, true, err
	}
	nextAcc, err := s.emitAugmentedValue(lexer.TOKEN_PLUSEQ, accValue, coercedValue, resultType)
	if err != nil {
		return nil, nil, true, err
	}
	C.LLVMBuildStore(s.builder, nextAcc, accAlloca)
	nextIndex := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree("reduce_sum.index.next"))
	C.LLVMBuildStore(s.builder, nextIndex, indexAlloca)
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopEndBB)
	resultValue, err := s.loadValue(accAlloca, resultType, "reduce_sum.result")
	if err != nil {
		return nil, nil, true, err
	}
	return resultValue, resultType, true, nil
}

func (s *functionState) emitZipMapHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 4 {
		return nil, nil, true, fmt.Errorf("zip_map expects 4 arguments, got %d", len(expr.Args))
	}
	dstType, dstElemType, ok := zipMapViewInfo(s.exprType(expr.Args[0]))
	if !ok {
		return nil, nil, true, fmt.Errorf("zip_map destination expects a dense view")
	}
	src1Type, src1ElemType, ok := zipMapViewInfo(s.exprType(expr.Args[1]))
	if !ok {
		return nil, nil, true, fmt.Errorf("zip_map source 1 expects a dense view")
	}
	src2Type, src2ElemType, ok := zipMapViewInfo(s.exprType(expr.Args[2]))
	if !ok {
		return nil, nil, true, fmt.Errorf("zip_map source 2 expects a dense view")
	}
	callbackType, ok := s.exprType(expr.Args[3]).(*semantic.FuncType)
	if !ok || callbackType == nil {
		return nil, nil, true, fmt.Errorf("zip_map callback expects a function value")
	}
	dstValue, _, err := s.emitExpr(expr.Args[0], dstType)
	if err != nil {
		return nil, nil, true, err
	}
	src1Value, _, err := s.emitExpr(expr.Args[1], src1Type)
	if err != nil {
		return nil, nil, true, err
	}
	src2Value, _, err := s.emitExpr(expr.Args[2], src2Type)
	if err != nil {
		return nil, nil, true, err
	}
	callbackValue, _, err := s.emitExpr(expr.Args[3], callbackType)
	if err != nil {
		return nil, nil, true, err
	}
	dstPtr, err := s.emitStackTempValue(dstValue, dstType, "zip_map.dst")
	if err != nil {
		return nil, nil, true, err
	}
	src1Ptr, err := s.emitStackTempValue(src1Value, src1Type, "zip_map.src1")
	if err != nil {
		return nil, nil, true, err
	}
	src2Ptr, err := s.emitStackTempValue(src2Value, src2Type, "zip_map.src2")
	if err != nil {
		return nil, nil, true, err
	}
	totalValue := C.LLVMBuildExtractValue(s.builder, dstValue, 1, cStringFree("zip_map.total"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	indexAlloca, err := s.createEntryAlloca("zip_map.index", usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	C.LLVMBuildStore(s.builder, zero, indexAlloca)

	loopCondBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.cond"))
	loopBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.body"))
	loopEndBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.end"))
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue, err := s.loadValue(indexAlloca, usizeType, "zip_map.index")
	if err != nil {
		return nil, nil, true, err
	}
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, totalValue, cStringFree("zip_map.has_more"))
	C.LLVMBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBodyBB)
	dstElemPtr, _, err := s.emitRuntimeIndexedAddress(dstPtr, dstType, dstElemType, indexValue)
	if err != nil {
		return nil, nil, true, err
	}
	src1ElemPtr, _, err := s.emitRuntimeIndexedAddress(src1Ptr, src1Type, src1ElemType, indexValue)
	if err != nil {
		return nil, nil, true, err
	}
	src2ElemPtr, _, err := s.emitRuntimeIndexedAddress(src2Ptr, src2Type, src2ElemType, indexValue)
	if err != nil {
		return nil, nil, true, err
	}
	src1Elem, err := s.loadValue(src1ElemPtr, src1ElemType, "zip_map.src1.elem")
	if err != nil {
		return nil, nil, true, err
	}
	src2Elem, err := s.loadValue(src2ElemPtr, src2ElemType, "zip_map.src2.elem")
	if err != nil {
		return nil, nil, true, err
	}
	callbackLLVMType, err := s.g.lowerFunctionType(callbackType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := s.buildCall(callbackLLVMType, callbackValue, []C.LLVMValueRef{src1Elem, src2Elem}, "zip_map.call")
	coerced, err := s.coerceValue(resultValue, callbackType.Return, dstElemType)
	if err != nil {
		return nil, nil, true, err
	}
	C.LLVMBuildStore(s.builder, coerced, dstElemPtr)
	nextIndex := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree("zip_map.index.next"))
	C.LLVMBuildStore(s.builder, nextIndex, indexAlloca)
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopEndBB)
	return nil, s.g.result.NamedTypes["void"], true, nil
}

func zipMapViewInfo(t semantic.Type) (semantic.Type, semantic.Type, bool) {
	switch tt := t.(type) {
	case *semantic.ViewType:
		if tt == nil {
			return nil, nil, false
		}
		return tt, tt.Elem, true
	case *semantic.DArrayViewType:
		if tt == nil || tt.SurfaceName == "packedtags" {
			return nil, nil, false
		}
		return tt, tt.Elem, true
	default:
		return nil, nil, false
	}
}

func (s *functionState) emitArenaViewSliceValue(viewValue C.LLVMValueRef, viewType *semantic.DArrayViewType, startValue C.LLVMValueRef, endValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if viewType == nil {
		return nil, fmt.Errorf("missing dview type for slice helper lowering")
	}
	usizeType := s.g.result.NamedTypes["usize"]
	helperType := &semantic.FuncType{
		Name:   "arena_da_view_slice",
		Params: []semantic.Type{viewType, usizeType, usizeType},
		Return: viewType,
	}
	callee, err := s.g.ensureFunctionDeclared("arena_da_view_slice", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{viewValue, startValue, endValue}, name), nil
}

func (s *functionState) emitChunksExactValidation(chunkSizeValue C.LLVMValueRef, totalValue C.LLVMValueRef, prefix string) error {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	nonZeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".nonzero"))
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".fail"))
	isNonZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), chunkSizeValue, zero, cStringFree(prefix+".chunk.nonzero"))
	C.LLVMBuildCondBr(s.builder, isNonZero, nonZeroBB, failBB)

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	trapFn, err := s.ensureTrapFunction()
	if err != nil {
		return err
	}
	trapType, err := s.g.lowerFunctionType(&semantic.FuncType{Name: "llvm.trap", Return: s.g.result.NamedTypes["void"]})
	if err != nil {
		return err
	}
	s.buildCall(trapType, trapFn, nil, "")
	C.LLVMBuildUnreachable(s.builder)

	C.LLVMPositionBuilderAtEnd(s.builder, nonZeroBB)
	remainder := C.LLVMBuildURem(s.builder, totalValue, chunkSizeValue, cStringFree(prefix+".remainder"))
	isExact := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), remainder, zero, cStringFree(prefix+".exact"))
	C.LLVMBuildCondBr(s.builder, isExact, okBB, failBB)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	return nil
}

func (s *functionState) emitPackedStoreConstructorValue(expr *ast.CallExpr, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || len(expr.Args) != 1 {
		return nil, nil, fmt.Errorf("packed store constructor expects exactly one arena argument")
	}
	value, err := s.emitPackedStoreValue(expr.Args[0], storeType)
	if err != nil {
		return nil, nil, err
	}
	return value, storeType, nil
}

func (s *functionState) emitPackedStoreValue(arenaExpr ast.Expr, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	arenaPtr, _, err := s.emitAddressOrTemp(arenaExpr)
	if err != nil {
		return nil, err
	}
	if storeType.Enum == nil {
		return nil, fmt.Errorf("packed enum store %s is missing enum metadata", storeType.Name)
	}
	rowType, err := s.g.ensurePackedEnumStorageType(storeType.Enum)
	if err != nil {
		return nil, err
	}
	rowSizeBytes, err := s.g.abiSizeOfLLVMType(rowType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	rowSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(rowSizeBytes), 0)
	storeLLVMType, err := s.g.lowerPackedEnumStoreType(storeType)
	if err != nil {
		return nil, err
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	stateHelperName := "ctx_packed_store_state_new"
	stateHelperParams := []semantic.Type{arenaRefType, usizeType}
	stateArgs := []C.LLVMValueRef{arenaPtr, rowSizeValue}
	if s.g.packedLoweringForStore(storeType) == packedEnumABIVariantSparse {
		stateHelperName = "ctx_packed_store_state_new_variant_sparse"
	} else if storeType.Enum != nil && storeType.Enum.HasPackedPrefixOverride && storeType.Enum.PackedPrefixOverride == "common-only" && s.g.packedLoweringForStore(storeType) == packedEnumABIIndexSOA {
		prefixWords, err := s.g.packedEnumCommonPrefixWordCount(storeType.Enum)
		if err != nil {
			return nil, err
		}
		stateHelperName = "ctx_packed_store_state_new_with_prefix_words"
		stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
		stateArgs = append(stateArgs, C.LLVMConstInt(usizeLLVMType, C.ulonglong(prefixWords), 0))
	}
	stateHelperType := &semantic.FuncType{Name: stateHelperName, Params: stateHelperParams, Return: voidRefType}
	stateCallee, err := s.g.ensureFunctionDeclared(stateHelperName, stateHelperType)
	if err != nil {
		return nil, err
	}
	stateLLVMFnType, err := s.g.lowerFunctionType(stateHelperType)
	if err != nil {
		return nil, err
	}
	stateValue := s.buildCall(stateLLVMFnType, stateCallee, stateArgs, "packed.store.state")
	storeValue := C.LLVMGetUndef(storeLLVMType)
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, arenaPtr, 0, cStringFree("packed.store.arena"))
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, rowSizeValue, 1, cStringFree("packed.store.row_bytes"))
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, stateValue, 2, cStringFree("packed.store.state"))
	return storeValue, nil
}

func (s *functionState) emitPackedStoreArenaValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	return s.emitPackedStoreArenaValueNamed(storeValue, storeType, "packed.store.arena.value")
}

func (s *functionState) emitPackedStoreFieldValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, index C.unsigned, name string) (C.LLVMValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	_, err := s.g.lowerPackedEnumStoreType(storeType)
	if err != nil {
		return nil, err
	}
	if block := C.LLVMGetInsertBlock(s.builder); block != nil && s.packedStoreValues != nil && storeValue != nil {
		key := packedStoreExtractCacheKey{block: block, store: storeValue, index: index}
		if cached, ok := s.packedStoreValues[key]; ok && cached != nil {
			return cached, nil
		}
		value := C.LLVMBuildExtractValue(s.builder, storeValue, index, cStringFree(name))
		s.packedStoreValues[key] = value
		return value, nil
	}
	return C.LLVMBuildExtractValue(s.builder, storeValue, index, cStringFree(name)), nil
}

func (s *functionState) emitPackedStoreArenaValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 0, name)
}

func (s *functionState) emitPackedStoreRowBytesValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	return s.emitPackedStoreRowBytesValueNamed(storeValue, storeType, "packed.store.row_bytes.value")
}

func (s *functionState) emitPackedStoreRowBytesValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 1, name)
}

func (s *functionState) emitPackedStoreStateValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 2, name)
}

func packedStoreOperandType(t semantic.Type) (*semantic.PackedEnumStoreType, bool) {
	if storeType, ok := t.(*semantic.PackedEnumStoreType); ok {
		return storeType, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, false
	}
	storeType, ok := refType.Elem.(*semantic.PackedEnumStoreType)
	return storeType, ok
}

func (s *functionState) emitPackedStoreValueFromExpr(expr ast.Expr) (C.LLVMValueRef, *semantic.PackedEnumStoreType, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("missing packed store expression")
	}
	objectType := s.exprType(expr)
	if objectType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for packed store expression")
	}
	if storeType, ok := objectType.(*semantic.PackedEnumStoreType); ok {
		value, _, err := s.emitExpr(expr, storeType)
		if err != nil {
			return nil, nil, err
		}
		return value, storeType, nil
	}
	refType, ok := objectType.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, nil, fmt.Errorf("packed store access requires a store value or proven non-null store reference")
	}
	storeType, ok := refType.Elem.(*semantic.PackedEnumStoreType)
	if !ok {
		return nil, nil, fmt.Errorf("packed store access requires a packed store, got %s", objectType.String())
	}
	ptrValue, _, err := s.emitExpr(expr, objectType)
	if err != nil {
		return nil, nil, err
	}
	storeValue, err := s.loadValue(ptrValue, storeType, "packed.store.load")
	if err != nil {
		return nil, nil, err
	}
	return storeValue, storeType, nil
}

func (s *functionState) resolveCallTarget(expr *ast.CallExpr) (C.LLVMValueRef, *semantic.FuncType, error) {
	if ident, ok := expr.Func.(*ast.Ident); ok {
		if sym, ok := s.g.result.GlobalScope.Lookup(ident.Name); ok {
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok {
				return nil, nil, fmt.Errorf("call target %s does not resolve to a function type", ident.Name)
			}
			if decl, ok := sym.Node.(*ast.FuncDecl); ok && len(decl.GenericParams) > 0 {
				argTypes := make([]semantic.Type, 0, len(expr.Args))
				for _, arg := range expr.Args {
					argTypes = append(argTypes, s.exprType(arg))
				}
				bindings := inferTypeBindingsFromCall(fnType, expr.Args, argTypes)
				value, specialized, err := s.g.ensureSpecializedFunction(decl, fnType, bindings)
				return value, specialized, err
			}
			value, err := s.g.ensureFunctionDeclared(ident.Name, specializeFuncType(fnType, s.typeMap))
			return value, specializeFuncType(fnType, s.typeMap), err
		}
	}
	calleeType, ok := s.exprType(expr.Func).(*semantic.FuncType)
	if !ok {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	callee, _, err := s.emitExpr(expr.Func, nil)
	return callee, calleeType, err
}

func (s *functionState) emitFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if enumType, variant, ok := s.enumConstructorInfoFromField(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor %s.%s", enumType.Name, expr.Field)
		}
		if len(variant.Payload) == 0 {
			return s.emitEnumConstructorValue(enumType, variant, nil, nil)
		}
	}
	if fieldType, ok := dstrSyntheticFieldType(s.exprType(expr.Object), expr.Field); ok {
		return s.emitRuntimeStringLenExpr(expr.Object, fieldType)
	}
	if value, fieldType, handled, err := s.emitPackedStoreCountExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitPackedStoreTagsExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitPackedVariantViewFieldExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitPackedCommonFieldExpr(expr); handled {
		return value, fieldType, err
	}
	ptr, fieldType, addressErr := s.emitReadableFieldAddress(expr)
	if addressErr == nil {
		value, loadErr := s.loadValue(ptr, fieldType, expr.Field)
		return value, fieldType, loadErr
	}
	objValue, objType, err := s.emitExpr(expr.Object, nil)
	if err != nil {
		return nil, nil, err
	}
	fieldType, index, _, pointerLike, err := s.g.fieldInfo(objType, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	if pointerLike {
		return nil, nil, fmt.Errorf("field %s requires an addressable object (base %T: %v)", expr.Field, expr.Object, addressErr)
	}
	value := C.LLVMBuildExtractValue(s.builder, objValue, C.unsigned(index), cStringFree(expr.Field))
	return value, fieldType, nil
}

func (s *functionState) emitPackedStoreCountExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Field != "count" {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	value, err := ops.storeCount("packed.store.count")
	if err != nil {
		return nil, nil, true, err
	}
	return value, s.g.result.NamedTypes["usize"], true, nil
}

func (s *functionState) emitPackedStoreCountValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return (&packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}).storeCount(name)
}

func (s *functionState) emitPackedStoreTagsExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Field != "tags" {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok || ops.storeType == nil || ops.storeType.Enum == nil || !semantic.IsFrozenPackedEnumStoreType(ops.storeType) {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.DArrayViewType)
	if !ok || resultType == nil {
		return nil, nil, true, fmt.Errorf("packed store tags requires dview result type")
	}
	totalValue, err := ops.storeCount("packed.store.tags.count")
	if err != nil {
		return nil, nil, true, err
	}
	zeroType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	zeroValue := C.LLVMConstInt(zeroType, 0, 0)
	value, actualType, err := ops.storeTagsView(zeroValue, totalValue, resultType, "packed.store.tags")
	return value, actualType, true, err
}

func (s *functionState) emitPackedVariantViewFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	name, hasName := packedVariantViewName(expr.Object)
	binding, ok := s.lookupPackedVariantView(name)
	if !ok {
		viewType, ok := s.exprType(expr.Object).(*semantic.PackedVariantViewType)
		if !ok || viewType == nil {
			return nil, nil, false, nil
		}
		objectValue, _, err := s.emitExpr(expr.Object, viewType)
		if err != nil {
			return nil, nil, true, err
		}
		binding, err = s.unpackPackedVariantViewValue(objectValue, viewType)
		if err != nil {
			return nil, nil, true, err
		}
		if hasName {
			var storeCopy *packedStoreBinding
			if binding.store.typ != nil {
				copied := binding.store
				storeCopy = &copied
			}
			s.bindPackedVariantView(name, viewType, binding.ptr, binding.handle, storeCopy)
		}
	}
	if binding.typ == nil || (binding.ptr == nil && binding.handle == nil) {
		return nil, nil, false, nil
	}
	field, ok := binding.typ.Field(expr.Field)
	if !ok {
		return nil, nil, true, fmt.Errorf("%s has no field %s", binding.typ.String(), expr.Field)
	}
	if _, isCommonField := binding.typ.Enum.Common[expr.Field]; isCommonField {
		fieldType, fieldIndex, _, _, err := s.g.fieldInfo(binding.typ.Enum, expr.Field)
		if err != nil {
			return nil, nil, true, err
		}
		if binding.ptr == nil && binding.handle != nil && binding.store.typ != nil {
			ops, ok := s.packedStoreOpsFromBinding(&binding.store)
			if ok && ops.canDirectWordRead() {
				fieldWordOffset, ok, err := s.packedEnumDirectWordFieldOffset(binding.typ.Enum, fieldIndex, fieldType)
				if err != nil {
					return nil, nil, true, err
				}
				if ok {
					wordValue, err := ops.loadPayloadWord(binding.handle, binding.typ.Enum, fieldWordOffset, "packed.view.common")
					if err != nil {
						return nil, nil, true, err
					}
					coerced, err := s.coerceValue(wordValue, s.g.result.NamedTypes["uintptr"], fieldType)
					if err != nil {
						return nil, nil, true, err
					}
					return coerced, fieldType, true, nil
				}
			}
			decodedPtr, err := s.decodePackedEnumHandleWithStore(binding.handle, binding.typ.Enum, &binding.store)
			if err != nil {
				return nil, nil, true, err
			}
			binding.ptr = decodedPtr
			if hasName {
				s.updatePackedVariantViewDecodedPtr(name, decodedPtr)
			}
		}
		containerType, err := s.loweredEnumStorageType(binding.typ.Enum)
		if err != nil {
			return nil, nil, true, err
		}
		fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerType, binding.ptr, C.unsigned(fieldIndex), cStringFree("view.common.field"))
		value, err := s.loadValue(fieldPtr, fieldType, expr.Field)
		return value, fieldType, true, err
	}
	if binding.ptr == nil && binding.handle != nil && binding.store.typ != nil {
		ops, ok := s.packedStoreOpsFromBinding(&binding.store)
		if ok && ops.canDirectWordRead() {
			payloadValues, ok, err := s.readPackedEnumVariantPayloadWithStore(binding.handle, binding.typ.Enum, binding.typ.Variant, &binding.store)
			if err != nil {
				return nil, nil, true, err
			}
			if ok {
				index, ok := binding.typ.Variant.PayloadIndex(expr.Field)
				if !ok || index < 0 || index >= len(payloadValues) {
					return nil, nil, true, fmt.Errorf("%s has no field %s", binding.typ.String(), expr.Field)
				}
				return payloadValues[index], field.Type, true, nil
			}
		}
		decodedPtr, err := s.decodePackedEnumHandleWithStore(binding.handle, binding.typ.Enum, &binding.store)
		if err != nil {
			return nil, nil, true, err
		}
		binding.ptr = decodedPtr
		if hasName {
			s.updatePackedVariantViewDecodedPtr(name, decodedPtr)
		}
	}
	payloadValues, err := s.loadEnumVariantPayload(binding.ptr, binding.handle, binding.typ.Enum, binding.typ.Variant, nil)
	if err != nil {
		return nil, nil, true, err
	}
	index, ok := binding.typ.Variant.PayloadIndex(expr.Field)
	if !ok || index < 0 || index >= len(payloadValues) {
		return nil, nil, true, fmt.Errorf("%s has no field %s", binding.typ.String(), expr.Field)
	}
	return payloadValues[index], field.Type, true, nil
}

func packedVariantViewName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := packedVariantViewName(n.Object)
		if !ok || base == "" {
			return "", false
		}
		return base + "." + n.Field, true
	case *ast.IndexExpr:
		base, ok := packedVariantViewName(n.Object)
		if !ok || base == "" {
			return "", false
		}
		indexKey, ok := packedEnumStorageIndexKey(n.Index)
		if !ok {
			return "", false
		}
		return base + "[" + indexKey + "]", true
	case *ast.CastExpr:
		return packedVariantViewName(n.Operand)
	case *ast.CanExpr:
		return packedVariantViewName(n.Expr)
	case *ast.ParenExpr:
		return packedVariantViewName(n.Inner)
	default:
		return "", false
	}
}

func (s *functionState) emitPackedCommonFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	objectType := s.exprType(expr.Object)
	if objectType == nil {
		return nil, nil, false, nil
	}
	fieldType, fieldIndex, containerType, _, err := s.g.fieldInfo(objectType, expr.Field)
	if err != nil {
		return nil, nil, false, nil
	}
	enumType, ok := containerType.(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, false, nil
	}
	if key, ok := s.packedEnumStoragePath(expr.Object); ok {
		if _, ok := s.lookupPackedEnumStorage(key, enumType); ok {
			return nil, nil, false, nil
		}
	}
	store, ok := s.lookupPackedStore(enumType)
	if !ok {
		return nil, nil, false, nil
	}
	ops, ok := s.packedStoreOpsFromBinding(&store)
	if !ok || !ops.canDirectWordRead() {
		return nil, nil, false, nil
	}
	if s.g != nil && s.g.result != nil && s.g.result.ExprHasOnlyFrozenPackedStoreDeps(expr.Object) {
		if !packedModeUsesDenseIndexHandle(s.g.packedModeForEnum(enumType)) {
			return nil, nil, false, nil
		}
	}
	fieldWordOffset, ok, err := s.packedEnumDirectWordFieldOffset(enumType, fieldIndex, fieldType)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	handleValue, err := s.packedEnumFieldHandleValue(expr.Object, objectType, enumType)
	if err != nil {
		return nil, nil, true, err
	}
	wordValue, err := ops.loadPayloadWord(handleValue, enumType, fieldWordOffset, "packed.common.store")
	if err != nil {
		return nil, nil, true, err
	}
	coerced, err := s.coerceValue(wordValue, s.g.result.NamedTypes["uintptr"], fieldType)
	if err != nil {
		return nil, nil, true, err
	}
	return coerced, fieldType, true, nil
}

func (s *functionState) emitPackedStoreValueAtDenseKey(ops *packedStoreOps, keyIndex C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	if ops == nil || ops.storeType == nil || ops.storeType.Enum == nil {
		return nil, nil, fmt.Errorf("dense-key packed store read requires store metadata")
	}
	switch s.g.packedModeForEnum(ops.storeType.Enum) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		coerced, err := s.coerceValue(keyIndex, s.g.result.NamedTypes["u32"], ops.storeType.Enum)
		if err != nil {
			return nil, nil, err
		}
		return coerced, ops.storeType.Enum, nil
	case packedEnumABIRowHandle, packedEnumABIWordHandle:
		rowPtr, err := ops.decodeDenseIndex(keyIndex, name+".decode")
		if err != nil {
			return nil, nil, err
		}
		handleValue, err := ops.encodeHandle(rowPtr, ops.storeType.Enum, name+".encode")
		if err != nil {
			return nil, nil, err
		}
		return handleValue, ops.storeType.Enum, nil
	default:
		return nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedModeForEnum(ops.storeType.Enum))
	}
}

func (s *functionState) emitPackedStoreIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	if keyIndex, _, handled, err := s.emitNodeKeyIndexValue(expr.Index); handled {
		if err != nil {
			return nil, nil, true, err
		}
		value, actualType, err := s.emitPackedStoreValueAtDenseKey(ops, keyIndex, "packed.store.key")
		return value, actualType, true, err
	}
	indexValue, _, err := s.emitExpr(expr.Index, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	value, actualType, err := ops.storeValueAt(indexValue, "packed.store.index")
	return value, actualType, true, err
}

func (s *functionState) emitPackedStoreSliceExpr(expr *ast.SliceExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	startValue, _, err := s.emitExpr(expr.Start, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	resultType := s.exprType(expr)
	value, actualType, err := ops.storeSlice(startValue, endValue, resultType, "packed.store.view")
	return value, actualType, true, err
}

func (s *functionState) packedEnumFieldHandleValue(expr ast.Expr, objectType semantic.Type, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if refType, ok := objectType.(*semantic.RefType); ok {
		if refEnum, ok := refType.Elem.(*semantic.EnumType); ok && refEnum == enumType {
			refValue, _, err := s.emitExpr(expr, objectType)
			if err != nil {
				return nil, err
			}
			return s.loadValue(refValue, enumType, "packed.common.handle")
		}
	}
	handleValue, _, err := s.emitExpr(expr, objectType)
	if err != nil {
		return nil, err
	}
	return handleValue, nil
}

func (s *functionState) packedEnumDirectWordFieldOffset(enumType *semantic.EnumType, fieldIndex int, fieldType semantic.Type) (C.LLVMValueRef, bool, error) {
	if enumType == nil || !enumType.Packed || fieldIndex <= 0 {
		return nil, false, nil
	}
	payloadIndex, err := s.g.packedEnumPayloadFieldIndex(enumType)
	if err != nil {
		return nil, false, err
	}
	if fieldIndex >= payloadIndex {
		return nil, false, nil
	}
	if !isNumericType(fieldType) {
		return nil, false, nil
	}
	wordBytes := uint64(s.g.wordBits / 8)
	fieldSizeBytes, err := s.g.abiSizeOfType(fieldType)
	if err != nil {
		return nil, false, err
	}
	if fieldSizeBytes == 0 || fieldSizeBytes > wordBytes {
		return nil, false, nil
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, false, err
	}
	offsetBytes, err := s.g.abiOffsetOfLLVMElement(rowType, fieldIndex)
	if err != nil {
		return nil, false, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, false, err
	}
	return C.LLVMConstInt(usizeType, C.ulonglong(offsetBytes/wordBytes), 0), true, nil
}

func (s *functionState) readPackedEnumWordWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding, wordOffset C.LLVMValueRef) (C.LLVMValueRef, error) {
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s word-handle common-field read requires store context", enumType.Name)
	}
	return ops.loadPayloadWord(handleValue, enumType, wordOffset, "packed.common.store")
}

func (s *functionState) packedStoreConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.PackedEnumStoreType, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, ok := s.g.result.NamedTypes[ident.Name]
	if !ok {
		return nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok || !enumType.Packed || expr.Field != "Store" || enumType.StoreType == nil {
		return nil, false
	}
	return semantic.PackedEnumStoreWithState(enumType.StoreType, s.g.result.NamedTypes["Local"]), true
}

func (s *functionState) packedStoreConstructorCall(expr *ast.CallExpr) (*semantic.PackedEnumStoreType, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	return s.packedStoreConstructorInfoFromField(fieldExpr)
}

func (s *functionState) emitSliceExpr(expr *ast.SliceExpr) (C.LLVMValueRef, semantic.Type, error) {
	if value, resultType, handled, err := s.emitPackedStoreSliceExpr(expr); handled {
		return value, resultType, err
	}
	if value, resultType, handled, err := s.emitFixedArraySliceExpr(expr); handled {
		return value, resultType, err
	}
	info, ok := runtimeSliceOperandInfo(s.exprType(expr.Object), s.exprType(expr))
	if !ok {
		return nil, nil, fmt.Errorf("slice is not implemented for %s", s.exprType(expr.Object).String())
	}
	var (
		objectValue C.LLVMValueRef
		err         error
	)
	if info.useAddress {
		objectValue, _, err = s.emitAddressOrTemp(expr.Object)
	} else {
		objectValue, _, err = s.emitExpr(expr.Object, info.operandType)
	}
	if err != nil {
		return nil, nil, err
	}
	startValue, _, err := s.emitExpr(expr.Start, info.indexType)
	if err != nil {
		return nil, nil, err
	}
	endValue, _, err := s.emitExpr(expr.End, info.indexType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   info.helperName,
		Params: []semantic.Type{info.operandType, info.indexType, info.indexType},
		Return: info.resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(info.helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{objectValue, startValue, endValue}
	callName := "slicetmp"
	if isVoidType(info.resultType) {
		callName = ""
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	return call, info.resultType, nil
}

func (s *functionState) emitFixedArraySliceExpr(expr *ast.SliceExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	arrayType, arrayPtr, handled, err := s.fixedArraySliceBase(expr.Object)
	if err != nil || !handled {
		return nil, nil, handled, err
	}
	resultType := s.exprType(expr)
	usizeSemanticType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	startValue, _, err := s.emitExpr(expr.Start, usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	arrayLen := C.LLVMConstInt(usizeLLVMType, C.ulonglong(arrayType.ConstSize), 0)
	startClamped := s.emitUnsignedMin(startValue, arrayLen, usizeLLVMType, "arrayslice.start.clamped")
	endClamped := s.emitUnsignedMin(endValue, arrayLen, usizeLLVMType, "arrayslice.end.clamped")
	boundedStart := s.emitUnsignedMin(startClamped, endClamped, usizeLLVMType, "arrayslice.start.bounded")
	sliceLen := C.LLVMBuildSub(s.builder, endClamped, boundedStart, cStringFree("arrayslice.len"))

	llvmArrayType, err := s.g.lowerType(arrayType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
	indices := []C.LLVMValueRef{zeroIndex, boundedStart}
	dataPtr := C.LLVMBuildGEP2(s.builder, llvmArrayType, arrayPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("arrayslice.data"))

	viewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	if _, ok := resultType.(*semantic.SViewType); ok {
		viewValue := C.LLVMGetUndef(viewLLVMType)
		viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree("strslice.view.data"))
		viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, sliceLen, 1, cStringFree("strslice.view.len"))
		return viewValue, resultType, true, nil
	}
	elemSize, err := s.sizeOfType(arrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree("arrayslice.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, sliceLen, 1, cStringFree("arrayslice.view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, elemSizeValue, 2, cStringFree("arrayslice.view.elem_size"))
	return viewValue, resultType, true, nil
}

func (s *functionState) fixedArraySliceBase(object ast.Expr) (*semantic.ArrayType, C.LLVMValueRef, bool, error) {
	objectType := s.exprType(object)
	if arrayType, ok := objectType.(*semantic.ArrayType); ok {
		arrayPtr, _, err := s.emitAddressOrTemp(object)
		if err != nil {
			return nil, nil, true, err
		}
		return arrayType, arrayPtr, true, nil
	}
	refType, ok := objectType.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, nil, false, nil
	}
	arrayType, ok := refType.Elem.(*semantic.ArrayType)
	if !ok {
		return nil, nil, false, nil
	}
	arrayPtr, _, err := s.emitExpr(object, objectType)
	if err != nil {
		return nil, nil, true, err
	}
	return arrayType, arrayPtr, true, nil
}

func (s *functionState) emitUnsignedMin(left C.LLVMValueRef, right C.LLVMValueRef, llvmType C.LLVMTypeRef, name string) C.LLVMValueRef {
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULE), left, right, cStringFree(name+".cmp"))
	mask := C.LLVMBuildZExt(s.builder, cmp, llvmType, cStringFree(name+".mask"))
	negMask := C.LLVMBuildSub(s.builder, C.LLVMConstNull(llvmType), mask, cStringFree(name+".negmask"))
	diff := C.LLVMBuildXor(s.builder, left, right, cStringFree(name+".diff"))
	maskedDiff := C.LLVMBuildAnd(s.builder, diff, negMask, cStringFree(name+".masked"))
	return C.LLVMBuildXor(s.builder, right, maskedDiff, cStringFree(name))
}

func (s *functionState) emitRuntimeStringLenExpr(object ast.Expr, fieldType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	stringType, ok := dstrFieldOperandType(s.exprType(object))
	if !ok {
		return nil, nil, fmt.Errorf("string len requires dstr operand")
	}
	stringValue, _, err := s.emitExpr(object, stringType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   "ctx_strlen",
		Params: []semantic.Type{stringType},
		Return: fieldType,
	}
	callee, err := s.g.ensureFunctionDeclared("ctx_strlen", helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{stringValue}
	call := s.buildCall(llvmFnType, callee, args, "strlen")
	return call, fieldType, nil
}

func (s *functionState) emitIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	if value, actualType, handled, err := s.emitPackedStoreIndexExpr(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitChunksExactIndexExpr(expr); handled {
		return value, actualType, err
	}
	if _, ok := semanticStringArrayType(s.exprType(expr.Object)); ok {
		return s.emitStaticStringIndexExpr(expr)
	}
	if helperName, operandType, ok := runtimeStringIndexedOperand(s.exprType(expr.Object)); ok {
		return s.emitRuntimeStringIndexExpr(expr, helperName, operandType)
	}
	ptr, elemType, err := s.emitIndexAddress(expr)
	if err != nil {
		return nil, nil, err
	}
	value, err := s.loadValue(ptr, elemType, "idx")
	return value, elemType, err
}

func (s *functionState) emitChunksExactIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	chunkType, ok := semantic.ChunksExactViewItemType(s.exprType(expr.Object))
	if !ok || chunkType == nil {
		return nil, nil, false, nil
	}
	objectType, ok := s.exprType(expr.Object).(*semantic.GenericInstanceType)
	if !ok || objectType == nil {
		return nil, nil, true, fmt.Errorf("chunks_exact index expects a carrier value")
	}
	objectValue, _, err := s.emitExpr(expr.Object, objectType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	indexValue, _, err := s.emitExpr(expr.Index, usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceValue := C.LLVMBuildExtractValue(s.builder, objectValue, 0, cStringFree("chunks.source"))
	chunkSizeValue := C.LLVMBuildExtractValue(s.builder, objectValue, 1, cStringFree("chunks.chunk_size"))
	startValue := C.LLVMBuildMul(s.builder, indexValue, chunkSizeValue, cStringFree("chunks.start"))
	endValue := C.LLVMBuildAdd(s.builder, startValue, chunkSizeValue, cStringFree("chunks.end"))
	value, err := s.emitArenaViewSliceValue(sourceValue, chunkType, startValue, endValue, "chunks.item")
	if err != nil {
		return nil, nil, true, err
	}
	return value, chunkType, true, nil
}

func (s *functionState) emitRuntimeStringIndexExpr(expr *ast.IndexExpr, helperName string, operandType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	stringValue, _, err := s.emitExpr(expr.Object, operandType)
	if err != nil {
		return nil, nil, err
	}
	indexType := s.g.result.NamedTypes["i64"]
	resultType := s.g.result.NamedTypes["char"]
	indexValue, _, err := s.emitExpr(expr.Index, indexType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{operandType, indexType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{stringValue, indexValue}
	call := s.buildCall(llvmFnType, callee, args, "stridx")
	return call, resultType, nil
}

func semanticStringArrayType(t semantic.Type) (*semantic.ArrayType, bool) {
	if arrayType, ok := t.(*semantic.ArrayType); ok {
		if arrayType.SurfaceName == "str" || arrayType.SurfaceName == "string" {
			return arrayType, true
		}
		return nil, false
	}
	ref, ok := t.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	arrayType, ok := ref.Elem.(*semantic.ArrayType)
	if !ok || (arrayType.SurfaceName != "str" && arrayType.SurfaceName != "string") {
		return nil, false
	}
	return arrayType, true
}

func (s *functionState) emitStaticStringIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	ptr, _, err := s.emitIndexAddress(expr)
	if err != nil {
		return nil, nil, err
	}
	byteType := s.g.result.NamedTypes["u8"]
	loaded, err := s.loadValue(ptr, byteType, "str.byte")
	if err != nil {
		return nil, nil, err
	}
	resultType := s.g.result.NamedTypes["char"]
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMBuildZExt(s.builder, loaded, llvmResultType, cStringFree("stridx.zext")), resultType, nil
}

func runtimeStringIndexedOperand(t semantic.Type) (string, semantic.Type, bool) {
	if _, ok := t.(*semantic.DStrType); ok {
		return "ctx_string_index", t, true
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return "ctx_string_view_index", t, true
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_string_view_index", t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return "", nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return "", nil, false
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return "ctx_string_index", ref.Elem, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return "ctx_string_view_index", ref.Elem, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_string_view_index", ref.Elem, true
	}
	return "", nil, false
}

func dstrSyntheticFieldType(t semantic.Type, fieldName string) (semantic.Type, bool) {
	if fieldName != "len" {
		return nil, false
	}
	if _, ok := dstrFieldOperandType(t); !ok {
		return nil, false
	}
	return &semantic.BuiltinType{Name: "i64"}, true
}

func dstrFieldOperandType(t semantic.Type) (semantic.Type, bool) {
	if _, ok := t.(*semantic.DStrType); ok {
		return t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return ref.Elem, true
	}
	return nil, false
}

type runtimeSliceInfo struct {
	helperName  string
	operandType semantic.Type
	resultType  semantic.Type
	indexType   semantic.Type
	useAddress  bool
}

func runtimeSliceOperandInfo(objectType semantic.Type, resultType semantic.Type) (runtimeSliceInfo, bool) {
	i64Type := &semantic.BuiltinType{Name: "i64"}
	usizeType := &semantic.BuiltinType{Name: "usize"}
	if view, ok := objectType.(*semantic.DArrayType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view",
			operandType: &semantic.RefType{Elem: objectType, State: semantic.RefStateNonNull},
			resultType:  &semantic.DArrayViewType{Elem: view.Elem},
			indexType:   usizeType,
			useAddress:  true,
		}, true
	}
	if view, ok := objectType.(*semantic.ViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.ViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if view, ok := objectType.(*semantic.DArrayViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.DArrayViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if _, ok := objectType.(*semantic.DStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := objectType.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := objectType.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	ref, ok := objectType.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return runtimeSliceInfo{}, false
	}
	if view, ok := ref.Elem.(*semantic.DArrayType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view",
			operandType: objectType,
			resultType:  &semantic.DArrayViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if view, ok := ref.Elem.(*semantic.ViewType); ok {
		return runtimeSliceInfo{helperName: "arena_da_view_slice", operandType: ref.Elem, resultType: &semantic.ViewType{Elem: view.Elem}, indexType: usizeType}, true
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	return runtimeSliceInfo{}, false
}

func runtimeStringCompareInfo(leftType semantic.Type, rightType semantic.Type) (string, semantic.Type, semantic.Type, bool, bool) {
	leftKind := classifyRuntimeStringCompareKind(leftType)
	rightKind := classifyRuntimeStringCompareKind(rightType)
	if leftKind == runtimeStringCompareNone || rightKind == runtimeStringCompareNone {
		return "", nil, nil, false, false
	}
	if leftKind == runtimeStringCompareRaw && rightKind == runtimeStringCompareRaw {
		return "", nil, nil, false, false
	}
	if leftKind == runtimeStringCompareView && rightKind == runtimeStringCompareView {
		return "ctx_string_views_eq", leftType, rightType, false, true
	}
	if leftKind == runtimeStringCompareView {
		return "ctx_string_view_eq", leftType, rightType, false, true
	}
	if rightKind == runtimeStringCompareView {
		return "ctx_string_view_eq", rightType, leftType, true, true
	}
	return "ctx_streq", leftType, rightType, false, true
}

type runtimeStringCompareKind int

const (
	runtimeStringCompareNone runtimeStringCompareKind = iota
	runtimeStringCompareDStr
	runtimeStringCompareView
	runtimeStringCompareRaw
)

func classifyRuntimeStringCompareKind(t semantic.Type) runtimeStringCompareKind {
	if t == nil {
		return runtimeStringCompareNone
	}
	if _, ok := t.(*semantic.DStrType); ok {
		return runtimeStringCompareDStr
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeStringCompareView
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return runtimeStringCompareNone
	}
	if builtin, ok := ref.Elem.(*semantic.BuiltinType); ok && builtin.Name == "u8" {
		return runtimeStringCompareRaw
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	return runtimeStringCompareNone
}
func (s *functionState) emitCastExpr(expr *ast.CastExpr) (C.LLVMValueRef, semantic.Type, error) {
	targetType := s.exprType(expr)
	if targetType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for cast target")
	}
	value, actualType, err := s.emitExpr(expr.Operand, nil)
	if err != nil {
		return nil, nil, err
	}
	coerced, err := s.coerceValue(value, actualType, targetType)
	if err != nil {
		return nil, nil, err
	}
	return coerced, targetType, nil
}

func (s *functionState) emitSizeofExpr(expr *ast.SizeofExpr) (C.LLVMValueRef, semantic.Type, error) {
	t, err := s.resolveTypeExpr(expr.Type)
	if err != nil {
		return nil, nil, err
	}
	size, err := s.sizeOfType(t)
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMConstInt(usizeType, C.ulonglong(size), 0), s.g.result.NamedTypes["usize"], nil
}

func (s *functionState) emitTernaryExpr(expr *ast.TernaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	condValue, _, err := s.emitExpr(expr.Cond, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.then"))
	elseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.else"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.end"))
	C.LLVMBuildCondBr(s.builder, condValue, thenBB, elseBB)

	C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
	leftValue, _, err := s.emitExpr(expr.Value, resultType)
	if err != nil {
		return nil, nil, err
	}
	thenEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(thenEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
	rightValue, _, err := s.emitExpr(expr.Alt, resultType)
	if err != nil {
		return nil, nil, err
	}
	elseEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(elseEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("termp"))
	values := []C.LLVMValueRef{leftValue, rightValue}
	blocks := []C.LLVMBasicBlockRef{thenEnd, elseEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	_ = parentBlock
	return phi, resultType, nil
}

func (s *functionState) emitAddrOfExpr(expr *ast.AddrOfExpr) (C.LLVMValueRef, semantic.Type, error) {
	ptr, operandType, err := s.emitAddress(expr.Operand)
	if err != nil {
		return nil, nil, err
	}
	return ptr, &semantic.RefType{Elem: operandType, State: semantic.RefStateNonNull}, nil
}

func (s *functionState) emitSpecializeExpr(expr *ast.SpecializeExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Operand == nil {
		return nil, nil, fmt.Errorf("missing specialization operand")
	}
	ident, ok := expr.Operand.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("specialize expects a named generic function")
	}
	sym, ok := s.g.result.GlobalScope.Lookup(ident.Name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown generic function %q during LLVM lowering", ident.Name)
	}
	baseType, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		return nil, nil, fmt.Errorf("specialize expects a function, got %s", sym.Type.String())
	}
	params := funcGenericParams(baseType)
	if len(params) == 0 {
		return nil, nil, fmt.Errorf("function %q is not generic", ident.Name)
	}
	if len(expr.TypeArgs) != len(params) {
		return nil, nil, fmt.Errorf("function %q expects %d arguments, got %d", ident.Name, len(params), len(expr.TypeArgs))
	}
	bindings := make(map[string]semantic.Type, len(params))
	for i, arg := range expr.TypeArgs {
		resolved, err := s.resolveTypeExpr(arg)
		if err != nil {
			return nil, nil, err
		}
		bindings[params[i].Name] = resolved
	}
	specialized := specializeFuncType(baseType, bindings)
	if decl, ok := sym.Node.(*ast.FuncDecl); ok {
		value, lowered, err := s.g.ensureSpecializedFunction(decl, baseType, bindings)
		return value, lowered, err
	}
	value, err := s.g.ensureFunctionDeclared(ident.Name, specialized)
	return value, specialized, err
}

func (s *functionState) emitStructLitExpr(expr *ast.StructLitExpr) (C.LLVMValueRef, semantic.Type, error) {
	structType := s.exprType(expr)
	llvmType, err := s.g.lowerType(structType)
	if err != nil {
		return nil, nil, err
	}
	fields, err := s.g.structLiteralFields(structType)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	for i, arg := range expr.Args {
		if i >= len(fields) {
			break
		}
		fieldValue, _, err := s.emitExpr(arg, fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, fieldValue, C.unsigned(i), cStringFree("ins"))
	}
	return value, structType, nil
}

func (s *functionState) enumConstructorInfo(expr *ast.CallExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	return s.enumConstructorInfoFromField(fieldExpr)
}

func (s *functionState) enumConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	base, ok := s.g.result.NamedTypes[ident.Name]
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(expr.Field)
	if !ok {
		return enumType, nil, true
	}
	return enumType, variant, true
}

func (s *functionState) emitEnumConstructorValue(enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing enum constructor metadata")
	}
	if enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum constructor %s.%s must be allocated with new[%s]", enumType.Name, variant.Name, enumType.StoreType.Name)
	}
	orderedArgs, err := s.resolveEnumConstructorArgs(enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	if enumIsTagOnly(enumType) {
		tagValue, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, nil, err
		}
		return tagValue, enumType, nil
	}
	enumPtr, err := s.emitStackTempZeroed(enumType, "enum.ctor")
	if err != nil {
		return nil, nil, err
	}
	enumLLVMType, err := s.g.lowerType(enumType)
	if err != nil {
		return nil, nil, err
	}
	tagValue, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	tagPtr := C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, 0, cStringFree("enum.tag.ptr"))
	C.LLVMBuildStore(s.builder, tagValue, tagPtr)
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(enumPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		if len(variant.Payload) == 1 {
			argValue, _, err := s.emitExpr(orderedArgs[0], variant.Payload[0])
			if err != nil {
				return nil, nil, err
			}
			if !llvmValueIsZeroConstant(argValue) {
				C.LLVMBuildStore(s.builder, argValue, payloadPtr)
			}
		} else {
			payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
			if err != nil {
				return nil, nil, err
			}
			aggregate := C.LLVMGetUndef(payloadType)
			allZero := true
			for i, payload := range variant.Payload {
				argValue, _, err := s.emitExpr(orderedArgs[i], payload)
				if err != nil {
					return nil, nil, err
				}
				if !llvmValueIsZeroConstant(argValue) {
					allZero = false
				}
				aggregate = C.LLVMBuildInsertValue(s.builder, aggregate, argValue, C.unsigned(i), cStringFree("enum.payload.ins"))
			}
			if !allZero {
				C.LLVMBuildStore(s.builder, aggregate, payloadPtr)
			}
		}
	}
	value, err := s.loadValue(enumPtr, enumType, "enum.value")
	if err != nil {
		return nil, nil, err
	}
	return value, enumType, nil
}

func (s *functionState) emitPackedEnumConstructorAlloc(storeValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("missing packed enum constructor metadata")
	}
	orderedArgs, commonArgs, err := s.resolvePackedEnumConstructorArgs(enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	tagValue, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	if s.canInlinePackedEnumVariant(enumType, variant) {
		var payloadValue C.LLVMValueRef
		var payloadType semantic.Type
		if len(orderedArgs) == 1 {
			payloadType = variant.Payload[0]
			payloadValue, _, err = s.emitExpr(orderedArgs[0], payloadType)
			if err != nil {
				return nil, nil, err
			}
		}
		inlineHandle, err := s.buildInlinePackedEnumHandle(tagValue, payloadValue, payloadType)
		if err != nil {
			return nil, nil, err
		}
		return inlineHandle, enumType, nil
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, nil, err
	}
	tailPlan, err := s.preparePackedEnumTailPayloadPlan(variant, orderedArgs)
	if err != nil {
		return nil, nil, err
	}
	allocPtr, enumValue, rowSizeValue, err := s.emitPackedEnumStorageAlloc(storeValue, enumType, tailPlan, tagValue)
	if err != nil {
		return nil, nil, err
	}
	rowValue := C.LLVMConstNull(rowType)
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, tagValue, 0, cStringFree("packed.enum.tag.ins"))
	for i, commonDecl := range enumType.Decl.Common {
		arg, ok := commonArgs[commonDecl.Name]
		if !ok {
			continue
		}
		field, ok := enumType.Common[commonDecl.Name]
		if !ok {
			return nil, nil, fmt.Errorf("missing packed enum common field %s.%s", enumType.Name, commonDecl.Name)
		}
		fieldValue, _, err := s.emitExpr(arg, field.Type)
		if err != nil {
			return nil, nil, err
		}
		rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, fieldValue, C.unsigned(1+i), cStringFree("packed.enum.common.ins"))
	}
	C.LLVMBuildStore(s.builder, rowValue, allocPtr)
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(allocPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		var tailDataPtr C.LLVMValueRef
		if tailPlan != nil {
			tailDataPtr, err = s.emitPackedEnumTailDataPtr(allocPtr, rowSizeValue)
			if err != nil {
				return nil, nil, err
			}
		}
		if len(variant.Payload) == 1 {
			argValue, err := s.emitPackedEnumConstructorPayloadValue(variant, 0, orderedArgs[0], tailPlan, tailDataPtr)
			if err != nil {
				return nil, nil, err
			}
			if !llvmValueIsZeroConstant(argValue) {
				C.LLVMBuildStore(s.builder, argValue, payloadPtr)
			}
		} else {
			payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
			if err != nil {
				return nil, nil, err
			}
			aggregate := C.LLVMGetUndef(payloadType)
			allZero := true
			for i, payload := range variant.Payload {
				_ = payload
				argValue, err := s.emitPackedEnumConstructorPayloadValue(variant, i, orderedArgs[i], tailPlan, tailDataPtr)
				if err != nil {
					return nil, nil, err
				}
				if !llvmValueIsZeroConstant(argValue) {
					allZero = false
				}
				aggregate = C.LLVMBuildInsertValue(s.builder, aggregate, argValue, C.unsigned(i), cStringFree("packed.enum.payload.ins"))
			}
			if !allZero {
				C.LLVMBuildStore(s.builder, aggregate, payloadPtr)
			}
		}
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: enumType.StoreType}
	if err := ops.recordPrefixWords(allocPtr, "packed.prefix.record"); err != nil {
		return nil, nil, err
	}
	mode := s.g.packedModeForEnum(enumType)
	if mode != packedEnumABIVariantSparse && (tailPlan != nil || (mode != packedEnumABIWordHandle && mode != packedEnumABIIndexSOA)) {
		if err := s.emitPackedStoreRecordTag(storeValue, enumType.StoreType, tagValue); err != nil {
			return nil, nil, err
		}
	}
	return enumValue, enumType, nil
}

func (s *functionState) canInlinePackedEnumVariant(enumType *semantic.EnumType, variant *semantic.EnumVariant) bool {
	return s != nil && s.g != nil && s.g.packedModeForEnum(enumType) == packedEnumABIWordHandle && s.g.wordBits == 64 && variant != nil && variant.CanInlineWordHandle(enumType)
}

func (s *functionState) buildInlinePackedEnumHandle(tagValue C.LLVMValueRef, payloadValue C.LLVMValueRef, payloadType semantic.Type) (C.LLVMValueRef, error) {
	uintptrLLVMType, err := s.g.lowerBuiltin("uintptr")
	if err != nil {
		return nil, err
	}
	handleValue := C.LLVMBuildZExt(s.builder, tagValue, uintptrLLVMType, cStringFree("packed.inline.tag.zext"))
	handleValue = C.LLVMBuildShl(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 49, 0), cStringFree("packed.inline.tag.shift"))
	if payloadValue != nil && payloadType != nil {
		payloadBits := C.LLVMBuildZExt(s.builder, payloadValue, uintptrLLVMType, cStringFree("packed.inline.payload.zext"))
		payloadBits = C.LLVMBuildShl(s.builder, payloadBits, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.payload.shift"))
		handleValue = C.LLVMBuildOr(s.builder, handleValue, payloadBits, cStringFree("packed.inline.payload.or"))
	}
	return C.LLVMBuildOr(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.handle")), nil
}

func (s *functionState) emitPackedStoreRecordTag(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, tagValue C.LLVMValueRef) error {
	if storeType == nil {
		return fmt.Errorf("missing packed store type for tag record")
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
	return ops.recordTag(tagValue, "packed.tag.record")
}

type packedEnumTailPayloadPlan struct {
	index         int
	viewType      *semantic.DArrayViewType
	elemSizeValue C.LLVMValueRef
	lenValue      C.LLVMValueRef
	byteCount     C.LLVMValueRef
	sourceData    C.LLVMValueRef
	literal       *ast.ListLitExpr
}

func (s *functionState) preparePackedEnumTailPayloadPlan(variant *semantic.EnumVariant, orderedArgs []ast.Expr) (*packedEnumTailPayloadPlan, error) {
	if variant == nil {
		return nil, nil
	}
	tailIndex, ok := variant.TailPayloadIndex()
	if !ok {
		return nil, nil
	}
	viewType, ok := variant.TailPayloadViewType()
	if !ok || viewType == nil {
		return nil, fmt.Errorf("packed enum %s tail payload metadata is inconsistent", variant.Name)
	}
	if tailIndex >= len(orderedArgs) {
		return nil, fmt.Errorf("packed enum %s tail payload argument is missing", variant.Name)
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	elemSize, err := s.sizeOfType(viewType.Elem)
	if err != nil {
		return nil, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	arg := orderedArgs[tailIndex]
	if literal, ok := arg.(*ast.ListLitExpr); ok {
		lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(len(literal.Elems)), 0)
		byteCount := C.LLVMConstInt(usizeLLVMType, C.ulonglong(uint64(len(literal.Elems))*elemSize), 0)
		return &packedEnumTailPayloadPlan{index: tailIndex, viewType: viewType, elemSizeValue: elemSizeValue, lenValue: lenValue, byteCount: byteCount, literal: literal}, nil
	}
	sourceType := s.exprType(arg)
	if _, ok := sourceType.(*semantic.DArrayViewType); !ok {
		if _, ok := sourceType.(*semantic.ViewType); !ok {
			return nil, fmt.Errorf("packed enum %s tail payload expects a list literal or view-compatible source, got %s", variant.Name, sourceType.String())
		}
	}
	viewValue, _, err := s.emitExpr(arg, sourceType)
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("packed.tail.src.len"))
	sourceData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("packed.tail.src.data"))
	byteCount := C.LLVMBuildMul(s.builder, lenValue, elemSizeValue, cStringFree("packed.tail.bytes"))
	return &packedEnumTailPayloadPlan{index: tailIndex, viewType: viewType, elemSizeValue: elemSizeValue, lenValue: lenValue, byteCount: byteCount, sourceData: sourceData}, nil
}

func (s *functionState) emitPackedEnumTailDataPtr(allocPtr C.LLVMValueRef, rowSizeValue C.LLVMValueRef) (C.LLVMValueRef, error) {
	i8Type, err := s.g.lowerBuiltin("u8")
	if err != nil {
		return nil, err
	}
	indices := []C.LLVMValueRef{rowSizeValue}
	return C.LLVMBuildGEP2(s.builder, i8Type, allocPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("packed.tail.data")), nil
}

func (s *functionState) emitPackedEnumConstructorPayloadValue(variant *semantic.EnumVariant, index int, arg ast.Expr, tailPlan *packedEnumTailPayloadPlan, tailDataPtr C.LLVMValueRef) (C.LLVMValueRef, error) {
	if tailPlan != nil && index == tailPlan.index {
		return s.emitPackedEnumTailPayloadValue(tailPlan, tailDataPtr)
	}
	argValue, _, err := s.emitExpr(arg, variant.Payload[index])
	if err != nil {
		return nil, err
	}
	return argValue, nil
}

func (s *functionState) emitPackedEnumTailPayloadValue(plan *packedEnumTailPayloadPlan, tailDataPtr C.LLVMValueRef) (C.LLVMValueRef, error) {
	if plan == nil || plan.viewType == nil {
		return nil, fmt.Errorf("missing packed enum tail payload plan")
	}
	viewLLVMType, err := s.g.lowerType(plan.viewType)
	if err != nil {
		return nil, err
	}
	if plan.literal != nil {
		elemLLVMType, err := s.g.lowerType(plan.viewType.Elem)
		if err != nil {
			return nil, err
		}
		usizeType := s.g.result.NamedTypes["usize"]
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, err
		}
		for i, elem := range plan.literal.Elems {
			elemValue, _, err := s.emitExpr(elem, plan.viewType.Elem)
			if err != nil {
				return nil, err
			}
			indexValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, tailDataPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("packed.tail.elem.ptr"))
			C.LLVMBuildStore(s.builder, elemValue, elemPtr)
		}
	} else {
		if err := s.emitPackedEnumTailMemcpy(tailDataPtr, plan.sourceData, plan.byteCount); err != nil {
			return nil, err
		}
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, tailDataPtr, 0, cStringFree("packed.tail.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, plan.lenValue, 1, cStringFree("packed.tail.view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, plan.elemSizeValue, 2, cStringFree("packed.tail.view.elem_size"))
	return viewValue, nil
}

func (s *functionState) emitPackedEnumTailMemcpy(dstData C.LLVMValueRef, srcData C.LLVMValueRef, byteCount C.LLVMValueRef) error {
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.result.NamedTypes["usize"]
	memcpyType := &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstData, srcData, byteCount}, "packed.tail.memcpy")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	return nil
}

func (s *functionState) emitPackedEnumStorageAlloc(storeValue C.LLVMValueRef, enumType *semantic.EnumType, tailPlan *packedEnumTailPayloadPlan, fixedTagValue C.LLVMValueRef) (C.LLVMValueRef, C.LLVMValueRef, C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, nil, nil, fmt.Errorf("missing packed enum storage metadata")
	}
	storeType := enumType.StoreType
	if storeType == nil {
		return nil, nil, nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
	rowSizeValue, err := ops.rowBytesValue("packed.alloc.store")
	if err != nil {
		return nil, nil, nil, err
	}
	totalSizeValue := rowSizeValue
	if tailPlan != nil {
		totalSizeValue = C.LLVMBuildAdd(s.builder, rowSizeValue, tailPlan.byteCount, cStringFree("packed.alloc.bytes"))
	}
	return ops.allocateStorage(enumType, totalSizeValue, tailPlan != nil, fixedTagValue, "packed.alloc.store")
}

func (s *functionState) resolveEnumConstructorArgs(enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, error) {
	if variant == nil {
		return nil, fmt.Errorf("missing enum constructor metadata")
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		return args, nil
	}
	if namedCount != len(args) {
		return nil, fmt.Errorf("enum constructor %s.%s cannot mix positional and named arguments", enumType.Name, variant.Name)
	}
	if !variant.HasNamedPayloads() {
		return nil, fmt.Errorf("enum constructor %s.%s does not declare named payload fields", enumType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seen := make([]bool, len(variant.Payload))
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		index, ok := variant.PayloadIndex(name)
		if !ok {
			return nil, fmt.Errorf("enum constructor %s.%s has no payload field %q", enumType.Name, variant.Name, name)
		}
		if seen[index] {
			return nil, fmt.Errorf("enum constructor %s.%s payload field %q is specified more than once", enumType.Name, variant.Name, name)
		}
		ordered[index] = arg
		seen[index] = true
	}
	for i, wasSeen := range seen {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, fmt.Errorf("enum constructor %s.%s is missing argument %d", enumType.Name, variant.Name, i+1)
			}
			return nil, fmt.Errorf("enum constructor %s.%s is missing payload field %q", enumType.Name, variant.Name, label)
		}
	}
	return ordered, nil
}

func (s *functionState) resolvePackedEnumConstructorArgs(enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, map[string]ast.Expr, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing packed enum constructor metadata")
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		return args, nil, nil
	}
	if namedCount != len(args) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s cannot mix positional and named arguments", enumType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seenPayload := make([]bool, len(variant.Payload))
	commonArgs := make(map[string]ast.Expr)
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		if index, ok := variant.PayloadIndex(name); ok {
			if seenPayload[index] {
				return nil, nil, fmt.Errorf("enum constructor %s.%s payload field %q is specified more than once", enumType.Name, variant.Name, name)
			}
			ordered[index] = arg
			seenPayload[index] = true
			continue
		}
		if _, ok := enumType.Common[name]; ok {
			if _, exists := commonArgs[name]; exists {
				return nil, nil, fmt.Errorf("packed enum constructor %s.%s common field %q is specified more than once", enumType.Name, variant.Name, name)
			}
			commonArgs[name] = arg
			continue
		}
		return nil, nil, fmt.Errorf("packed enum constructor %s.%s has no payload or common field %q", enumType.Name, variant.Name, name)
	}
	for i, wasSeen := range seenPayload {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, nil, fmt.Errorf("enum constructor %s.%s is missing argument %d", enumType.Name, variant.Name, i+1)
			}
			return nil, nil, fmt.Errorf("enum constructor %s.%s is missing payload field %q", enumType.Name, variant.Name, label)
		}
	}
	return ordered, commonArgs, nil
}
