//go:build cgo

package backend

import (
	"fmt"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestIncrementalObjectRotateEditRevertFullWidth(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	values := []uint64{0, 1, ^uint64(0), 1 << 63, 0x123456789abcdef0, 1 << 48}
	amounts := []uint64{0, 1, 63, 64, 65, 127, 255, 1 << 48, ^uint64(0)}
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel2, OptimizationLevel3} {
		t.Run(fmt.Sprintf("O%d", level), func(t *testing.T) {
			dir := t.TempDir()
			harness := filepath.Join(dir, "harness.c")
			var c strings.Builder
			c.WriteString("#include <stdint.h>\n#include <stdio.h>\nextern uint64_t rotate_left(uint64_t,uint64_t);\nint main(void){\n")
			for _, value := range values {
				for _, amount := range amounts {
					fmt.Fprintf(&c, "printf(\"%%llu\\n\",(unsigned long long)rotate_left(UINT64_C(%d),UINT64_C(%d)));\n", value, amount)
				}
			}
			c.WriteString("return 0;}\n")
			if err := os.WriteFile(harness, []byte(c.String()), 0600); err != nil {
				t.Fatal(err)
			}
			session := NewIncrementalObjectSession(4, 1<<20)
			session.minWeight = 0
			for revision, offset := range []uint64{0, 0, 17, 0} {
				source := fmt.Sprintf(`@callconv(c)
@inline(never)
def rotate_left(value: u64, amount: u64) -> u64:
    shift: u64 = (amount + %d) & 63
    return (value << shift) | (value >> ((64 - shift) & 63))

export fn rotate_left(value: u64, amount: u64) -> u64 = rotate_left

`, offset)
				for i := 0; i < 16; i++ {
					source += fmt.Sprintf("def untouched%d(x: u64) -> u64:\n    return x + %d\n\n", i, i)
				}
				result := parseAndAnalyzeBackendTest(t, "rotate_edit.elisa", source)
				options := LLVMObjectEmitOptions{OptLevel: level}
				retained := filepath.Join(dir, "retained.o")
				before := session.Stats()
				if err := session.WriteObject(result, retained, options); err != nil {
					t.Fatal(err)
				}
				after := session.Stats()
				emitted, reused := after.PartitionsEmitted-before.PartitionsEmitted, after.PartitionsReused-before.PartitionsReused
				if revision == 1 || revision == 3 {
					if emitted != 0 || reused != 4 {
						t.Fatalf("revision %d must reuse all retained partitions: emitted=%d reused=%d", revision, emitted, reused)
					}
				} else if revision == 2 && (emitted == 0 || reused == 0) {
					t.Fatalf("edit must emit changed code and reuse unchanged code: emitted=%d reused=%d", emitted, reused)
				}
				if after.SerialFallbacks != 0 || after.RetainedBytes > session.maxBytes {
					t.Fatalf("unexpected fallback or cache overflow: %+v", after)
				}
				fresh := filepath.Join(dir, "fresh.o")
				if err := WriteLLVMObjectFileWithOptions(result, fresh, options); err != nil {
					t.Fatal(err)
				}
				for _, object := range []string{retained, fresh} {
					exe := object + ".exe"
					if output, err := exec.Command(clang, harness, object, "-o", exe).CombinedOutput(); err != nil {
						t.Fatalf("link: %v %s", err, output)
					}
					output, err := exec.Command(exe).Output()
					if err != nil {
						t.Fatal(err)
					}
					got := strings.Fields(string(output))
					if len(got) != len(values)*len(amounts) {
						t.Fatalf("unexpected output count: %d", len(got))
					}
					index := 0
					for _, value := range values {
						for _, amount := range amounts {
							native, err := strconv.ParseUint(got[index], 10, 64)
							want := bits.RotateLeft64(value, int((amount+offset)&63))
							if err != nil || native != want {
								t.Fatalf("revision=%d object=%s value=%x amount=%x got=%x want=%x err=%v", revision, object, value, amount, native, want, err)
							}
							index++
						}
					}
				}
			}
		})
	}
}
