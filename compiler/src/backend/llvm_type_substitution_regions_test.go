//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/semantic"
)

func TestTypeSubstitutionPreservesAndResolvesRegionMetadata(t *testing.T) {
	bindings := map[string]semantic.Type{
		"r": &semantic.RegionValueType{Name: "input_region"},
	}
	tests := []struct {
		name string
		typ  semantic.Type
		want string
	}{
		{"reference", &semantic.RefType{Elem: &semantic.SViewType{Region: "r"}, Mutable: true, Linear: true, Region: "r"}, "input_region"},
		{"darray", &semantic.DArrayType{Elem: &semantic.SViewType{Region: "r"}, Region: "r"}, "input_region"},
		{"view", &semantic.ViewType{Elem: &semantic.TypeParamType{Name: "T"}, Mutable: true, Region: "r"}, "input_region"},
		{"cstr", &semantic.CStrType{Region: "r"}, "input_region"},
		{"string view", &semantic.SViewType{Region: "r"}, "input_region"},
		{"dict", &semantic.DictType{Key: &semantic.TypeParamType{Name: "K"}, Value: &semantic.TypeParamType{Name: "V"}, Region: "r"}, "input_region"},
		{"set", &semantic.SetType{Elem: &semantic.TypeParamType{Name: "T"}, Region: "r"}, "input_region"},
		{"generic instance", &semantic.GenericInstanceType{Name: "Box", Args: []semantic.Type{&semantic.RegionParamType{Name: "r"}}, Region: "r"}, "input_region"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := substituteType(tt.typ, bindings, nil)
			if region := substitutedTypeRegion(got); region != tt.want {
				t.Fatalf("region = %q, want %q (type %T)", region, tt.want, got)
			}
			if tt.name == "darray" {
				darray := got.(*semantic.DArrayType)
				if region := substitutedTypeRegion(darray.Elem); region != tt.want {
					t.Fatalf("nested element region = %q, want %q", region, tt.want)
				}
			}
			if tt.name == "reference" {
				ref := got.(*semantic.RefType)
				if !ref.Mutable || !ref.Linear {
					t.Fatalf("substitution dropped reference mutability metadata: mutable=%t linear=%t", ref.Mutable, ref.Linear)
				}
			}
			if tt.name == "view" && !got.(*semantic.ViewType).Mutable {
				t.Fatal("substitution dropped mutable-view metadata")
			}
		})
	}
}

func substitutedTypeRegion(typ semantic.Type) string {
	switch value := typ.(type) {
	case *semantic.RefType:
		return value.Region
	case *semantic.DArrayType:
		return value.Region
	case *semantic.ViewType:
		return value.Region
	case *semantic.CStrType:
		return value.Region
	case *semantic.SViewType:
		return value.Region
	case *semantic.DictType:
		return value.Region
	case *semantic.SetType:
		return value.Region
	case *semantic.GenericInstanceType:
		return value.Region
	default:
		return ""
	}
}
