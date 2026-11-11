//go:build cgo && (darwin || linux)

package backend

import (
	"os"
	"strings"
	"testing"
)

func TestPowerPositionCleanupPropagatesEntryFacts(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	source, err := os.ReadFile("testdata/power_position_cleanup.ll")
	if err != nil {
		t.Fatal(err)
	}
	g, err := parseLLVMIRForSplitHalfTest(string(source))
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.ensureTargetMachine(); err != nil {
		t.Fatal(err)
	}
	if status := unrollBoundedDivisionLoops(g); status != 1 {
		t.Fatalf("cleanup status %d, want changed", status)
	}
	if err := simplifyLateLoopFacts(g); err != nil {
		t.Fatal(err)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	ir := g.printModule()
	if !strings.Contains(ir, "@llvm.ctlz.i64") {
		t.Fatalf("power search was not folded:\n%s", ir)
	}
	if strings.Contains(ir, "select i1") {
		t.Fatalf("redundant zero-exponent select survived guarded power search:\n%s", ir)
	}
}
