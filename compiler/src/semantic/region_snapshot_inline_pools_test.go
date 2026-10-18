package semantic

import (
	"os"
	"strings"
	"testing"
)

// An unparameterized snapshot can be inserted safely when every owned pool is
// explicitly in the destination region and the literal is formed in that caller.
func TestSnapshotInlineOwnedPools(t *testing.T) {
	for _, bad := range []bool{false, true} {
		name := "testdata/snapshot_region_generic_inline.elisa"
		if bad {
			name = "testdata/snapshot_region_generic_inline_bad_pool.elisa"
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "snapshot_pools.elisa", string(data), AnalyzeOptions{})
		if !bad && len(r.Errors()) != 0 {
			t.Fatal(r.Errors())
		}
		if bad {
			message := strings.Join(r.Errors(), "\n")
			if !strings.Contains(message, "scratch") || !strings.Contains(message, "longer-lived") {
				t.Fatalf("short-lived pool accepted or wrong failure: %v", r.Errors())
			}
		}
	}
}

// Keep the actual nine-pool snapshot schema in this regression: the minimal
// Packet control alone cannot demonstrate the production aggregate boundary.
func TestSnapshotExplicitRegionFactory(t *testing.T) {
	data, err := os.ReadFile("testdata/snapshot_region_generic_factory.elisa")
	if err != nil {
		t.Fatal(err)
	}
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "snapshot_factory.elisa", string(data), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
}
