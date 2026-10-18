//go:build cgo

package backend

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Retaining lowered definitions must not retain revoked public names. This also
// gates future caches that admit a function by body identity alone.
func TestIncrementalObjectExportRenameRemovalAndRevert(t *testing.T) {
	nm, err := exec.LookPath("nm")
	if err != nil {
		t.Fatal("native symbol qualification requires nm:", err)
	}
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	dir := t.TempDir()
	for revision, name := range []string{"api_old", "api_old", "api_new", "", "api_old"} {
		source := `@callconv(c)
@inline(never)
def work(value: i64) -> i64:
    return value + 1

def main() -> i64:
    return work(40)

`
		if name != "" {
			source += fmt.Sprintf("export fn %s(value: i64) -> i64 = work\n", name)
		}
		for i := 0; i < 16; i++ {
			source += fmt.Sprintf("def padding%d(value: i64) -> i64:\n    return value + %d\n\n", i, i)
		}
		result := parseAndAnalyzeBackendTest(t, "export_lifecycle.elisa", source)
		options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel2}
		before := session.Stats()
		retained := filepath.Join(dir, "retained.o")
		if err := session.WriteObject(result, retained, options); err != nil {
			t.Fatal(err)
		}
		after := session.Stats()
		if revision == 1 || revision == 4 {
			if after.PartitionsEmitted != before.PartitionsEmitted || after.PartitionsReused-before.PartitionsReused != 4 {
				t.Fatalf("revision%d did not reuse exact prior objects: before=%+v after=%+v", revision, before, after)
			}
		}
		if after.SerialFallbacks != 0 || after.RetainedBytes > session.maxBytes {
			t.Fatalf("unexpected cache state: %+v", after)
		}
		fresh := filepath.Join(dir, "fresh.o")
		if err := WriteLLVMObjectFileWithOptions(result, fresh, options); err != nil {
			t.Fatal(err)
		}
		for _, object := range []string{retained, fresh} {
			output, err := exec.Command(nm, "-g", object).CombinedOutput()
			if err != nil {
				t.Fatalf("inspect symbols: %v: %s", err, output)
			}
			defined := map[string]bool{}
			for _, line := range strings.Split(string(output), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && (fields[len(fields)-2] == "T" || fields[len(fields)-2] == "t") {
					defined[strings.TrimPrefix(fields[len(fields)-1], "_")] = true
				}
			}
			for _, candidate := range []string{"api_old", "api_new"} {
				if defined[candidate] != (candidate == name) {
					t.Fatalf("revision%d object=%s symbol=%s defined=%v want=%v", revision, object, candidate, defined[candidate], candidate == name)
				}
			}
			if got := incrementalObjectNativeStatus(t, object); got != 41 {
				t.Fatalf("revision%d object=%s status=%d want41", revision, object, got)
			}
		}
	}
}
