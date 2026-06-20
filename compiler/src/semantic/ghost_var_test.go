//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// POSITIVE: a `ghost` variable participates in a contract and lets the prover discharge a clause it
// could otherwise see directly. Here the ghost mirrors a value the invariant then asserts. The ghost
// must NOT cause any analysis error and must be recorded for codegen erasure.
func TestGhostVarUsableInContractProves(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x >= 0
    ghost g: i64 = x + 1
    invariant g > x
    return x
`
	result := analyzeWithSMT(t, "ghost_prove.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis of a ghost var used in an invariant, got: %v", errs)
	}
	if len(result.GhostDecls) != 1 {
		t.Fatalf("expected exactly one ghost decl registered for codegen erasure, got %d", len(result.GhostDecls))
	}
}

// POSITIVE: a ghost var is readable in an `ensure` clause (evaluated over the body's final scope).
func TestGhostVarUsableInEnsure(t *testing.T) {
	src := `
def g(n: i64) -> i64:
    ensure result == n
    ghost shadow: i64 = n
    invariant shadow == n
    return n
`
	result := analyzeWithSMT(t, "ghost_ensure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
}

// POSITIVE: the grouped `ghost:` block declares several ghost vars at once; all are erased and all
// are usable in a following contract.
func TestGhostBlockGroup(t *testing.T) {
	src := `
def h(x: i64) -> i64:
    ghost:
        a: i64 = x
        b: i64 = x + 1
    invariant b > a
    return x
`
	result := analyzeWithSMT(t, "ghost_block.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis of a ghost block, got: %v", errs)
	}
	if len(result.GhostDecls) != 2 {
		t.Fatalf("expected two ghost decls from the block, got %d", len(result.GhostDecls))
	}
}

// SOUNDNESS: assigning a REAL variable from a ghost var is a hard error. Real values may never depend
// on ghost state — the ghost is erased, so at runtime the real var would read a value that does not
// exist. This is invariant (1): no ghost-to-real flow.
func TestGhostToRealVarDeclRejected(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    ghost g: i64 = x + 1
    real: i64 = g
    return real
`
	result := analyzeWithSMT(t, "ghost_to_real_decl.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "ghost variable") {
		t.Fatalf("expected a ghost-to-real flow error, got: %v", result.Errors())
	}
}

// SOUNDNESS: returning a ghost var is ghost-to-real flow (the return value is real) — rejected.
func TestGhostReturnRejected(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    ghost g: i64 = x
    return g
`
	result := analyzeWithSMT(t, "ghost_return.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "ghost variable") {
		t.Fatalf("expected a ghost return to be rejected, got: %v", result.Errors())
	}
}

// SOUNDNESS: reading a ghost var in ordinary (non-contract) runtime code — here a real arithmetic
// expression statement / passing into a real call — is rejected. This is invariant (2): real code
// never reads a ghost.
func TestGhostReadInRealCodeRejected(t *testing.T) {
	src := `
extern sink(value: i64) -> void

def f(x: i64) -> void:
    ghost g: i64 = x
    sink(g)
`
	result := analyzeWithSMT(t, "ghost_real_read.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "ghost variable") {
		t.Fatalf("expected a ghost read in real code to be rejected, got: %v", result.Errors())
	}
}

// SOUNDNESS: assigning into a real mutable var from a ghost var via `<-` is ghost-to-real flow.
func TestGhostToRealAssignRejected(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    real: mutable i64 = 0
    ghost g: i64 = x
    real <- g
    return real
`
	result := analyzeWithSMT(t, "ghost_assign.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "ghost variable") {
		t.Fatalf("expected a ghost-to-real assignment to be rejected, got: %v", result.Errors())
	}
}

// SOUNDNESS of erasure: a ghost initializer must be side-effect-free, since it is dropped from
// codegen. A general call could mutate real state, so it is rejected.
func TestGhostInitWithCallRejected(t *testing.T) {
	src := `
extern bump() -> i64

def f() -> i64:
    ghost g: i64 = bump()
    invariant g >= 0
    return 0
`
	result := analyzeWithSMT(t, "ghost_init_call.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "side-effect-free") {
		t.Fatalf("expected a side-effecting ghost initializer to be rejected, got: %v", result.Errors())
	}
}

// A ghost var may be defined in terms of another ghost var (ghost-to-ghost flow is fine).
func TestGhostToGhostAllowed(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    ghost a: i64 = x
    ghost b: i64 = a + 1
    invariant b > a
    return x
`
	result := analyzeWithSMT(t, "ghost_to_ghost.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("ghost-to-ghost flow must be allowed, got: %v", errs)
	}
}

// A variable literally named `ghost` (not the keyword) must still parse as an ordinary variable.
func TestVariableNamedGhostStillWorks(t *testing.T) {
	src := `
def f() -> i64:
    ghost = 5
    return ghost
`
	result := analyzeWithSMT(t, "named_ghost.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a variable named `ghost` must keep working, got: %v", errs)
	}
	if len(result.GhostDecls) != 0 {
		t.Fatalf("a variable named `ghost` must not be treated as a ghost decl, got %d", len(result.GhostDecls))
	}
}
