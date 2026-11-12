package semantic

import (
	"fmt"
	"strings"
	"testing"
)

func TestDefaultCallbackContributesErrorSet(t *testing.T) {
	const declarations = `module Provider:
    public:
        error Fault:
            Available
            Busy
        def available() -> i64 error[Provider::Fault{Available}]:
            raise Provider::Fault.Available
using Provider::available

def wrap[errorset R](callback: fn() -> i64 error[R] = available) -> i64 error[R]:
    return try callback()
`
	good := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "default_good.elisa", declarations+`
def caller() -> i64 error[Provider::Fault{Available}]:
    return wrap()
`, AnalyzeOptions{})
	if errs := good.Errors(); len(errs) != 0 {
		t.Fatalf("compatible default callback rejected: %v", errs)
	}
	bad := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "default_bad.elisa", declarations+`
def caller() -> i64 error[Provider::Fault{Busy}]:
    return wrap()
`, AnalyzeOptions{})
	if all := fmt.Sprint(bad.Errors()); !strings.Contains(all, "return type expects") {
		t.Fatalf("incompatible default callback was not propagated: %s", all)
	}
}

func TestDefaultCallbackStillChecksSignature(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "default_bad_signature.elisa", `error Fault:
    Bad

def source(value: i64) -> i64 error[Fault]:
    raise Fault.Bad

def wrap[errorset R](callback: fn() -> i64 error[R] = source) -> i64 error[R]:
    return try callback()
`, AnalyzeOptions{})
	if all := fmt.Sprint(result.Errors()); !strings.Contains(all, "default argument expects") {
		t.Fatalf("incompatible callback signature was accepted: %s", all)
	}
}
