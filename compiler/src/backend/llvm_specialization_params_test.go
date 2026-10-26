//go:build cgo

package backend

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestFunctionSpecializationParamsWithoutRegions(t *testing.T) {
	want := []ast.GenericParam{
		{Kind: ast.GenericParamType, Name: "T"},
		{Kind: ast.GenericParamValue, Name: "N"},
	}
	base := &semantic.FuncType{
		GenericParams: want,
		Params: []semantic.Type{
			&semantic.ArrayType{Elem: &semantic.RefType{Elem: &semantic.ArrayType{Elem: &semantic.RegionParamType{Name: "unrelated"}}}},
		},
		Return: &semantic.OptionalType{Value: &semantic.RegionParamType{Name: "also_unrelated"}},
	}
	if got := functionSpecializationParams(base); !reflect.DeepEqual(got, want) {
		t.Fatalf("functionSpecializationParams without layout regions = %#v, want %#v", got, want)
	}

	legacy := &semantic.FuncType{TypeParams: []string{"A", "B"}}
	legacyWant := []ast.GenericParam{
		{Kind: ast.GenericParamType, Name: "A"},
		{Kind: ast.GenericParamType, Name: "B"},
	}
	if got := functionSpecializationParams(legacy); !reflect.DeepEqual(got, legacyWant) {
		t.Fatalf("functionSpecializationParams for legacy type params = %#v, want %#v", got, legacyWant)
	}
}

func TestFunctionSpecializationParamsFindsNestedLayoutRegion(t *testing.T) {
	box := &semantic.StructType{
		Name:         "RegionBox",
		RegionParams: []string{"r"},
		GenericParams: []ast.GenericParam{
			{Kind: ast.GenericParamRegion, Name: "r"},
		},
	}
	instance := &semantic.GenericInstanceType{
		Base: box,
		Args: []semantic.Type{&semantic.RegionParamType{Name: "r"}},
	}
	base := &semantic.FuncType{
		GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}},
		RegionParams:  []string{"r", "unused"},
		Params: []semantic.Type{
			&semantic.ArrayType{Elem: &semantic.RefType{Elem: &semantic.OptionalType{Value: instance}}},
		},
	}
	want := []ast.GenericParam{
		{Kind: ast.GenericParamType, Name: "T"},
		{Kind: ast.GenericParamRegion, Name: "r"},
	}
	if got := functionSpecializationParams(base); !reflect.DeepEqual(got, want) {
		t.Fatalf("functionSpecializationParams for nested layout region = %#v, want %#v", got, want)
	}
}
