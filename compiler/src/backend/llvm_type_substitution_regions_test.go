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

func TestFunctionTypeSubstitutionPreservesImplicitStoreABI(t *testing.T) {
	integer := &semantic.BuiltinType{Name: "i64"}
	source := &semantic.FuncType{
		Name: "worker", Params: []semantic.Type{&semantic.TypeParamType{Name: "T"}, &semantic.TypeParamType{Name: "Store"}},
		ExplicitParamCount: 1, ExplicitParamNames: []string{"value"}, ImplicitParamNames: []string{"__packed_store_Node"}, Return: integer,
	}
	got, ok := substituteType(source, map[string]semantic.Type{"T": integer, "Store": integer}, nil).(*semantic.FuncType)
	if !ok || len(got.Params) != 2 || got.Params[0] != integer || got.Params[1] != integer || got.ExplicitParamCount != 1 || len(got.ExplicitParamNames) != 1 || got.ExplicitParamNames[0] != "value" || len(got.ImplicitParamNames) != 1 || got.ImplicitParamNames[0] != "__packed_store_Node" {
		t.Fatalf("substitution lost callback ABI: %+v", got)
	}
	got.ImplicitParamNames[0] = "changed"
	got.ExplicitParamNames[0] = "changed"
	if source.ImplicitParamNames[0] != "__packed_store_Node" || source.ExplicitParamNames[0] != "value" {
		t.Fatal("substitution aliased the original parameter names")
	}
}
