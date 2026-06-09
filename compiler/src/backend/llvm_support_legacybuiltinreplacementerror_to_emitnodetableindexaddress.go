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

func legacyBuiltinReplacementError(oldName, replacement string) error {
	return fmt.Errorf("legacy built-in %q has been replaced; use %q instead", oldName, replacement)
}
// backendIsAddressableLValue reports whether expr denotes a storage location whose
// address emitAddress can take (mirroring emitAddress's supported node kinds). Used to
// decide when materializing a `T&` from a value-typed expression should take the
// element's address rather than reinterpret a loaded value as a pointer.
func backendIsAddressableLValue(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return true
	case *ast.FieldExpr:
		return true
	case *ast.IndexExpr:
		return true
	case *ast.ParenExpr:
		return backendIsAddressableLValue(n.Inner)
	default:
		return false
	}
}

func (s *functionState) emitAddress(expr ast.Expr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		if binding, ok := s.lookupBinding(n.Name); ok {
			if refType, ok := binding.typ.(*semantic.RefType); ok && !binding.mutable {
				refValue, err := s.loadValue(binding.ptr, binding.typ, n.Name+".ref")
				if err != nil {
					return nil, nil, err
				}
				return refValue, refType.Elem, nil
			}
			return binding.ptr, binding.typ, nil
		}
		if sym, resolvedName, ok := s.lookupVisibleGlobalSymbol(n.Name); ok {
			if sym.Kind == semantic.SymbolGlobal || sym.Kind == semantic.SymbolExternVar {
				global, err := s.g.ensureGlobalDeclared(resolvedName, sym.Type, sym.Kind == semantic.SymbolExternVar)
				return global, sym.Type, err
			}
			// Aggregate (array) consts are materialized as read-only globals (see
			// emitIdentValueAddress / emitDeclInNamespace), so their address can be
			// taken — e.g. TABLE.ref[u32[N]&] to build a zero-copy view over a
			// static const table. Mutating through such a ref is rejected by the
			// semantic layer (a const yields no writable ref).
			if sym.Kind == semantic.SymbolConst {
				if _, isArray := sym.Type.(*semantic.ArrayType); isArray {
					global, err := s.g.ensureGlobalDeclared(resolvedName, sym.Type, false)
					return global, sym.Type, err
				}
			}
		}
		return nil, nil, fmt.Errorf("identifier %s is not addressable", n.Name)
	case *ast.FieldExpr:
		return s.emitFieldAddress(n)
	case *ast.IndexExpr:
		return s.emitIndexAddress(n, false)
	case *ast.ParenExpr:
		return s.emitAddress(n.Inner)
	default:
		return nil, nil, fmt.Errorf("expression %T is not addressable", expr)
	}
}
func (s *functionState) emitValueAddress(expr ast.Expr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		return s.emitIdentValueAddress(n)
	case *ast.FieldExpr:
		return s.emitReadableFieldAddress(n)
	case *ast.ParenExpr:
		return s.emitValueAddress(n.Inner)
	default:
		return s.emitAddress(expr)
	}
}
func (s *functionState) emitIdentValueAddress(expr *ast.Ident) (C.LLVMValueRef, semantic.Type, error) {
	valueType := s.exprType(expr)
	if semantic.IsNullType(valueType) {
		return nil, nil, fmt.Errorf("identifier %s is known null and has no readable address", expr.Name)
	}
	if binding, ok := s.lookupBinding(expr.Name); ok {
		return s.refinedOptionalPayloadAddress(binding.ptr, binding.typ, valueType, expr.Name)
	}
	if sym, resolvedName, ok := s.lookupVisibleGlobalSymbol(expr.Name); ok {
		if sym.Kind == semantic.SymbolGlobal || sym.Kind == semantic.SymbolExternVar {
			global, err := s.g.ensureGlobalDeclared(resolvedName, sym.Type, sym.Kind == semantic.SymbolExternVar)
			if err != nil {
				return nil, nil, err
			}
			return s.refinedOptionalPayloadAddress(global, sym.Type, valueType, expr.Name)
		}
		// Aggregate (array) consts are materialized as read-only globals (see
		// emitDeclInNamespace), so they are addressable for indexing.
		if sym.Kind == semantic.SymbolConst {
			if _, isArray := sym.Type.(*semantic.ArrayType); isArray {
				global, err := s.g.ensureGlobalDeclared(resolvedName, sym.Type, false)
				if err != nil {
					return nil, nil, err
				}
				return s.refinedOptionalPayloadAddress(global, sym.Type, valueType, expr.Name)
			}
		}
	}
	return nil, nil, fmt.Errorf("identifier %s is not addressable", expr.Name)
}
func (s *functionState) refinedOptionalPayloadAddress(ptr C.LLVMValueRef, storedType semantic.Type, valueType semantic.Type, name string) (C.LLVMValueRef, semantic.Type, error) {
	optionalType, ok := storedType.(*semantic.OptionalType)
	if !ok || valueType == nil || semantic.SameType(storedType, valueType) {
		return ptr, storedType, nil
	}
	if semantic.IsNullType(valueType) {
		return nil, nil, fmt.Errorf("known-null optional has no payload address")
	}
	if optionalType.Value == nil || !semantic.SameType(optionalType.Value, valueType) {
		return ptr, storedType, nil
	}
	llvmOptionalType, err := s.g.lowerType(optionalType)
	if err != nil {
		return nil, nil, err
	}
	payloadPtr := C.LLVMBuildStructGEP2(s.builder, llvmOptionalType, ptr, 1, cStringFree(name+".payload"))
	return payloadPtr, valueType, nil
}
func (s *functionState) emitFieldAddress(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if ptr, fieldType, handled, err := s.emitStoreRowFieldAddress(expr); handled {
		return ptr, fieldType, err
	}
	if ptr, fieldType, handled, err := s.emitTreeFieldAddress(expr); handled {
		return ptr, fieldType, err
	}
	objType := s.exprType(expr.Object)
	if objType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for field base when lowering .%s", expr.Field)
	}
	fieldType, index, containerType, pointerLike, err := s.g.fieldInfo(objType, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	var containerLLVMType C.LLVMTypeRef
	if enumType, ok := containerType.(*semantic.EnumType); ok && enumType.Packed {
		containerLLVMType, err = s.loweredEnumStorageType(enumType)
	} else {
		containerLLVMType, err = s.g.lowerType(containerType)
	}
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := containerType.(*semantic.EnumType); ok && enumType.Packed {
		if key, ok := s.packedEnumStoragePath(expr.Object); ok {
			if cachedPtr, ok := s.lookupPackedEnumStorage(key, enumType); ok {
				fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, cachedPtr, C.unsigned(index), cStringFree(expr.Field))
				return fieldPtr, fieldType, nil
			}
		}
	}
	var objPtr C.LLVMValueRef
	if pointerLike {
		objPtr, _, err = s.emitExpr(expr.Object, nil)
	} else {
		objPtr, _, err = s.emitAddress(expr.Object)
	}
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := containerType.(*semantic.EnumType); ok && enumType.Packed {
		var activeStore *packedStoreBinding
		if binding, ok := s.lookupPackedStore(enumType); ok {
			activeStore = &binding
		}
		objPtr, err = s.packedEnumStoragePtrFromExprValue(objPtr, objType, enumType, activeStore)
		if err != nil {
			return nil, nil, err
		}
		if key, ok := s.packedEnumStoragePath(expr.Object); ok {
			s.bindPackedEnumStorage(key, enumType, objPtr)
		}
	}
	// Guard the base before computing a field address: a null/near-null object base
	// here is the store/address-of analogue of the read guard in loadValue, catching
	// e.g. `nullObj.field <- x` at the base instead of as a downstream SIGSEGV.
	if err := s.emitDebugPointerDerefGuard(objPtr); err != nil {
		return nil, nil, err
	}
	fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, objPtr, C.unsigned(index), cStringFree(expr.Field))
	return fieldPtr, fieldType, nil
}
func (s *functionState) emitReadableFieldAddress(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if ptr, fieldType, handled, err := s.emitStoreRowFieldAddress(expr); handled {
		return ptr, fieldType, err
	}
	if ptr, fieldType, handled, err := s.emitTreeFieldAddress(expr); handled {
		if err != nil {
			return nil, nil, err
		}
		return s.refinedOptionalPayloadAddress(ptr, fieldType, s.exprType(expr), expr.Field)
	}
	objType := s.exprType(expr.Object)
	if objType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for field base when lowering .%s", expr.Field)
	}
	fieldType, index, containerType, pointerLike, err := s.g.fieldInfo(objType, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	var containerLLVMType C.LLVMTypeRef
	if enumType, ok := containerType.(*semantic.EnumType); ok && enumType.Packed {
		containerLLVMType, err = s.loweredEnumStorageType(enumType)
	} else {
		containerLLVMType, err = s.g.lowerType(containerType)
	}
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := containerType.(*semantic.EnumType); ok && enumType.Packed {
		if key, ok := s.packedEnumStoragePath(expr.Object); ok {
			if cachedPtr, ok := s.lookupPackedEnumStorage(key, enumType); ok {
				fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, cachedPtr, C.unsigned(index), cStringFree(expr.Field))
				return s.refinedOptionalPayloadAddress(fieldPtr, fieldType, s.exprType(expr), expr.Field)
			}
		}
	}
	var objPtr C.LLVMValueRef
	if pointerLike {
		objPtr, _, err = s.emitExpr(expr.Object, nil)
	} else {
		objPtr, _, err = s.emitValueAddress(expr.Object)
	}
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := containerType.(*semantic.EnumType); ok && enumType.Packed {
		var activeStore *packedStoreBinding
		if binding, ok := s.lookupPackedStore(enumType); ok {
			activeStore = &binding
		}
		objPtr, err = s.packedEnumStoragePtrFromExprValue(objPtr, objType, enumType, activeStore)
		if err != nil {
			return nil, nil, err
		}
		if key, ok := s.packedEnumStoragePath(expr.Object); ok {
			s.bindPackedEnumStorage(key, enumType, objPtr)
		}
	}
	// Guard the base for field reads too (covers reads that take the address here and
	// load directly rather than through loadValue's guarded path).
	if err := s.emitDebugPointerDerefGuard(objPtr); err != nil {
		return nil, nil, err
	}
	fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, objPtr, C.unsigned(index), cStringFree(expr.Field))
	return s.refinedOptionalPayloadAddress(fieldPtr, fieldType, s.exprType(expr), expr.Field)
}
func backendSafeChainReceiverType(receiverType semantic.Type) (semantic.Type, bool) {
	if receiverType == nil {
		return nil, false
	}
	if optionalType, ok := receiverType.(*semantic.OptionalType); ok && optionalType != nil && optionalType.Value != nil {
		return optionalType.Value, true
	}
	if refType, ok := receiverType.(*semantic.RefType); ok && refType != nil && refType.State != semantic.RefStateNonNull {
		cloned := *refType
		cloned.State = semantic.RefStateNonNull
		return &cloned, true
	}
	return nil, false
}
func backendImplicitCallLikeRefUpcastType(expected *semantic.RefType, actual semantic.Type) (semantic.Type, bool) {
	if expected == nil {
		return nil, false
	}
	actualRef, ok := actual.(*semantic.RefType)
	if !ok || actualRef == nil {
		return nil, false
	}
	if expected.Storage != semantic.RefStorageAny || actualRef.Storage == semantic.RefStorageAny {
		return nil, false
	}
	if expected.Mutable && !actualRef.Mutable {
		return nil, false
	}
	if expected.State != actualRef.State || expected.Region != actualRef.Region {
		return nil, false
	}
	if !semantic.AssignableTo(expected.Elem, actualRef.Elem) {
		return nil, false
	}
	coerced := *actualRef
	coerced.Storage = semantic.RefStorageAny
	coerced.ExplicitStorage = expected.ExplicitStorage
	return &coerced, true
}
func (s *functionState) emitSafeChainReceiverValue(receiver ast.Expr) (C.LLVMValueRef, C.LLVMValueRef, semantic.Type, error) {
	if receiver == nil {
		return nil, nil, nil, fmt.Errorf("optional chaining requires a receiver")
	}
	sourceType := s.exprType(receiver)
	baseReceiverType, ok := backendSafeChainReceiverType(sourceType)
	if !ok {
		return nil, nil, nil, fmt.Errorf("optional chaining requires an optional or nullable reference receiver, got %s", sourceType.String())
	}
	if optionalType, ok := sourceType.(*semantic.OptionalType); ok && optionalType != nil {
		fallibleValue, _, err := s.emitExpr(receiver, sourceType)
		if err != nil {
			return nil, nil, nil, err
		}
		presentValue, err := s.extractOptionalPresent(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, nil, err
		}
		payloadValue, err := s.extractOptionalPayload(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, nil, err
		}
		return presentValue, payloadValue, baseReceiverType, nil
	}
	refValue, _, err := s.emitExpr(receiver, sourceType)
	if err != nil {
		return nil, nil, nil, err
	}
	nullValue := C.LLVMConstNull(C.LLVMTypeOf(refValue))
	presentValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), refValue, nullValue, cStringFree("safe.receiver.present"))
	return presentValue, refValue, baseReceiverType, nil
}
func (s *functionState) emitFieldAddressFromObjectValue(objValue C.LLVMValueRef, objType semantic.Type, fieldName string, name string) (C.LLVMValueRef, semantic.Type, error) {
	if objType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for field %q", fieldName)
	}
	fieldType, index, containerType, pointerLike, err := s.g.fieldInfo(objType, fieldName)
	if err != nil {
		return nil, nil, err
	}
	var containerLLVMType C.LLVMTypeRef
	if enumType, ok := containerType.(*semantic.EnumType); ok && enumType.Packed {
		containerLLVMType, err = s.loweredEnumStorageType(enumType)
	} else {
		containerLLVMType, err = s.g.lowerType(containerType)
	}
	if err != nil {
		return nil, nil, err
	}
	objPtr := objValue
	if !pointerLike {
		objPtr, err = s.emitStackTempValue(objValue, objType, name+".obj")
		if err != nil {
			return nil, nil, err
		}
	}
	if enumType, ok := containerType.(*semantic.EnumType); ok && enumType.Packed {
		var activeStore *packedStoreBinding
		if binding, ok := s.lookupPackedStore(enumType); ok {
			activeStore = &binding
		}
		objPtr, err = s.packedEnumStoragePtrFromExprValue(objPtr, objType, enumType, activeStore)
		if err != nil {
			return nil, nil, err
		}
	}
	fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, objPtr, C.unsigned(index), cStringFree(name+"."+fieldName))
	return fieldPtr, fieldType, nil
}
func (s *functionState) emitFieldValueFromObjectValue(objValue C.LLVMValueRef, objType semantic.Type, fieldName string, name string) (C.LLVMValueRef, semantic.Type, error) {
	if fieldType, ok := cstrSyntheticFieldType(objType, fieldName); ok {
		value, err := s.emitRuntimeStringLengthValue(objValue, objType, fieldType, name+"."+fieldName)
		return value, fieldType, err
	}
	fieldPtr, fieldType, err := s.emitFieldAddressFromObjectValue(objValue, objType, fieldName, name)
	if err != nil {
		return nil, nil, err
	}
	value, err := s.loadValue(fieldPtr, fieldType, name+"."+fieldName)
	if err != nil {
		return nil, nil, err
	}
	return value, fieldType, nil
}
func (s *functionState) emitPreparedUFCSReceiverValue(receiverValue C.LLVMValueRef, actualType semantic.Type, expectedType semantic.Type, name string) (C.LLVMValueRef, semantic.Type, error) {
	if actualType == nil || expectedType == nil {
		return receiverValue, actualType, nil
	}
	if semantic.AssignableTo(expectedType, actualType) {
		coerced, err := s.coerceValue(receiverValue, actualType, expectedType)
		if err != nil {
			return receiverValue, actualType, nil
		}
		return coerced, expectedType, nil
	}
	expectedRef, ok := expectedType.(*semantic.RefType)
	if !ok || expectedRef == nil {
		return nil, nil, fmt.Errorf("UFCS receiver expects %s, got %s", expectedType.String(), actualType.String())
	}
	if upcastType, ok := backendImplicitCallLikeRefUpcastType(expectedRef, actualType); ok {
		coerced, err := s.coerceValue(receiverValue, actualType, upcastType)
		if err != nil {
			return nil, nil, err
		}
		return coerced, upcastType, nil
	}
	if !semantic.AssignableTo(expectedRef.Elem, actualType) {
		return nil, nil, fmt.Errorf("UFCS receiver expects %s, got %s", expectedType.String(), actualType.String())
	}
	ptr, err := s.emitStackTempValue(receiverValue, actualType, name+".autoref")
	if err != nil {
		return nil, nil, err
	}
	return ptr, expectedType, nil
}
func (s *functionState) emitStoreRowFieldAddress(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Object == nil {
		return nil, nil, false, nil
	}
	objType := s.exprType(expr.Object)
	if objType == nil {
		return nil, nil, false, nil
	}
	rowType, rowValue, handled, err := s.emitStoreRowCarrierValue(expr.Object, objType, expr.Field)
	if err != nil || !handled {
		return nil, nil, handled, err
	}
	storePtr := C.LLVMBuildExtractValue(s.builder, rowValue, 0, cStringFree(expr.Field+".row.store"))
	indexValue := C.LLVMBuildExtractValue(s.builder, rowValue, 1, cStringFree(expr.Field+".row.index"))
	columnPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, rowType.Store, expr.Field)
	if err != nil {
		return nil, nil, true, err
	}
	darrayLLVMType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	elemPtr, elemType, err := s.emitRuntimePointerIndexedAddressWithType(columnPtr, darrayLLVMType, darrayType.Elem, indexValue)
	if err != nil {
		return nil, nil, true, err
	}
	return elemPtr, elemType, true, nil
}
func (s *functionState) emitStoreRowCarrierValue(expr ast.Expr, objType semantic.Type, fieldName string) (*semantic.StoreRowViewType, C.LLVMValueRef, bool, error) {
	switch tt := objType.(type) {
	case *semantic.StoreRowViewType:
		rowValue, _, err := s.emitExpr(expr, tt)
		if err != nil {
			return nil, nil, true, err
		}
		return tt, rowValue, true, nil
	case *semantic.RefType:
		rowType, ok := tt.Elem.(*semantic.StoreRowViewType)
		if !ok || rowType == nil {
			return nil, nil, false, nil
		}
		if tt.State != semantic.RefStateNonNull {
			return nil, nil, true, fmt.Errorf("field access requires proven non-null reference, got %s", objType.String())
		}
		rowPtr, _, err := s.emitExpr(expr, tt)
		if err != nil {
			return nil, nil, true, err
		}
		rowLLVMType, err := s.g.lowerType(rowType)
		if err != nil {
			return nil, nil, true, err
		}
		rowValue := C.LLVMBuildLoad2(s.builder, rowLLVMType, rowPtr, cStringFree(fieldName+".row.load"))
		return rowType, rowValue, true, nil
	default:
		return nil, nil, false, nil
	}
}
func (s *functionState) emitTreeFieldAddress(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	objType := s.exprType(expr.Object)
	baseType := semantic.StripAggregateStateType(objType)
	if refType, ok := baseType.(*semantic.RefType); ok {
		baseType = semantic.StripAggregateStateType(refType.Elem)
	}
	if _, ok := treeNodeHandleFamily(baseType); !ok {
		return nil, nil, false, nil
	}
	return nil, nil, true, fmt.Errorf("tree field address .%s is not supported for handle-lowered tree values", expr.Field)
}
func (s *functionState) packedEnumStoragePtrFromExprValue(value C.LLVMValueRef, exprType semantic.Type, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return value, nil
	}
	if refType, ok := exprType.(*semantic.RefType); ok {
		if refEnum, ok := refType.Elem.(*semantic.EnumType); ok && refEnum == enumType {
			handleValue, err := s.loadValue(value, enumType, "packed.enum.handle")
			if err != nil {
				return nil, err
			}
			return s.decodePackedEnumHandleWithStore(handleValue, enumType, store)
		}
	}
	return s.decodePackedEnumHandleWithStore(value, enumType, store)
}
func (s *functionState) emitAddressOrTemp(expr ast.Expr) (C.LLVMValueRef, semantic.Type, error) {
	if ptr, typ, err := s.emitAddress(expr); err == nil {
		return ptr, typ, nil
	}
	value, typ, err := s.emitExpr(expr, nil)
	if err != nil {
		return nil, nil, err
	}
	ptr, err := s.emitStackTempValue(value, typ, "addr.tmp")
	if err != nil {
		return nil, nil, err
	}
	return ptr, typ, nil
}
func (s *functionState) emitNodeTableIndexAddress(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	keyIndex, keyEnum, handled, err := s.emitNodeKeyIndexValue(expr.Index)
	if !handled {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, true, err
	}
	objType := s.exprType(expr.Object)
	containerType := objType
	pointerLike := false
	if refType, ok := objType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
		containerType = refType.Elem
		pointerLike = true
	}
	containerType = semantic.StripAggregateStateType(containerType)
	enumType, elemType, ok := semantic.NodeTableParts(containerType)
	if !ok || enumType == nil {
		return nil, nil, false, nil
	}
	if keyEnum != nil && keyEnum != enumType {
		return nil, nil, true, fmt.Errorf("node key enum %s does not match node table %s", keyEnum.Name, enumType.Name)
	}
	var tablePtr C.LLVMValueRef
	if pointerLike {
		tablePtr, _, err = s.emitExpr(expr.Object, objType)
	} else {
		tablePtr, _, err = s.emitAddressOrTemp(expr.Object)
	}
	if err != nil {
		return nil, nil, true, err
	}
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, nil, true, err
	}
	valuesPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, tablePtr, 0, cStringFree("node.table.values.ptr"))
	viewType := &semantic.ViewType{Elem: elemType, SurfaceName: "view"}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, nil, true, err
	}
	indexValue, err := s.coerceValue(keyIndex, s.g.result.NamedTypes["u32"], s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	ptr, elemType, err := s.emitRuntimePointerIndexedAddressWithType(valuesPtr, viewLLVMType, elemType, indexValue)
	return ptr, elemType, true, err
}
