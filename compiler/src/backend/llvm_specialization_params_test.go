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

func TestSharedFunctionSpecializationParamsReuseAcrossGenerators(t *testing.T) {
	box := &semantic.StructType{
		Name:          "RegionBox",
		RegionParams:  []string{"r"},
		GenericParams: []ast.GenericParam{{Kind: ast.GenericParamRegion, Name: "r"}},
	}
	base := &semantic.FuncType{
		GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}},
		RegionParams:  []string{"r", "unused"},
		Params: []semantic.Type{
			&semantic.RefType{Elem: &semantic.GenericInstanceType{
				Base: box,
				Args: []semantic.Type{&semantic.RegionParamType{Name: "r"}},
			}},
		},
	}
	want := []ast.GenericParam{
		{Kind: ast.GenericParamType, Name: "T"},
		{Kind: ast.GenericParamRegion, Name: "r"},
	}
	shared := &sharedCodegenLookups{}
	first := (&llvmGenerator{sharedLookups: shared}).functionSpecializationParams(base)
	second := (&llvmGenerator{sharedLookups: shared}).functionSpecializationParams(base)
	if !reflect.DeepEqual(first, want) || !reflect.DeepEqual(second, want) {
		t.Fatalf("shared specialization params = %#v / %#v, want %#v", first, second, want)
	}
	if len(first) == 0 || &first[0] != &second[0] {
		t.Fatal("partitions did not reuse the immutable derived parameter slice")
	}
}

func BenchmarkFunctionSpecializationParams(b *testing.B) {
	box := &semantic.StructType{
		Name:          "RegionBox",
		RegionParams:  []string{"r"},
		GenericParams: []ast.GenericParam{{Kind: ast.GenericParamRegion, Name: "r"}},
	}
	var nested semantic.Type = &semantic.GenericInstanceType{
		Base: box,
		Args: []semantic.Type{&semantic.RegionParamType{Name: "r"}},
	}
	for range 12 {
		nested = &semantic.RefType{Elem: &semantic.OptionalType{Value: &semantic.ArrayType{Elem: nested}}}
	}
	base := &semantic.FuncType{
		GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}},
		RegionParams:  []string{"r", "unused"},
		Params:        []semantic.Type{nested},
		Return:        nested,
	}
	b.Run("recompute", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = functionSpecializationParams(base)
		}
	})
	b.Run("shared-cache", func(b *testing.B) {
		g := &llvmGenerator{sharedLookups: &sharedCodegenLookups{}}
		_ = g.functionSpecializationParams(base)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = g.functionSpecializationParams(base)
		}
	})
}
