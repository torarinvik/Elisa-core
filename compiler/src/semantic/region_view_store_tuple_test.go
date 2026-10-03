package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A view built inside `region NAME:` and stored with `<-` into an outer place through a tuple
// literal, a tuple-returning call (whole or one field) or a struct literal dangles after the
// block. The same fixtures run through stage1 (test/fixtures/semantic/adversarial_escape).
func TestRegionViewStoreThroughTupleRejected(t *testing.T) {
	dir := filepath.Join("testdata", "region_view_store")
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

// A tuple-returning call over an OUTER source is only rebound, never escapes.
func TestRegionViewStoreOuterSourceTupleAccepted(t *testing.T) {
	name := "region_view_store_tuple_call_outer_source.pos.elisa"
	source, err := os.ReadFile(filepath.Join("testdata", "region_view_store", name))
	if err != nil {
		t.Fatal(err)
	}
	result := analyzeTreeTestSourceWithSemanticErrors(t, name, string(source))
	for _, e := range result.Errors() {
		t.Errorf("%s rejected: %s", name, e)
	}
}
