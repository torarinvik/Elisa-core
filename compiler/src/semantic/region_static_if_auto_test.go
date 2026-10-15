package semantic

import (
	"strings"
	"testing"
)

func TestStaticIfLocalDarrayRegionInference(t *testing.T) {
	source := `def fill(path: mutable darray[u8]&) -> bool can[Memory.Allocate, Abort.Panic, Unsafe.PointerCast]:
    path.resize(8192.usize())
    path_cstr: cstr = (&path[0]).cast[cstr] can Unsafe.PointerCast
    path.push(0)
    return true

def outer(prefix: mutable darray[u8]&) -> bool can[Memory.Allocate, Abort.Panic, Unsafe.PointerCast]:
    static if true:
        storage: mutable darray[u8] = []
        return fill(&storage)
    static else:
        return false
`
	errs := analyzeTreeTestSourceWithSemanticErrors(t, "region_proof_blocker.elisa", source).Errors()
	if joined := strings.Join(errs, " | "); joined != "" {
		t.Fatalf("local darray call must infer its region: %s", joined)
	}
}

func TestStaticElseLocalDarrayRegionInference(t *testing.T) {
	source := `def fill(path: mutable darray[u8]&) -> bool can[Memory.Allocate, Abort.Panic]:
    path.resize(8.usize())
    return true

def outer() -> bool can[Memory.Allocate, Abort.Panic]:
    static if false:
        return false
    static else:
        storage: mutable darray[u8] = []
        return fill(&storage)
`
	errs := analyzeTreeTestSourceWithSemanticErrors(t, "region_proof_blocker_else.elisa", source).Errors()
	if joined := strings.Join(errs, " | "); joined != "" {
		t.Fatalf("selected static else must infer its local region: %s", joined)
	}
}

func TestStaticElifLocalDarrayRegionInference(t *testing.T) {
	source := `def fill(path: mutable darray[u8]&) -> bool can[Memory.Allocate, Abort.Panic]:
    path.resize(8.usize())
    return true

def outer() -> bool can[Memory.Allocate, Abort.Panic]:
    static if false:
        return false
    static elif true:
        storage: mutable darray[u8] = []
        return fill(&storage)
    static else:
        return false
`
	errs := analyzeTreeTestSourceWithSemanticErrors(t, "region_proof_blocker_elif.elisa", source).Errors()
	if joined := strings.Join(errs, " | "); joined != "" {
		t.Fatalf("selected static elif must infer its local region: %s", joined)
	}
}

func TestStaticBranchLocalRegionEscapeRejected(t *testing.T) {
	source := `def leak() -> darray[u8] can[Memory.Allocate, Abort.Panic]:
    static if true:
        storage: mutable darray[u8] = []
        storage.push(1)
        return storage
    static else:
        return []
`
	errs := analyzeTreeTestSourceWithSemanticErrors(t, "region_proof_blocker_escape.elisa", source).Errors()
	if joined := strings.Join(errs, " | "); !strings.Contains(joined, "value escapes its `in auto:` scope via return") {
		t.Fatalf("function-local auto-region storage must not escape through a static branch, got: %s", joined)
	}
}
