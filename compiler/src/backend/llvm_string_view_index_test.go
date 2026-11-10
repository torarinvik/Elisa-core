//go:build cgo && (darwin || linux)

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOptimizedStringViewIndexPreservesBounds(t *testing.T) {
	if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
		t.Skip("requires a 64-bit target")
	}
	result := parseAndAnalyzeBackendTest(t, "sview_index_bounds.elisa", `struct StringView:
    data: u8&
    len: i64

def read_at(data: u8&, length: i64, index: i64) -> i64:
    trusted Unsafe.PointerCast:
        view: sview = StringView{data: data, len: length}
        trusted Unsafe.UncheckedIndex:
            return view[index].i64()

export fn read_at(data: u8&, length: i64, index: i64) -> i64 = read_at
`)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ir, "@ctx_string_view_index") {
		t.Fatal("optimized read still calls the string view indexing helper")
	}

	dir := t.TempDir()
	irPath, driverPath := filepath.Join(dir, "view.ll"), filepath.Join(dir, "driver.c")
	for path, contents := range map[string]string{irPath: ir, driverPath: `
#include <stdint.h>
extern int64_t read_at(const unsigned char *data, int64_t len, int64_t index);
int main(void) {
    static const unsigned char bytes[] = {0x80, 0, 0xff};
    return read_at(bytes, 3, 0) != 128 || read_at(bytes, 3, 1) != 0 ||
           read_at(bytes, 3, 2) != 255 || read_at((const unsigned char *)1, 0, 0) != 0 ||
           read_at((const unsigned char *)1, -1, 0) != 0 || read_at((const unsigned char *)1, 3, 3) != 0 ||
           read_at((const unsigned char *)1, 3, -1) != 0;
}
`} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(dir, "probe")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "clang", "-O3", driverPath, irPath, "-o", executable).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil {
		t.Fatalf("view bounds: %v\n%s", err, out)
	}
}
