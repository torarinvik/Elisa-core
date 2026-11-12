package backend

import (
	"strings"
	"testing"
)

// Frame-block capacity has the target's pointer width, including on wasm32.
func TestFrameBlockCapacityUsesTargetWordSize(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "frame_block.elisa", `def main() -> i64:
    values: darray[i64] = [0, 1, 2]
    return values[0]
`)
	for _, tc := range []struct{ target, want string }{
		{"wasm32-unknown-unknown", "i32"},
		{"arm64-apple-darwin", "i64"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			ir, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result, OptimizationLevel0, DefaultPackedLoweringProfile(), tc.target)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(ir, "\n") {
				if strings.Contains(line, "call void @arena_init_stack_block(") {
					if !strings.Contains(line, ", "+tc.want+" ") {
						t.Fatalf("incorrect capacity type: %s", line)
					}
					return
				}
			}
			t.Fatal("fixture did not emit frame-block initializer")
		})
	}
}
