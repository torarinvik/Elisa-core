//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func externResourceErrors(t *testing.T, name, src string) string {
	t.Helper()
	return strings.Join(analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{}).Errors(), "\n")
}

// D2: an extern resource must declare its destructor.
func TestExternResourceRequiresDrop(t *testing.T) {
	errs := externResourceErrors(t, "res_nodrop.elisa", "extern resource NoDrop\n")
	want := "extern resource \"NoDrop\" declares no `__drop__`; add `def __drop__(self: NoDrop)` in this module so the native handle is released on every exit path"
	if !strings.Contains(errs, want) {
		t.Fatalf("expected D2, got: %v", errs)
	}
}

// D3: an extern never returns a borrowed resource.
func TestExternResourceRejectsBorrowedReturn(t *testing.T) {
	src := "extern resource Handle\ndef __drop__(self: Handle) -> void:\n    pass\n\n@callconv(c)\nextern peek() -> Handle&\n"
	errs := externResourceErrors(t, "res_borrowed_return.elisa", src)
	if !strings.Contains(errs, `extern function "peek" returns a borrowed resource handle Handle&; return Handle (owned) so the caller releases it, or Handle? when the call can fail`) {
		t.Fatalf("expected D3, got: %v", errs)
	}
}

// The happy shape: owned optional constructor, borrowed use, consuming release in __drop__.
func TestExternResourceSignaturesAccepted(t *testing.T) {
	src := `extern resource CFile
def __drop__(self: CFile) -> void:
    _ = fclose(move self)

@callconv(c)
extern fopen(path: cstr, mode: cstr) -> CFile?
@callconv(c)
extern fclose(file: CFile) -> i32
@callconv(c)
extern fgetc(file: CFile&) -> i32

def first_byte(path: cstr) -> i64:
    file: CFile = get fopen(path, "r") else return -1
    return fgetc(file).i64()
`
	if errs := externResourceErrors(t, "res_ok.elisa", src); errs != "" {
		t.Fatalf("resource shapes must analyze, got: %v", errs)
	}
}
