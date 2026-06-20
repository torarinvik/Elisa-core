//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Without -strict-externs, an uncontracted extern is accepted (the default — programs link many
// uncontracted libc/host externs, so blanket enforcement would break them).
func TestUncontractedExternAllowedByDefault(t *testing.T) {
	src := `extern raw(x: i64) -> i64`
	if errs := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ext_default.elisa", src, AnalyzeOptions{}).Errors(); len(errs) != 0 {
		t.Fatalf("an uncontracted extern must be fine without -strict-externs, got: %v", errs)
	}
}

// Under -strict-externs, an extern with NEITHER a contract NOR @trusted is rejected — its native
// boundary is unspecified.
func TestStrictExternsRejectsUncontracted(t *testing.T) {
	src := `extern raw(x: i64) -> i64`
	errs := strings.Join(analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ext_strict.elisa", src, AnalyzeOptions{RequireExternContracts: true}).Errors(), "\n")
	if !strings.Contains(errs, "has no contract") {
		t.Fatalf("an uncontracted extern must be rejected under -strict-externs, got: %v", errs)
	}
}

// A `requires` boundary contract satisfies the discipline.
func TestStrictExternsAcceptsContracted(t *testing.T) {
	src := `extern checked(a: i64, b: i64) -> i64 requires b != 0`
	for _, e := range analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ext_contract.elisa", src, AnalyzeOptions{RequireExternContracts: true}).Errors() {
		if strings.Contains(e, "has no contract") {
			t.Fatalf("a contracted extern must satisfy -strict-externs, got: %v", e)
		}
	}
}

// An explicit `@trusted("reason")` satisfies the discipline (documented trust in lieu of a contract).
func TestStrictExternsAcceptsTrustedWithReason(t *testing.T) {
	src := `@trusted("libc abort: never returns; no caller obligations")
extern host_abort() -> void`
	for _, e := range analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ext_trusted.elisa", src, AnalyzeOptions{RequireExternContracts: true}).Errors() {
		if strings.Contains(e, "has no contract") {
			t.Fatalf("a @trusted extern must satisfy -strict-externs, got: %v", e)
		}
	}
}

// `@trusted` with no reason is always an error (the trust must never be silent), independent of the flag.
func TestTrustedExternRequiresReason(t *testing.T) {
	src := `@trusted
extern host_thing(x: i64) -> i64`
	errs := strings.Join(analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ext_noreason.elisa", src, AnalyzeOptions{}).Errors(), "\n")
	if !strings.Contains(errs, "requires exactly one non-empty reason") {
		t.Fatalf("@trusted without a reason must be rejected, got: %v", errs)
	}
}
