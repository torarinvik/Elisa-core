package semantic

import (
	"strings"
	"testing"
)

// Every statement walker must descend into `region`/`scope`/`can`/`trusted` bodies — and into
// the auto-region the compiler wraps around an allocating body. A walker that lacked the arm
// skipped the whole body: these checks went silent exactly where real programs allocate.
func regionBlockWalkerExpect(t *testing.T, name, src, want string) {
	t.Helper()
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name+".elisa", src, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, want) {
		t.Errorf("%s: missing %q; got:\n%s", name, want, all)
	}
}

func TestRegionBlockWalkerSegment(t *testing.T) {
	regionBlockWalkerExpect(t, "guest_region", `
extern guest_only() -> void can[Segment.Guest]

def run() -> void:
    region r(64):
        can Segment.Guest:
            guest_only()
`, `segment owner mismatch: call to "guest_only"`)
	regionBlockWalkerExpect(t, "guest_auto", `
extern guest_only() -> void can[Segment.Guest]

def run() -> i64:
    can Memory.Allocate:
        xs: darray[u8] = [1.u8()]
        can Segment.Guest:
            guest_only()
        return xs.count.i64()
`, `segment owner mismatch: call to "guest_only"`)
	regionBlockWalkerExpect(t, "agnostic_region", `
extern host_only() -> void can[Segment.Host]

@segment_agnostic
def alarm_handler() -> void:
    region r(64):
        can Segment.Host:
            host_only()
`, `@segment_agnostic code cannot call "host_only"`)
	regionBlockWalkerExpect(t, "reentrant_region", `
def ordinary() -> void:
    return

@reentrant_safe
def alarm_helper() -> void:
    region r(64):
        ordinary()
`, `@reentrant_safe code cannot call "ordinary"`)
}

func TestRegionBlockWalkerE4(t *testing.T) {
	for _, w := range []string{"", "can Abort.Panic:", "region rr(64):", "trusted Unsafe.PointerCast:"} {
		body := "            total <- total + x\n"
		if w != "" {
			body = "            " + w + "\n                total <- total + x\n"
		}
		regionBlockWalkerExpect(t, "e4_"+w, `
def f(xs: darray[i64]) -> i64:
    total: mutable i64 = 0
    r: i64 =
        for x in xs |acc = 0| -> acc:
`+body+`            acc <- acc + 1
    return total + r
`, `value block may not mutate`)
	}
}

func TestRegionBlockWalkerSentinel(t *testing.T) {
	regionBlockWalkerExpect(t, "sentinel_region", `def find(target: i32) -> int:
    if target > 0:
        return 0
    return -1

def use_bad(xs: darray[i32]&, target: i32) -> i32:
    region rr(64):
        idx: int = find(target)
        return xs[idx.usize()]
    return 0
`, "negative not-found sentinel")
}

// Regions are keyed by name, so an inner region reusing the name of a region still
// open around it would make its values indistinguishable from the outer region's.
func TestNestedSameNameRegionIsRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "same_name_region.elisa", `def main() -> i64:
    region scratch(4096):
        kept: mutable sview = ""
        region scratch(4096):
            inner: mutable darray[u8] = [66.u8()]
            kept <- inner.as_sview()
        return kept.len.i64()
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, `region "scratch" shadows an enclosing region`) {
		t.Fatalf("expected the shadowing region to be rejected, got:\n%s", got)
	}
}

func TestSequentialSameNameRegionsAreAccepted(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "sequential_same_name_region.elisa", `def main() -> i64:
    region scratch(4096):
        a: mutable darray[u8] = [1.u8()]
        _ = a
    region scratch(4096):
        b: mutable darray[u8] = [2.u8()]
        _ = b
    return 0
`)
	if got := strings.Join(result.Errors(), "\n"); strings.Contains(got, "shadows an enclosing region") {
		t.Fatalf("sibling regions may reuse a name, got:\n%s", got)
	}
}

// A user-written `in auto:` block is freed at its exit like `region NAME:`, so a
// view stored out of it into an enclosing local dangles (the backend read a freed
// arena: SIGSEGV at run time). Only compiler-inferred auto regions are exempt.
func TestUserAutoRegionViewStoreOutIsRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "user_auto_store.elisa", `def main() -> i64:
    kept: mutable sview = ""
    in auto:
        b: mutable darray[u8] = [65.u8()]
        kept <- b.as_sview()
    return kept[0].i64() - 65
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, `is stored into "kept", which outlives the region`) {
		t.Fatalf("expected the in-auto store escape to be rejected, got:\n%s", got)
	}
}

// Declaring the target inside the `in auto:` block stays legal.
func TestUserAutoRegionInnerStoreIsAccepted(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "user_auto_inner.elisa", `def main() -> i64:
    in auto:
        kept: mutable sview = ""
        b: mutable darray[u8] = [65.u8()]
        kept <- b.as_sview()
        return kept[0].i64() - 65
    return 0
`)
	if got := strings.Join(result.Errors(), "\n"); strings.Contains(got, "outlives the region") {
		t.Fatalf("inner store must stay legal, got:\n%s", got)
	}
}
