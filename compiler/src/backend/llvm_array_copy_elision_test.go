//go:build cgo

package backend

import (
	"os"
	"strings"
	"testing"
)

// The pass is shared with Stage1; test/parity/array_copy_elision_smoke.py there
// runs the behavioral fixture against either compiler through ELISAC.
func TestArrayRoundTripElisionIsWired(t *testing.T) {
	for _, source := range []string{"parallel_emit.c", "llvm_target.go"} {
		if !strings.Contains(mustReadBackendSource(t, source), "elisacoreElideArrayRoundTrips") {
			t.Fatalf("%s does not run array round-trip elision", source)
		}
	}
}

func mustReadBackendSource(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
