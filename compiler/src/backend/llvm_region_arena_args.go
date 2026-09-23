//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func regionTypeArgumentName(t semantic.Type) string {
	switch region := t.(type) {
	case *semantic.RegionParamType:
		if region != nil {
			return region.Name
		}
	case *semantic.RegionValueType:
		if region != nil {
			return region.Name
		}
	}
	return ""
}

// matchedRegionName finds the actual region paired with a callee region parameter
// in corresponding formal and argument types. It covers both container regions
// and nominal region-generic values, including opaque/phantom wrappers such as a
// JSON handle whose backing lifetime exists only in its type argument.
func matchedRegionName(formal, actual semantic.Type, regionParam string) string {
	if formal == nil || actual == nil || regionParam == "" {
		return ""
	}
	formal = semantic.StripAggregateStateType(formal)
	actual = semantic.StripAggregateStateType(actual)
	switch f := formal.(type) {
	case *semantic.RegionParamType:
		if f.Name == regionParam {
			return regionTypeArgumentName(actual)
		}
	case *semantic.RefType:
		if f.Region == regionParam {
			if a, ok := actual.(*semantic.RefType); ok && a.Region != "" {
				return a.Region
			}
		}
		if a, ok := actual.(*semantic.RefType); ok {
			return matchedRegionName(f.Elem, a.Elem, regionParam)
		}
	case *semantic.DArrayType:
		if f.Region == regionParam {
			if a, ok := actual.(*semantic.DArrayType); ok {
				return a.Region
			}
		}
		if a, ok := actual.(*semantic.DArrayType); ok {
			return matchedRegionName(f.Elem, a.Elem, regionParam)
		}
	case *semantic.DictType:
		if f.Region == regionParam {
			if a, ok := actual.(*semantic.DictType); ok {
				return a.Region
			}
		}
		if a, ok := actual.(*semantic.DictType); ok {
			if name := matchedRegionName(f.Key, a.Key, regionParam); name != "" {
				return name
			}
			return matchedRegionName(f.Value, a.Value, regionParam)
		}
	case *semantic.SetType:
		if f.Region == regionParam {
			if a, ok := actual.(*semantic.SetType); ok {
				return a.Region
			}
		}
		if a, ok := actual.(*semantic.SetType); ok {
			return matchedRegionName(f.Elem, a.Elem, regionParam)
		}
	case *semantic.ViewType:
		if f.Region == regionParam {
			if a, ok := actual.(*semantic.ViewType); ok {
				return a.Region
			}
		}
		if a, ok := actual.(*semantic.ViewType); ok {
			return matchedRegionName(f.Elem, a.Elem, regionParam)
		}
	case *semantic.SViewType:
		if f.Region == regionParam {
			if a, ok := actual.(*semantic.SViewType); ok {
				return a.Region
			}
		}
	case *semantic.CStrType:
		if f.Region == regionParam {
			if a, ok := actual.(*semantic.CStrType); ok {
				return a.Region
			}
		}
	case *semantic.GenericInstanceType:
		a, ok := actual.(*semantic.GenericInstanceType)
		if !ok || f.Name != a.Name || len(f.Args) != len(a.Args) {
			return ""
		}
		if base, ok := f.Base.(*semantic.StructType); ok {
			for index, param := range structGenericParams(base) {
				if index >= len(f.Args) || index >= len(a.Args) || param.Kind != ast.GenericParamRegion {
					continue
				}
				if regionTypeArgumentName(f.Args[index]) == regionParam {
					if name := regionTypeArgumentName(a.Args[index]); name != "" {
						return name
					}
				}
			}
		}
		for index := range f.Args {
			if name := matchedRegionName(f.Args[index], a.Args[index], regionParam); name != "" {
				return name
			}
		}
	case *semantic.OptionalType:
		if a, ok := actual.(*semantic.OptionalType); ok {
			return matchedRegionName(f.Value, a.Value, regionParam)
		}
	case *semantic.ErrorUnionType:
		if a, ok := actual.(*semantic.ErrorUnionType); ok {
			return matchedRegionName(f.Value, a.Value, regionParam)
		}
	case *semantic.ArrayType:
		if a, ok := actual.(*semantic.ArrayType); ok {
			return matchedRegionName(f.Elem, a.Elem, regionParam)
		}
	case *semantic.TupleType:
		if a, ok := actual.(*semantic.TupleType); ok && len(f.Fields) == len(a.Fields) {
			for index := range f.Fields {
				if name := matchedRegionName(f.Fields[index].Type, a.Fields[index].Type, regionParam); name != "" {
					return name
				}
			}
		}
	}
	return ""
}

func (s *functionState) regionArenaPointer(region string) C.LLVMValueRef {
	if s == nil || region == "" {
		return nil
	}
	if owner, ok := s.regionArenaOwner(region); ok {
		if arena, err := s.treeOwnerArenaRefValue(owner, region); err == nil && arena != nil {
			return arena
		}
	}
	if binding, ok := s.lookupBinding(region); ok && semantic.IsArenaValueOrRefType(binding.typ) {
		if ref, isRef := binding.typ.(*semantic.RefType); isRef && ref != nil && semantic.IsArenaValueOrRefType(ref.Elem) {
			ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
			return C.LLVMBuildLoad2(s.builder, ptrType, binding.ptr, cStringFree("region.arena."+region))
		}
		return binding.ptr
	}
	return nil
}
