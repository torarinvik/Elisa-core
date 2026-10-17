//go:build cgo

package semantic

import (
	"fmt"
	"testing"
)

const projectionShadowHelpers = `struct ProjectionLeaf:
    value: i64
    flag: bool
struct ProjectionOuter:
    inner: ProjectionLeaf

def projected_value(p: ProjectionOuter) -> i64:
    ensure result == p.inner.value
    return p.inner.value

def projected_flag(p: ProjectionOuter) -> bool:
    ensure result == p.inner.flag
    return p.inner.flag
`

func TestSMTProjectionBindingNestedIntegerCallerShadow(t *testing.T) {
	for _, expected := range []int{3, 99} {
		source := projectionShadowHelpers + fmt.Sprintf(`
def caller() -> i64:
    ensure result == %d
    p: ProjectionOuter = ProjectionOuter{inner: ProjectionLeaf{value: 99, flag: true}}
    return projected_value(ProjectionOuter{inner: ProjectionLeaf{value: 3, flag: false}})
`, expected)
		r := analyzeContractStrict(t, "nested_projection_shadow.elisa", source)
		if rejected := ensureFailuresFor(r.Errors(), "caller") != 0; rejected != (expected != 3) {
			t.Fatalf("expected %d: %s", expected, allDiagnostics(r))
		}
	}
}

func TestSMTProjectionBindingNestedBooleanCallerShadow(t *testing.T) {
	for _, expected := range []bool{false, true} {
		source := projectionShadowHelpers + fmt.Sprintf(`
def caller() -> bool:
    ensure result == %t
    p: ProjectionOuter = ProjectionOuter{inner: ProjectionLeaf{value: 99, flag: true}}
    return projected_flag(ProjectionOuter{inner: ProjectionLeaf{value: 3, flag: false}})
`, expected)
		r := analyzeContractStrict(t, "boolean_projection_shadow.elisa", source)
		if rejected := ensureFailuresFor(r.Errors(), "caller") != 0; rejected != expected {
			t.Fatalf("expected %t: %s", expected, allDiagnostics(r))
		}
	}
}
