package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// A darray region param `@r` must bind to the argument's region at a call and
// substitute through (mirroring RefType region unification).
func TestDArrayRegionUnificationAndSubstitution(t *testing.T) {
	a := &Analyzer{}
	i64 := &BuiltinType{Name: "i64"}
	pattern := &DArrayType{Elem: i64, Shape: &WildcardShape{}, SurfaceName: "darray", Region: "r"}
	actual := &DArrayType{Elem: i64, Shape: &WildcardShape{}, SurfaceName: "darray", Region: "a"}

	regionBindings := map[string]string{}
	a.collectTypeBindings(
		pattern, actual,
		map[string]Type{}, map[string]Shape{}, regionBindings,
		map[string][]ast.PermissionRef{}, map[string]bool{"r": true},
	)
	if regionBindings["r"] != "a" {
		t.Fatalf("region binding: want r->a, got %q", regionBindings["r"])
	}

	out := a.substituteType(pattern, map[string]Type{}, map[string]Shape{}, regionBindings, map[string][]ast.PermissionRef{})
	da, ok := out.(*DArrayType)
	if !ok || da.Region != "a" {
		t.Fatalf("substitute: want darray @a, got %#v", out)
	}

	// A non-param region name must NOT bind (only declared region params unify).
	notParam := map[string]string{}
	a.collectTypeBindings(
		pattern, actual,
		map[string]Type{}, map[string]Shape{}, notParam,
		map[string][]ast.PermissionRef{}, map[string]bool{},
	)
	if _, bound := notParam["r"]; bound {
		t.Fatalf("region %q is not a declared param; must not bind, got %q", "r", notParam["r"])
	}
}
