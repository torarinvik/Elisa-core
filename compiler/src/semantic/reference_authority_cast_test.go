package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Forging a reference from an integer and upgrading a read-only reference to a mutable one
// require Unsafe.PointerCast in EVERY mode; sound reborrows stay legal. The same fixtures run
// through stage1 (test/repro/reference_authority_cast/).
func TestReferenceAuthorityCastRejected(t *testing.T) {
	dir := filepath.Join("testdata", "reference_authority_cast")
	expect, err := os.ReadFile(filepath.Join(dir, "EXPECT"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(expect)), "\n") {
		name, want, _ := strings.Cut(line, "\t")
		source, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		result := analyzeTreeTestSourceWithSemanticErrors(t, name, string(source))
		if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, want) {
			t.Errorf("%s: want %q, got:\n%s", name, want, got)
		}
	}
}

func TestReferenceAuthorityCastAccepted(t *testing.T) {
	name := "reference_authority_cast.pos.elisa"
	source, err := os.ReadFile(filepath.Join("testdata", "reference_authority_cast", name))
	if err != nil {
		t.Fatal(err)
	}
	result := analyzeTreeTestSourceWithSemanticErrors(t, name, string(source))
	for _, e := range result.Errors() {
		t.Errorf("%s rejected: %s", name, e)
	}
}
