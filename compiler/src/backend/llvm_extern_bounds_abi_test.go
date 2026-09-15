//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// `@bounds` keeps the C prototype's order: the pointer and length land where the native
// signature declares them, even with other parameters around them. docs/127 §3.3.
func TestGenerateLLVMIRHonoursBoundsPlan(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "extern_bounds.elisa", `
@callconv(c)
@bounds(buf, count)
extern read(fd: i32, buf: mutable u8&, count: usize) -> isize

@callconv(c)
@bounds(text, cap)
extern strnlen(text: u8&, cap: usize) -> usize

def main() -> i64:
    t: mutable darray[u8] = []
    t.push(0)
    got: isize = read(0, t[0:1])
    return got.i64() + strnlen(t[0:1]).i64()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("fixture must analyze, got:\n%s", strings.Join(errs, "\n"))
	}
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, want := range []string{
		"declare i64 @read(i32, ptr, i64)",
		"declare i64 @strnlen(ptr, i64)",
		"call i64 @read(i32 0, ptr %extern.view.ptr, i64 %extern.view.len)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in IR, got:\n%s", want, output)
		}
	}
}
