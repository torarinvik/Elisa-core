//go:build cgo

package backend

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A partitioned -O3 build must import small cross-partition callees without any
// flag. Without imports a call across partitions is not inlined, which costs runtime.
func TestPartitionedO3ImportsByDefault(t *testing.T) {
	var source strings.Builder
	source.WriteString("def h0(x: i64) -> i64:\n    return (x * 3 + 1) % 1000003\n")
	for k := 1; k < 80; k++ {
		fmt.Fprintf(&source, "def h%d(x: i64) -> i64:\n    return (h%d(x) * 3 + %d) %% 1000003\n", k, k-1, k)
	}
	source.WriteString("def main() -> i64:\n    return h79(7) % 251\n")
	emit := func(t *testing.T, imports *string) []byte {
		t.Helper()
		if imports != nil {
			t.Setenv("ELISACORE_IR_IMPORTS", *imports)
		} else if previous, had := os.LookupEnv("ELISACORE_IR_IMPORTS"); had {
			os.Unsetenv("ELISACORE_IR_IMPORTS")
			t.Cleanup(func() { os.Setenv("ELISACORE_IR_IMPORTS", previous) })
		}
		t.Setenv("ELISACORE_PARALLEL_IR", "1")
		t.Setenv("ELISACORE_PARALLEL_OPT", "1")
		result := parseAndAnalyzeBackendTest(t, "import_default_fixture.elisa", source.String())
		session := NewIncrementalObjectSession(8, 32<<20)
		session.minWeight = 0
		out := filepath.Join(t.TempDir(), "out.o")
		options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, PackedProfile: DefaultPackedLoweringProfile()}
		disposal := newLLVMDisposalScope(1)
		done, err := session.tryPartitionedObject(result, out, options, disposal)
		disposal.wait()
		if err != nil {
			t.Fatal(err)
		}
		if !done {
			t.Fatal("fixture declined the partitioned IR path")
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	on, off := "1", "0"
	def := emit(t, nil)
	explicitOn := emit(t, &on)
	explicitOff := emit(t, &off)
	if !bytes.Equal(def, explicitOn) {
		t.Fatalf("default partitioned -O3 object differs from ELISACORE_IR_IMPORTS=1 (%d vs %d bytes)", len(def), len(explicitOn))
	}
	if bytes.Equal(explicitOn, explicitOff) {
		t.Fatal("fixture does not exercise cross-partition imports: imports on and off produce identical objects")
	}
}
