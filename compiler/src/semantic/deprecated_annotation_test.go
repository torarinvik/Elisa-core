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

func TestAnalyzeStringBuilderSurfaceDeprecations(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "deprecated_string_builder.elisa", `
struct StringBuilder:
    len: mutable i64

@deprecated("StringBuilder is legacy runtime glue; prefer rt_concat2_scratch(...) plus existing value-to-string conversions like rt_int_to_string(...)")
def rt_string_builder_new(initial: u8&?) -> mutable heap StringBuilder&:
    _ = initial
    return zeroed

@deprecated("StringBuilder is legacy runtime glue; prefer rt_concat2_scratch(...) plus existing value-to-string conversions like rt_int_to_string(...)")
def rt_string_builder_append(builder: mutable heap StringBuilder&?, suffix: u8&?) -> mutable heap StringBuilder&:
    _ = builder
    _ = suffix
    return zeroed

@deprecated("StringBuilder is legacy runtime glue; prefer rt_concat2_scratch(...) plus existing value-to-string conversions like rt_int_to_string(...)")
def rt_string_builder_finish(builder: heap StringBuilder&?) -> cstr:
    _ = builder
    return ""

def build() -> cstr:
    builder: mutable heap StringBuilder& = rt_string_builder_new("ASC")
    builder <- rt_string_builder_append(builder, "[")
    return rt_string_builder_finish(builder)
`)
	deps := strings.Join(result.Deprecations(), "\n")
	if strings.Count(deps, "StringBuilder is legacy runtime glue") < 3 {
		t.Fatalf("expected deprecations for new/append/finish, got:\n%s", deps)
	}
}
