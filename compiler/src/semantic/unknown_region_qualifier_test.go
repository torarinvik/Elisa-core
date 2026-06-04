package semantic

import (
	"strings"
	"testing"
)

// An unknown region name in a `@r` provenance annotation on a container silently
// bypassed the nested-region escape check (`darray[u8] @misnamed = inner_value` was
// accepted where the same store tagged `@outer` is rejected). It must be diagnosed as
// an unknown region, exactly as a `T& @r` reference already is.
func TestUnknownRegionQualifierOnContainerRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "unknown_region_container.elisa", `def f() -> void:
    region outer(4096):
        region inner(4096):
            small: mutable darray[u8] @inner = []
            stash: mutable darray[u8] @misnamed = small
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, `unknown region qualifier "misnamed"`) {
		t.Fatalf("expected unknown-region diagnostic for @misnamed container; got:\n%s", all)
	}
}

// Same for an unknown region on a function parameter's container type.
func TestUnknownRegionQualifierOnParamRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "unknown_region_param.elisa", `def f(xs: darray[u8] @ghost) -> usize:
    return xs.count
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, `unknown region qualifier "ghost"`) {
		t.Fatalf("expected unknown-region diagnostic for @ghost param; got:\n%s", all)
	}
}

// A valid region name must still flow through to the escape check (no false positive
// from the new validation shadowing the real diagnostic).
func TestValidRegionQualifierStillReachesEscapeCheck(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "valid_region_escape.elisa", `def f() -> void:
    region outer(4096):
        region inner(4096):
            small: mutable darray[u8] @inner = []
            stash: mutable darray[u8] @outer = small
`)
	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, "unknown region qualifier") {
		t.Fatalf("valid region name @outer must not be flagged unknown; got:\n%s", all)
	}
	if !strings.Contains(all, "freed first") {
		t.Fatalf("expected nested-region escape diagnostic for @outer store; got:\n%s", all)
	}
}
