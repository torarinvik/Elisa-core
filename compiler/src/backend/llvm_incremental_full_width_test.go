//go:build cgo

package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Exit statuses alone lose the high 56 bits of a native result. Keep the C ABI
// boundary observable and compare full-width values with an independent oracle.
func TestIncrementalObjectFullWidthKernelParity(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	counts := []uint64{0, 1, 2, 7, 31, 64, 255}
	seeds := []uint64{0, 1, ^uint64(0), 1 << 63, 0xaaaaaaaaaaaaaaaa}
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel2, OptimizationLevel3} {
		t.Run(fmt.Sprintf("O%d", level), func(t *testing.T) {
			dir := t.TempDir()
			harness := filepath.Join(dir, "harness.c")
			var c strings.Builder
			c.WriteString("#include <stdint.h>\n#include <stdio.h>\nextern uint64_t kernel(uint64_t, uint64_t);\nint main(void) {\n")
			for _, n := range counts {
				for _, seed := range seeds {
					fmt.Fprintf(&c, "printf(\"%%llu\\n\", (unsigned long long)kernel(UINT64_C(%d), UINT64_C(%d)));\n", n, seed)
				}
			}
			c.WriteString("return 0;\n}\n")
			if err := os.WriteFile(harness, []byte(c.String()), 0600); err != nil {
				t.Fatal(err)
			}
			session := NewIncrementalObjectSession(4, 1<<20)
			session.minWeight = 0
			const original uint64 = 11400714819323198485
			// Change only a high bit: a low-byte-only result check can miss this.
			for revision, multiplier := range []uint64{original, original ^ (1 << 48), original} {
				source := fmt.Sprintf(`@callconv(c)
@inline(never)
def kernel(n: u64, seed: u64) -> u64:
    acc: mutable u64 = seed
    while i < n |i: u64 = 0, acc|:
        acc <- (acc + i * %du64) ^ (acc >> 13)
        i <- i + 1
    return acc

export fn kernel(n: u64, seed: u64) -> u64 = kernel

`, multiplier)
				// Leave independent functions for partial native reuse after the edit.
				for i := 0; i < 16; i++ {
					source += fmt.Sprintf("def untouched%d(x: u64) -> u64:\n    return x + %du64\n\n", i, i)
				}
				result := parseAndAnalyzeBackendTest(t, "full_width.elisa", source)
				options := LLVMObjectEmitOptions{OptLevel: level}
				retained := filepath.Join(dir, "retained.o")
				before := session.Stats()
				if err := session.WriteObject(result, retained, options); err != nil {
					t.Fatal(err)
				}
				if revision > 0 && session.Stats().PartitionsReused == before.PartitionsReused {
					t.Fatal("edit/revert did not exercise native partition reuse")
				}
				fresh := filepath.Join(dir, "fresh.o")
				if err := WriteLLVMObjectFileWithOptions(result, fresh, options); err != nil {
					t.Fatal(err)
				}
				for _, object := range []string{retained, fresh} {
					executable := object + ".exe"
					if output, err := exec.Command(clang, harness, object, "-o", executable).CombinedOutput(); err != nil {
						t.Fatalf("link: %v\n%s", err, output)
					}
					output, err := exec.Command(executable).Output()
					if err != nil {
						t.Fatal(err)
					}
					values := strings.Fields(string(output))
					if len(values) != len(counts)*len(seeds) {
						t.Fatalf("got %d outputs", len(values))
					}
					index := 0
					for _, n := range counts {
						for _, seed := range seeds {
							want := seed
							for i := uint64(0); i < n; i++ {
								want = (want + i*multiplier) ^ (want >> 13)
							}
							got, err := strconv.ParseUint(values[index], 10, 64)
							if err != nil || got != want {
								t.Fatalf("revision=%d object=%s n=%d seed=%x: got %x err=%v want %x", revision, object, n, seed, got, err, want)
							}
							index++
						}
					}
				}
			}
		})
	}
}
