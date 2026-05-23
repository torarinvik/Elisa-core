package semantic

import (
	"strings"
	"testing"
)

// Growing a darray field that outlives the call from a function-local Arena
// value is a use-after-free: the backing buffer is freed when the local arena
// goes out of scope. The analyzer must reject it.
func TestDArrayGrowthFromLocalArenaIntoNonLocalFieldIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_escape_field.elisa", `struct Bag:
    items: mutable darray[i64]

def add(self: mutable Bag&, value: i64) -> void:
    arena: Arena = zeroed
    in arena:
        self.items.push(value)
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "use-after-free") {
		t.Fatalf("expected use-after-free diagnostic for non-local darray grown from local arena, got:\n%s", joined)
	}
}

// Same hazard via a `mutable darray&` parameter (the buffer the caller owns).
func TestDArrayGrowthFromLocalArenaIntoRefParamIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_escape_param.elisa", `def fill(out: mutable darray[u8]&, n: usize) -> void:
    arena: Arena = zeroed
    in arena:
        i: mutable usize = 0
        while i < n:
            out.push(0)
            i <- i + 1
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "use-after-free") {
		t.Fatalf("expected use-after-free diagnostic for ref-parameter darray grown from local arena, got:\n%s", joined)
	}
}

// Growing a darray field from an Arena& parameter (caller-owned, persistent) is
// the correct pattern and must be accepted.
func TestDArrayGrowthFromArenaRefParamIntoFieldIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_ok_arenaref.elisa", `struct Bag:
    items: mutable darray[i64]

def add(self: mutable Bag&, owner: mutable Arena&, value: i64) -> void:
    in owner:
        self.items.push(value)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for field grown from Arena& parameter, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Returning a local collection whose backing was grown in a function-local
// arena is a use-after-free (the backing is freed on return).
func TestReturnLocalCollectionGrownInLocalArenaIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_escape_return.elisa", `def collect(n: usize) -> darray[i64]:
    out: mutable darray[i64] = []
    arena: Arena = zeroed
    in arena:
        i: mutable usize = 0
        while i < n:
            out.push(i.i64())
            i <- i + 1
    return out
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "use-after-free") {
		t.Fatalf("expected use-after-free diagnostic for returning a local collection grown in a local arena, got:\n%s", joined)
	}
}

// Returning a pointer into a local collection grown in a function-local arena is
// likewise a use-after-free (the Substr/Concat string-builder pattern).
func TestReturnPointerIntoLocalArenaBufferIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_escape_ptr.elisa", `def substr(input: static u8&, n: usize) -> static u8&:
    out: mutable darray[u8] = []
    arena: Arena = zeroed
    in arena:
        i: mutable usize = 0
        while i < n:
            out.push(input[i])
            i <- i + 1
        out.push(0)
    return out[0].ref[static u8&]
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "use-after-free") {
		t.Fatalf("expected use-after-free diagnostic for returning a pointer into a local-arena buffer, got:\n%s", joined)
	}
}

// Storing a local collection grown in a function-local arena into a longer-lived
// (non-local) location is a use-after-free.
func TestStoreLocalArenaCollectionIntoNonLocalIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_escape_store.elisa", `struct Cache:
    items: mutable darray[i64]

def fill(self: mutable Cache&, n: usize) -> void:
    out: mutable darray[i64] = []
    arena: Arena = zeroed
    in arena:
        i: mutable usize = 0
        while i < n:
            out.push(i.i64())
            i <- i + 1
    self.items <- out
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "use-after-free") {
		t.Fatalf("expected use-after-free diagnostic for storing a local-arena collection into a non-local field, got:\n%s", joined)
	}
}

// Building a collection in a persistent (Arena&-parameter) arena and returning
// it is the correct pattern and must be accepted.
func TestReturnCollectionBuiltInPersistentArenaIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_return_ok.elisa", `def collect(owner: mutable Arena&, n: usize) -> darray[i64]:
    out: mutable darray[i64] = []
    in owner:
        i: mutable usize = 0
        while i < n:
            out.push(i.i64())
            i <- i + 1
    return out
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for collection built in a persistent arena, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Growing a purely local darray from a local arena is fine: both share the
// function's lifetime and nothing escapes here.
func TestDArrayGrowthFromLocalArenaIntoLocalIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_ok_local.elisa", `def build() -> usize:
    arena: Arena = zeroed
    in arena:
        xs: mutable darray[i64] = []
        xs.push(1)
        return xs.count
    return 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for local darray grown from local arena, got:\n%s", strings.Join(errs, "\n"))
	}
}
