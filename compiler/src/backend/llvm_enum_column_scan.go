//go:build cgo

package backend

// #include <stdlib.h>
// #include "llvm-c/Core.h"
import "C"

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// resolveColumnScanStore recovers the enum type and active packed store backing
// a first-class column scan `Expr of .field` (docs/76 §5). The store is the
// implicit region-backed store in scope, exactly as a storeless match recovers
// it.
func (s *functionState) resolveColumnScanStore(colExpr *ast.EnumColumnExpr) (*semantic.EnumType, *packedStoreOps, error) {
	if colExpr == nil {
		return nil, nil, fmt.Errorf("nil column scan expression")
	}
	named := s.g.result.NamedTypes[colExpr.Enum]
	enumType, ok := semantic.StripAggregateStateType(named).(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("column scan over %q requires a packed enum type", colExpr.Enum)
	}
	store, ok := s.lookupPackedStore(enumType)
	if !ok {
		return nil, nil, fmt.Errorf("column scan `%s of .%s` has no active %s store in scope; build the store (e.g. inside `in auto:`) before scanning it", colExpr.Enum, colExpr.Field, colExpr.Enum)
	}
	ops, ok := s.packedStoreOpsFromBinding(&store)
	if !ok {
		return nil, nil, fmt.Errorf("column scan `%s of .%s` could not resolve store operations", colExpr.Enum, colExpr.Field)
	}
	return enumType, ops, nil
}

// emitEnumColumnScanCount returns the row count of the scanned store — the loop
// bound for `for x in Expr of .field`.
func (s *functionState) emitEnumColumnScanCount(colExpr *ast.EnumColumnExpr, name string) (C.LLVMValueRef, error) {
	_, ops, err := s.resolveColumnScanStore(colExpr)
	if err != nil {
		return nil, err
	}
	return ops.storeCount(name + ".col.count")
}

// emitEnumColumnScanElement reads the column value for the node at indexValue:
// the tag, or a dense common(...) field. Per-variant payload fields are sparse
// and rejected earlier in analysis.
func (s *functionState) emitEnumColumnScanElement(colExpr *ast.EnumColumnExpr, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	enumType, ops, err := s.resolveColumnScanStore(colExpr)
	if err != nil {
		return nil, nil, err
	}

	if colExpr.Field == "tag" {
		stateValue, err := ops.stateValue(name + ".col.state")
		if err != nil {
			return nil, nil, err
		}
		tagRaw, err := ops.loadIndexSOATagDirect(stateValue, indexValue, name+".col.tag")
		if err != nil {
			return nil, nil, err
		}
		tagType := enumType.TagType
		if tagType == nil {
			return nil, nil, fmt.Errorf("enum %q has no tag type for column scan", enumType.Name)
		}
		coerced, err := s.coerceValue(tagRaw, s.g.result.NamedTypes["u32"], tagType)
		if err != nil {
			return nil, nil, err
		}
		return coerced, tagType, nil
	}

	layout, err := s.g.packedEnumCommonFieldLayout(enumType, colExpr.Field)
	if err != nil {
		return nil, nil, err
	}
	if !layout.StoredInline {
		return nil, nil, fmt.Errorf("column scan `%s of .%s` over a side-tabled common field is not yet supported", colExpr.Enum, colExpr.Field)
	}
	fieldOffsetBytes, ok, err := s.packedEnumDirectFieldByteOffset(enumType, layout.RowFieldIndex)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("column scan `%s of .%s` could not resolve the column byte offset", colExpr.Enum, colExpr.Field)
	}
	// The dense index is the node handle for the index-SoA store; the read
	// helpers coerce it to the store's handle width internally.
	handleValue, err := s.coerceValue(indexValue, s.g.result.NamedTypes["usize"], enumType)
	if err != nil {
		return nil, nil, err
	}
	value, err := s.emitPackedDirectFieldReadAtOrigin(ops, handleValue, enumType, layout.Field.Type, fieldOffsetBytes, packedReadOriginKey{}, name+".col.field")
	if err != nil {
		return nil, nil, err
	}
	return value, layout.Field.Type, nil
}
