package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// A container allocated in a scope-owned region must not escape via return
// (use-after-free); a container in a caller-provided region param may.
func TestRegionContainerReturnEscapeChecking(t *testing.T) {
	bad := analyzeTreeTestSourceWithSemanticErrors(t, "region_escape_bad.elisa", `def leak() -> darray[u8]:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            v: mutable darray[u8] @a = []
            v.push(65)
            return v
`)
	if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, "escapes via return") {
		t.Fatalf("expected scope-owned region container return to be rejected; got: %s", all)
	}

	ok := analyzeTreeTestSourceWithSemanticErrors(t, "region_escape_ok.elisa", `def keep[region r](v: mutable darray[u8] @r) -> darray[u8] @r:
    return v
`)
	if all := strings.Join(ok.Errors(), "\n"); strings.Contains(all, "escapes via return") {
		t.Fatalf("returning a caller-provided region-param container must be allowed; got: %s", all)
	}
}

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
