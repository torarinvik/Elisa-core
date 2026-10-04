package semantic

import (
	"strings"
	"testing"
)

func TestNestedReferenceMutabilityAppliesOnlyAtItsLayer(t *testing.T) {
	readonlyOuter := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "readonly_outer_ref.elisa", `def replace(pointer: (mutable i32&?) &?) -> void:
    next: mutable i32 = 2
    pointer <- &next
`, AnalyzeOptions{})
	if diagnostics := strings.Join(readonlyOuter.Errors(), "\n"); !strings.Contains(diagnostics, "readonly ref") {
		t.Fatalf("inner mutability must not grant write access through the readonly outer reference, got:\n%s", diagnostics)
	}

}
