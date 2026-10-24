//go:build cgo

package backend

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestStructGenericParamsAvoidsUnneededCopies(t *testing.T) {
	for _, base := range []*semantic.StructType{
		nil,
		{},
		{GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}}},
		{GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}, {Kind: ast.GenericParamRegion, Name: "r"}}, RegionParams: []string{"r"}},
	} {
		params := structGenericParams(base)
		if base != nil && !reflect.DeepEqual(params, base.GenericParams) {
			t.Fatal("complete parameter list changed")
		}
		if allocations := testing.AllocsPerRun(1000, func() {
			if len(structGenericParams(base)) != len(params) {
				panic("complete parameter count changed")
			}
		}); allocations != 0 {
			t.Fatalf("complete parameter query allocated %g objects", allocations)
		}
	}
}

func TestStructGenericParamsSynthesizesMissingRegionsAndLegacyState(t *testing.T) {
	base := &semantic.StructType{GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}}, RegionParams: []string{"r"}}
	params := structGenericParams(base)
	want := []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}, {Kind: ast.GenericParamRegion, Name: "r"}}
	if !reflect.DeepEqual(params, want) {
		t.Fatal("missing region synthesis changed", params)
	}
	params[0].Name = "changed"
	if base.GenericParams[0].Name != "T" || len(base.GenericParams) != 1 {
		t.Fatal("synthesis modified the semantic declaration")
	}
	legacy := &semantic.StructType{Name: "Box", TypeParams: []string{"T"}, RegionParams: []string{"r"}, NamedStateCases: []string{"ready"}}
	params = structGenericParams(legacy)
	want = append(want, ast.GenericParam{Kind: ast.GenericParamState, Name: "state", StateCases: []string{"ready"}, StateOwner: "Box"})
	if !reflect.DeepEqual(params, want) {
		t.Fatal("legacy parameter/state order changed", params)
	}
	params[2].StateCases[0] = "changed"
	if legacy.NamedStateCases[0] != "ready" {
		t.Fatal("synthesized state cases alias the declaration")
	}
}
