//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// A `view[T]` parameter of a C-ABI native extern lowers to (ptr, len); an extern without an
// explicit C calling convention keeps Elisa's %DynArrayView aggregate (it may be an Elisa
// function in another unit). docs/127 §3.3.
func TestGenerateLLVMIRSplitsViewParamsOfCABIExterns(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "extern_view_split.elisa", `
@callconv(c)
extern write(fd: i32, buf: view[u8]) -> isize

@callconv(c)
extern scale(samples: mutable view[f32], gain: f32) -> void

extern take_elisa(xs: view[f32]) -> usize

def main() -> i64:
    text: mutable darray[u8] = []
    text.push(104)
    written: isize = write(1, text[0:1])
    return written.i64()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("fixture must analyze, got:\n%s", strings.Join(errs, "\n"))
	}
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, want := range []string{
		"declare i64 @write(i32, ptr, i64)",
		"declare void @scale(ptr, i64, float)",
		"declare i64 @take_elisa(%DynArrayView)",
		"call i64 @write(i32 1, ptr %extern.view.ptr, i64 %extern.view.len)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in IR, got:\n%s", want, output)
		}
	}
}
