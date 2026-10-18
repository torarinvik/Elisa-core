//go:build cgo

package backend

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIncrementalObjectByteBoundWorkingSetSurvivesUnitEdit(t *testing.T) {
	options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}
	dir := t.TempDir()
	base := incrementalObjectFixture(16)
	edited := strings.Replace(base, "return value + 1\n", "return value + 2\n", 1)
	session := NewIncrementalObjectSession(2, 1<<20)
	session.minWeight = 0
	out := filepath.Join(dir, "retained.o")
	write := func(filename, source string) {
		t.Helper()
		if err := session.WriteObject(parseAndAnalyzeBackendTest(t, filename, source), out, options); err != nil {
			t.Fatal(err)
		}
	}
	write("working_A.elisa", base)
	costA := session.Stats().RetainedBytes
	write("working_B.elisa", base)
	costB := session.Stats().RetainedBytes - costA
	// Measure the edited revision independently so the budget fits all current
	// partitions even if constant encoding changes their exact serialized size.
	probe := NewIncrementalObjectSession(2, 1<<20)
	probe.minWeight = 0
	if err := probe.WriteObject(parseAndAnalyzeBackendTest(t, "working_B.elisa", edited), filepath.Join(dir, "probe.o"), options); err != nil {
		t.Fatal(err)
	}
	editedCost := probe.Stats().RetainedBytes
	if editedCost > costB {
		costB = editedCost
	}
	session.maxBytes = costA + costB
	write("working_B.elisa", edited)
	if got := incrementalObjectNativeStatus(t, out); got != 39 {
		t.Fatalf("edited B status=%d want39", got)
	}
	before := session.Stats()
	write("working_A.elisa", base)
	after := session.Stats()
	if after.PartitionsEmitted != before.PartitionsEmitted || after.PartitionsReused-before.PartitionsReused != 2 {
		t.Fatalf("B edit displaced A current partitions: before=%+v after=%+v", before, after)
	}
	if got := incrementalObjectNativeStatus(t, out); got != 38 {
		t.Fatalf("unchanged A status=%d want38", got)
	}
	if after.RetainedBytes > session.maxBytes || after.SerialFallbacks != 0 {
		t.Fatalf("budget/fallback violation: %+v", after)
	}
}
