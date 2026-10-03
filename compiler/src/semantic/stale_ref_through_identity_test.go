package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An interior reference passed through an identity helper (`idf(&buf[0])`, generic `gid`)
// borrows buf's buffer exactly like `&buf[0]`; a view read at the top of a loop body and only
// assigned further down is read stale on the next iteration once the body grows its backing.
// Both used to be accepted with no diagnostic (fuzz finding F17).
func TestStaleRefThroughIdentityNegative(t *testing.T) {
	dir := filepath.Join("testdata", "stale_ref_through_identity")
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

func TestStaleRefThroughIdentityPositive(t *testing.T) {
	name := "stale_ref_through_identity.pos.elisa"
	source, err := os.ReadFile(filepath.Join("testdata", "stale_ref_through_identity", name))
	if err != nil {
		t.Fatal(err)
	}
	result := analyzeTreeTestSourceWithSemanticErrors(t, name, string(source))
	for _, e := range result.Errors() {
		t.Errorf("%s rejected: %s", name, e)
	}
}
