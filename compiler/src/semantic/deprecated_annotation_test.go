package semantic

import (
	"strings"
	"testing"
)

// A `@deprecated("msg")` function emits a deprecation diagnostic at each use site (here a UFCS
// call). The annotation is pure metadata, so it is allowed on a generic function and one that
// requires permissions, and it does not block compilation.
func TestAnalyzeEmitsDeprecatedFunctionDiagnostic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "deprecated_fn.elisa", `
@deprecated("use the new thing")
def myold[T](da: darray[T], y: i64) -> i64:
    return y

def use_it(d: darray[i64]) -> i64:
    return d.myold(3)
`)
	deps := strings.Join(result.Deprecations(), "\n")
	if !strings.Contains(deps, "use the new thing") {
		t.Fatalf("expected deprecation diagnostic at the call site, got deprecations:\n%s", deps)
	}
}
