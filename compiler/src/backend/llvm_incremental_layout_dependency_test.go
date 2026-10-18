//go:build cgo

package backend

import (
	"fmt"
	"path/filepath"
	"testing"
)

// Type declarations affect lowered bodies even when function source is stable:
// field indices, layout constants, and aggregate calling conventions can change.
func TestIncrementalObjectNominalLayoutEditRevert(t *testing.T) {
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel2} {
		t.Run(fmt.Sprintf("O%d", level), func(t *testing.T) {
			session := NewIncrementalObjectSession(4, 1<<20)
			session.minWeight = 0
			dir := t.TempDir()
			for revision, padding := range []bool{false, false, true, false} {
				field := ""
				want := 31
				if padding {
					field = "    padding: i64 = 0\n"
					want = 47
				}
				source := fmt.Sprintf(`struct Pair layout(c):
    left: i64
%s    right: i64

@inline(never)
def read(value: Pair) -> i64:
    return value.right

@inline(never)
def layout_total() -> usize:
    return size_of[Pair]() + offset_of[Pair](.right)

def main() -> i64:
    return read(Pair{left: 1, right: 7}) + i64(layout_total())
`, field)
				result := parseAndAnalyzeBackendTest(t, "layout_dependency.elisa", source)
				options := LLVMObjectEmitOptions{OptLevel: level}
				before := session.Stats()
				retained := filepath.Join(dir, "retained.o")
				if err := session.WriteObject(result, retained, options); err != nil {
					t.Fatal(err)
				}
				after := session.Stats()
				emitted, reused := after.PartitionsEmitted-before.PartitionsEmitted, after.PartitionsReused-before.PartitionsReused
				if (revision == 1 || revision == 3) && (emitted != 0 || reused != 4) {
					t.Fatalf("revision%d: emitted%d reused%d", revision, emitted, reused)
				}
				if revision == 2 && emitted == 0 {
					t.Fatal("layout edit reused all old native partitions")
				}
				if after.SerialFallbacks != 0 || after.RetainedBytes > session.maxBytes {
					t.Fatalf("unexpected cache state: %+v", after)
				}
				fresh := filepath.Join(dir, "fresh.o")
				if err := WriteLLVMObjectFileWithOptions(result, fresh, options); err != nil {
					t.Fatal(err)
				}
				for _, object := range []string{retained, fresh} {
					if got := incrementalObjectNativeStatus(t, object); got != want {
						t.Fatalf("revision%d %s: got%d want%d", revision, object, got, want)
					}
				}
			}
		})
	}
}
