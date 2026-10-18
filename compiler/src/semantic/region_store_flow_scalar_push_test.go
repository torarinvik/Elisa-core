package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestStoreFlowGenericDarrayPushFiltersKnownScalarProvenance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     Type
		retains bool
	}{
		{"scalar", &BuiltinType{Name: "u8"}, false},
		{"borrowed", &ViewType{Elem: &BuiltinType{Name: "u8"}}, true},
		{"unknown", nil, true},
		{"type_parameter", &TypeParamType{Name: "T"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &ast.Ident{Name: "target"}
			// Model a scalar computed from two borrowed sources. Their names may
			// occur in the expression without their backing being stored by push.
			elem := &ast.BinaryExpr{Left: &ast.Ident{Name: "left"}, Right: &ast.Ident{Name: "right"}}
			call := &ast.CallExpr{Func: &ast.FieldExpr{Object: receiver, Field: "push"}, Args: []ast.Expr{elem}}
			a := &Analyzer{
				storeFlowDarrays: map[string]bool{"target": true},
				exprTypes:        map[ast.Expr]Type{receiver: &DArrayType{Elem: &TypeParamType{Name: "E"}}, elem: tc.typ},
			}
			c := &storeFlowCtx{written: map[string]bool{"target": true}, scalars: map[string]bool{}}
			ret, handled := a.applyCallStoreFlow(call, c, map[*ast.CallExpr][]string{})
			if !handled {
				t.Fatal("builtin push was not handled")
			}
			if !tc.retains {
				if len(c.edges) != 0 || !reflect.DeepEqual(ret, []string{"target"}) || c.pu.find("left") == c.pu.find("right") {
					t.Fatalf("scalar computation must not retain or unite source storage: edges=%v result=%v sources=%v", c.edges, ret, c.pu.parent)
				}
			} else {
				if len(c.edges) == 0 || c.pu.find("left") != c.pu.find("right") || !reflect.DeepEqual(ret, []string{"target", "left", "right"}) {
					t.Fatalf("borrowed or unknown element lost conservative storage flow: edges=%v result=%v", c.edges, ret)
				}
			}
		})
	}
}
