//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func strictExternErrors(t *testing.T, name, src string) string {
	t.Helper()
	return strings.Join(analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{RequireExternContracts: true}).Errors(), "\n")
}

// D1: a `void&?` parameter is an untyped pointer and is rejected under -strict-externs even when
// the extern carries a contract.
func TestStrictExternsRejectsUntypedPointerParam(t *testing.T) {
	src := `extern adsr_process(envelope: mutable void&?, level: f64) -> f64 requires level >= 0.0`
	errs := strictExternErrors(t, "ext_d1_param.elisa", src)
	want := `extern function "adsr_process" parameter "envelope" is an untyped pointer (mutable void&?); declare an opaque handle with ` + "`extern Name`" + ` and use it instead of void, or mark the extern @trusted("reason")`
	if !strings.Contains(errs, want) {
		t.Fatalf("expected D1 for an untyped pointer parameter, got: %v", errs)
	}
}

// D1 on the return side.
func TestStrictExternsRejectsUntypedPointerReturn(t *testing.T) {
	src := `extern adsr_create(sustain: bool) -> mutable void&? ensure true`
	errs := strictExternErrors(t, "ext_d1_return.elisa", src)
	if !strings.Contains(errs, `extern function "adsr_create" returns an untyped pointer (mutable void&?)`) {
		t.Fatalf("expected D1 for an untyped pointer return, got: %v", errs)
	}
}

// An opaque handle is the fix: the same extern over `extern Adsr` passes D1 and, because the
// handle is typed on its own, needs no pointer coverage from the contract.
func TestStrictExternsAcceptsOpaqueHandle(t *testing.T) {
	src := `extern Adsr
extern adsr_process(envelope: mutable Adsr&, level: f64) -> f64 requires level >= 0.0`
	errs := strictExternErrors(t, "ext_d1_handle.elisa", src)
	if strings.Contains(errs, "untyped pointer") || strings.Contains(errs, "covers none") {
		t.Fatalf("an opaque handle must satisfy the pointer discipline, got: %v", errs)
	}
}

// @trusted("reason") is the only other way past D1.
func TestStrictExternsTrustedBypassesUntypedPointer(t *testing.T) {
	src := `@trusted("SDL user-data slot: opaque by design")
extern set_user(handle: mutable void&?) -> void`
	errs := strictExternErrors(t, "ext_d1_trusted.elisa", src)
	if strings.Contains(errs, "untyped pointer") {
		t.Fatalf("@trusted must bypass D1, got: %v", errs)
	}
}

// D12: presence is not coverage. A contract that names none of the pointer parameters is rejected.
func TestStrictExternsRejectsContractCoveringNoPointer(t *testing.T) {
	src := `extern memcpy(dest: mutable u8&, src: u8&, n: usize) -> void requires n > 0`
	errs := strictExternErrors(t, "ext_d12.elisa", src)
	want := `extern function "memcpy" has a contract that covers none of its pointer parameters (` + "`dest`, `src`" + `); under -strict-externs every pointer parameter must be named by the contract, be an opaque handle, or the extern must be @trusted("reason")`
	if !strings.Contains(errs, want) {
		t.Fatalf("expected D12, got: %v", errs)
	}
}

// Naming one pointer parameter in `requires` satisfies D12. `cstr` counts as a pointer.
func TestStrictExternsAcceptsContractNamingPointer(t *testing.T) {
	src := `extern strnlen(text: cstr, cap: usize) -> usize requires text != null ensure result <= cap`
	errs := strictExternErrors(t, "ext_d12_ok.elisa", src)
	if strings.Contains(errs, "covers none") {
		t.Fatalf("a contract naming a pointer parameter must satisfy D12, got: %v", errs)
	}
}

// Without -strict-externs none of this fires: existing binding files keep compiling.
func TestPointerDisciplineOffByDefault(t *testing.T) {
	src := `extern memcpy(dest: mutable void&, src: void&, n: usize) -> void requires n > 0`
	errs := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ext_default_ptr.elisa", src, AnalyzeOptions{}).Errors()
	if len(errs) != 0 {
		t.Fatalf("pointer discipline must be off without -strict-externs, got: %v", errs)
	}
}
