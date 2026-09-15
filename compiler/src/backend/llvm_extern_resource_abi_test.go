//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// A resource crosses the C boundary as its bare handle: by value (consuming), by
// reference (a borrow, loaded), and as a nullable return. docs/127 §3.2.
func TestGenerateLLVMIRPassesResourcesAsHandles(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "extern_resource.elisa", `
extern resource CFile
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
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("fixture must analyze, got:\n%s", strings.Join(errs, "\n"))
	}
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, want := range []string{
		"declare ptr @fopen(ptr, ptr)",
		"declare i32 @fclose(ptr)",
		"declare i32 @fgetc(ptr)",
		"call i32 @fgetc(ptr %extern.resource.handle",
		"call i32 @fclose(ptr %extern.resource.handle",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in IR, got:\n%s", want, output)
		}
	}
}
