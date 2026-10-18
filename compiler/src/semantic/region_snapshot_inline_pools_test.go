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

func TestSnapshotFactoryByValueCanonicalizer(t *testing.T) {
	data, err := os.ReadFile("testdata/snapshot_region_canonicalizer.elisa")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []bool{false, true} {
		source := string(data)
		if bad {
			// Replacing one owned pool with the borrowed source descriptor must
			// invalidate the output-region contract even though all others are copied.
			start := strings.Index(source, "def clone_borrow")
			if start < 0 {
				t.Fatal("missing borrowed canonicalizer")
			}
			source = source[:start] + strings.Replace(source[start:], "bytes: bytes, source_path_start:", "bytes: snapshot.bytes, source_path_start:", 1)
		}
		r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "canonicalizer.elisa", source, AnalyzeOptions{})
		if !bad && len(r.Errors()) != 0 {
			t.Fatal(r.Errors())
		}
		if bad && !strings.Contains(strings.Join(r.Errors(), "\n"), "independent region parameter") {
			t.Fatalf("borrowed pool escaped fresh canonicalizer: %v", r.Errors())
		}
	}
}
