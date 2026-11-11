//go:build cgo

package backend

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func (s *functionState) emitFlagsIndexExpr(expr *ast.IndexExpr, flagType *semantic.ConstEnumType) (llvmIRValueRef, semantic.Type, error) {
	if expr == nil || flagType == nil {
		return nil, nil, fmt.Errorf("missing flags index metadata")
	}
	flagsValue, flagsType, err := s.emitExpr(expr.Object, nil)
	if err != nil {
		return nil, nil, err
	}
	flagsType = semantic.StripAggregateStateType(flagsType)
	if ref, ok := flagsType.(*semantic.RefType); ok && ref != nil {
		loaded, err := s.loadValue(flagsValue, ref.Elem, "flags")
		if err != nil {
			return nil, nil, err
		}
		flagsValue = loaded
		flagsType = semantic.StripAggregateStateType(ref.Elem)
	}
	if _, ok := semantic.FlagsInstanceType(flagsType); !ok {
		return nil, nil, fmt.Errorf("flags membership indexing requires Flags[T], got %s", flagsType)
	}
	bitsValue := llvmBuildExtractValue(s.builder, flagsValue, 0, cStringFree("flags.bits"))
	indexValue, indexType, err := s.emitExpr(expr.Index, flagType)
	if err != nil {
		return nil, nil, err
	}
	u64Type := s.g.namedType("u64")
	indexValue, err = s.coerceValue(indexValue, indexType, u64Type)
	if err != nil {
		return nil, nil, err
	}
	u64LLVMType, err := s.g.lowerType(u64Type)
	if err != nil {
		return nil, nil, err
	}
	one := llvmConstInt(u64LLVMType, 1, 0)
	zero := llvmConstInt(u64LLVMType, 0, 0)
	maskValue := llvmBuildShl(s.builder, one, indexValue, cStringFree("flags.mask"))
	hitValue := llvmBuildAnd(s.builder, bitsValue, maskValue, cStringFree("flags.hit"))
	boolValue := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), hitValue, zero, cStringFree("flags.has"))
	return boolValue, s.g.namedType("bool"), nil
}
