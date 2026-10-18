//go:build cgo

package backend

import (
	"fmt"
	"path/filepath"
	"regexp"
	"testing"
)

// A private constant is shared across partitions. Its payload can change while
// every function body stays identical, so body-only reuse would be unsound.
func TestIncrementalObjectPrivateConstantEditRevert(t *testing.T) {
	session := NewIncrementalObjectSession(2, 1<<20)
	session.minWeight = 0
	dir := t.TempDir()
	for revision, value := range []int{20, 20, 21, 20} {
		source := fmt.Sprintf(`const XS = [10, %d, 30]

@callconv(c)
@inline(never)
def left(index: usize) -> usize:
    return (&XS[index]).cast[usize]

@callconv(c)
@inline(never)
def right(index: usize) -> usize:
    return (&XS[index]).cast[usize]

@callconv(c)
@inline(never)
def read(index: usize) -> i64:
    return XS[index]

export fn left(index: usize) -> usize = left
export fn right(index: usize) -> usize = right
export fn read(index: usize) -> i64 = read

def main() -> i64:
    if left(1) == right(1):
        return read(1)
    return 1
`, value)
		result := parseAndAnalyzeBackendTest(t, "constant_dependency.elisa", source)
		ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel2)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"left", "right", "read"} {
			if !regexp.MustCompile(`call [^\n]*@` + name + `\(`).MatchString(ir) {
				t.Fatalf("fixture optimized away %s call", name)
			}
		}
		before := session.Stats()
		retained := filepath.Join(dir, "retained.o")
		options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel2}
		if err := session.WriteObject(result, retained, options); err != nil {
			t.Fatal(err)
		}
		after := session.Stats()
		if revision == 0 {
			assertNativeFunctionsInDifferentShards(t, session, "left", "right")
		}
		emitted, reused := after.PartitionsEmitted-before.PartitionsEmitted, after.PartitionsReused-before.PartitionsReused
		if (revision == 1 || revision == 3) && (emitted != 0 || reused != 2) {
			t.Fatalf("revision%d: emitted=%d reused=%d", revision, emitted, reused)
		}
		if revision == 2 && emitted == 0 {
			t.Fatal("constant edit reused all old partitions")
		}
		if after.SerialFallbacks != 0 || after.RetainedBytes > session.maxBytes {
			t.Fatalf("unexpected cache state: %+v", after)
		}
		fresh := filepath.Join(dir, "fresh.o")
		if err := WriteLLVMObjectFileWithOptions(result, fresh, options); err != nil {
			t.Fatal(err)
		}
		for _, object := range []string{retained, fresh} {
			if got := incrementalObjectNativeStatus(t, object); got != value {
				t.Fatalf("revision%d %s: got%d want%d", revision, object, got, value)
			}
		}
	}
}
