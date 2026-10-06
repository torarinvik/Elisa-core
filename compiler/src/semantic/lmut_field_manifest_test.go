package semantic

import (
	"strings"
	"testing"
)

// docs/120 §8 field-path arg-manifest: `report.cache <- bump(report.cache)` threads an lmut
// sub-struct of a mutable root exactly like the identifier form `c <- bump(c)`.
const lmutFieldManifestPrelude = `struct Cache:
    hits: mutable i64 = 0
    slots: mutable darray[i64] = []

struct Report:
    cache: mutable Cache = Cache{}
    total: mutable i64 = 0

def bump(cache: lmut Cache) -> void:
    cache.hits <- cache.hits + 10
    cache.slots <- cache.slots.push(7)
`

func TestLmutFieldPathManifestClean(t *testing.T) {
	analyzeTreeTestSource(t, "lmut_field_ok.elisa", lmutFieldManifestPrelude+`
def main() -> i64:
    report: mutable Report = Report{}
    report.cache.hits <- report.cache.hits + 1
    report.cache.slots <- report.cache.slots.push(5)
    report.cache <- bump(report.cache)
    copy: Report = Report{..report, total: 100}
    return report.cache.hits + report.cache.slots.count.i64() * 100 + copy.total * 1000
`)
}

// The target and the lmut argument must be the same rooted place.
func TestLmutFieldPathManifestMismatchedTargetRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_field_mismatch.elisa", lmutFieldManifestPrelude+`
def main() -> i64:
    report: mutable Report = Report{}
    report.total <- bump(report.cache)
    return report.total
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot assign void to i64") {
		t.Fatalf("expected a mismatched field manifest to be rejected, got: %s", all)
	}
}

// The root must be a mutable binding.
func TestLmutFieldPathManifestImmutableRootRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_field_immut.elisa", lmutFieldManifestPrelude+`
def main() -> i64:
    report: Report = Report{}
    report.cache <- bump(report.cache)
    return report.total
`)
	if len(result.Errors()) == 0 {
		t.Fatalf("expected an immutable-root field manifest to be rejected")
	}
}

// A bare call on a field place gets the same §10 treatment as an identifier, naming the place.
func TestLmutFieldPathBareCallFlagged(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_field_bare.elisa", lmutFieldManifestPrelude+`
def main() -> i64:
    report: mutable Report = Report{}
    bump(report.cache)
    return report.total
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "must be a reassignment") || !strings.Contains(all, "`report.cache <- …`") {
		t.Fatalf("expected §10 bare-mutation error naming report.cache, got: %s", all)
	}
}
