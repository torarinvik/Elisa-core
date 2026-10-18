//go:build cgo

package backend

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestIncrementalObjectOptimizationChangesAndReverts(t *testing.T) {
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	result := parseAndAnalyzeBackendTest(t, "options.elisa", incrementalObjectFixture(32))
	dir := t.TempDir()
	// Keep one cache across optimization changes. Identical optimized partitions
	// may share entries, while returning to an earlier pipeline must find its IR.
	for revision, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3, OptimizationLevel2, OptimizationLevel0, OptimizationLevel3} {
		t.Run(fmt.Sprintf("revision%d_O%d", revision, level), func(t *testing.T) {
			options := LLVMObjectEmitOptions{OptLevel: level}
			before := session.Stats()
			retained := filepath.Join(dir, "retained.o")
			if err := session.WriteObject(result, retained, options); err != nil {
				t.Fatal(err)
			}
			after := session.Stats()
			if revision == 1 && after.PartitionsEmitted == before.PartitionsEmitted {
				t.Fatal("O0 to O3 fixture failed to exercise changed partition IR")
			}
			if revision >= 3 && (after.PartitionsEmitted != before.PartitionsEmitted || after.PartitionsReused-before.PartitionsReused != 4) {
				t.Fatalf("returning to O%d failed to reuse prior partitions: before=%+v after=%+v", level, before, after)
			}
			if after.SerialFallbacks != 0 || after.RetainedBytes > session.maxBytes {
				t.Fatalf("unexpected fallback or cache overflow: %+v", after)
			}
			fresh := filepath.Join(dir, "fresh.o")
			if err := WriteLLVMObjectFileWithOptions(result, fresh, options); err != nil {
				t.Fatal(err)
			}
			if got, want := incrementalObjectNativeStatus(t, retained), incrementalObjectNativeStatus(t, fresh); got != 54 || want != 54 {
				t.Fatalf("retained=%d fresh=%d expected=54", got, want)
			}
		})
	}
}
