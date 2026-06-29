//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"elisacore/src/semantic"
	"fmt"
)

func iterLoopItemTypeBackend(t semantic.Type) (semantic.Type, bool) {
	switch tt := t.(type) {
	case *semantic.ArrayType:
		if tt.SurfaceName == "str" || tt.SurfaceName == "string" {
			return nil, false
		}
		return tt.Elem, true
	case *semantic.DArrayType:
		return tt.Elem, true
	case *semantic.DictType:
		return semantic.DictEntryTupleType(tt), true
	case *semantic.ViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "view" {
			return nil, false
		}
		return tt.Elem, true
	case *semantic.StoreRowsViewType:
		return &semantic.StoreRowViewType{Store: tt.Store}, true
	case *semantic.GenericInstanceType:
		if itemType, ok := semantic.TreeAttributeSequenceItemType(tt); ok {
			return itemType, true
		}
		if itemType, ok := semantic.TreeKindFilteredViewItemType(tt); ok {
			return itemType, true
		}
		if itemType, ok := semantic.ChunksExactViewItemType(tt); ok {
			return itemType, true
		}
		return nil, false
	case *semantic.RefType:
		if tt.State != semantic.RefStateNonNull {
			return nil, false
		}
		return iterLoopItemTypeBackend(tt.Elem)
	default:
		return nil, false
	}
}
func isIterLoopRuntimeStringViewType(t semantic.Type) bool {
	st, ok := t.(*semantic.StructType)
	return ok && st != nil && st.Name == "StringView"
}
func (s *functionState) emitStoreRowsCarrierStorePtr(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (*semantic.StoreRowsViewType, C.LLVMValueRef, error) {
	switch tt := sourceType.(type) {
	case *semantic.StoreRowsViewType:
		carrierValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".rows")
		if err != nil {
			return nil, nil, err
		}
		storePtr := C.LLVMBuildExtractValue(s.builder, carrierValue, 0, cStringFree(sourceName+".rows.store"))
		return tt, storePtr, nil
	case *semantic.RefType:
		rowsType, ok := tt.Elem.(*semantic.StoreRowsViewType)
		if !ok || rowsType == nil {
			return nil, nil, fmt.Errorf("unsupported store rows carrier %s", sourceType.String())
		}
		if tt.State != semantic.RefStateNonNull {
			return nil, nil, fmt.Errorf("iterable loop requires a non-null reference source, got %s", sourceType.String())
		}
		carrierPtr, err := s.loadValue(sourceAlloca, sourceType, sourceName+".rows.ref")
		if err != nil {
			return nil, nil, err
		}
		rowsLLVMType, err := s.g.lowerType(rowsType)
		if err != nil {
			return nil, nil, err
		}
		storeFieldPtr := C.LLVMBuildStructGEP2(s.builder, rowsLLVMType, carrierPtr, 0, cStringFree(sourceName+".rows.store.ptr"))
		storePtr, err := s.loadValue(storeFieldPtr, &semantic.RefType{Elem: rowsType.Store, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}, sourceName+".rows.store")
		if err != nil {
			return nil, nil, err
		}
		return rowsType, storePtr, nil
	default:
		return nil, nil, fmt.Errorf("unsupported store rows carrier %s", sourceType.String())
	}
}
func (s *functionState) emitStoreRowsCount(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (C.LLVMValueRef, error) {
	rowsType, storePtr, err := s.emitStoreRowsCarrierStorePtr(sourceAlloca, sourceType, sourceName)
	if err != nil {
		return nil, err
	}
	if rowsType.Store == nil || len(rowsType.Store.StoreFieldOrder) == 0 {
		usizeType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
		if err != nil {
			return nil, err
		}
		return C.LLVMConstInt(usizeType, 0, 0), nil
	}
	firstField := rowsType.Store.StoreFieldOrder[0]
	columnPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, rowsType.Store, firstField)
	if err != nil {
		return nil, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(columnPtr, darrayType)
	if err != nil {
		return nil, err
	}
	return s.loadValue(countPtr, usizeType, sourceName+".rows.count")
}
func (s *functionState) emitStoreRowItemValue(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (C.LLVMValueRef, semantic.Type, error) {
	rowsType, storePtr, err := s.emitStoreRowsCarrierStorePtr(sourceAlloca, sourceType, sourceName)
	if err != nil {
		return nil, nil, err
	}
	rowType := &semantic.StoreRowViewType{Store: rowsType.Store}
	rowLLVMType, err := s.g.lowerType(rowType)
	if err != nil {
		return nil, nil, err
	}
	rowValue := C.LLVMGetUndef(rowLLVMType)
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, storePtr, 0, cStringFree(sourceName+".row.store"))
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, indexValue, 1, cStringFree(sourceName+".row.index"))
	return rowValue, rowType, nil
}
