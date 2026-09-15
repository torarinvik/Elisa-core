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
	if strings.Contains(errs, "untyped pointer") || strings.Contains(errs, "not covered") {
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
	for _, want := range []string{
		`extern function "memcpy" pointer parameter "dest" is not covered by its contract; under -strict-externs every pointer parameter must be named by a ` + "`requires`/`ensure`" + ` clause, be an opaque handle, or the extern must be @trusted("reason")`,
		`extern function "memcpy" pointer parameter "src" is not covered by its contract`,
	} {
		if !strings.Contains(errs, want) {
			t.Fatalf("expected D12 per uncovered parameter, got: %v", errs)
		}
	}
}

// Naming every pointer parameter satisfies D12. `cstr` counts as a pointer.
func TestStrictExternsAcceptsContractNamingPointer(t *testing.T) {
	src := `extern strnlen(text: cstr, cap: usize) -> usize requires text != null ensure result <= cap`
	errs := strictExternErrors(t, "ext_d12_ok.elisa", src)
	if strings.Contains(errs, "not covered") {
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


// D5: a scalar reference beside an integer parameter is a pointer with no bounds.
func TestStrictExternsRejectsUnboundedScalarRef(t *testing.T) {
	src := `extern read(fd: i32, buf: mutable u8&, count: usize) -> isize requires buf != null`
	errs := strictExternErrors(t, "ext_d5.elisa", src)
	want := `extern function "read" parameter "buf" is a pointer with no bounds; declare it as mutable view[u8], bind a length with @bounds(buf, <length>), or mark the extern @trusted("reason")`
	if !strings.Contains(errs, want) {
		t.Fatalf("expected D5, got: %v", errs)
	}
}

// Not D5: a struct reference is one object; a lone scalar reference is an out-parameter;
// a @bounds pair has become a view.
func TestStrictExternsAcceptsBoundedShapes(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"struct_ref", "struct Rect layout(c):\n    w: i32\n\nextern area(r: Rect&, scale: i32) -> i32 requires r != null"},
		{"out_param", "extern get_len(out: mutable i64&) -> void requires out != null"},
		{"bounds", "@callconv(c)\n@bounds(buf, count)\nextern read(fd: i32, buf: mutable u8&, count: usize) -> isize requires buf.count > 0"},
	} {
		errs := strictExternErrors(t, "ext_d5_"+tc.name+".elisa", tc.src)
		if strings.Contains(errs, "no bounds") {
			t.Fatalf("%s must not trigger D5, got: %v", tc.name, errs)
		}
	}
}
