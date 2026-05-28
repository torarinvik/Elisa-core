package semantic

import (
	"strings"
	"testing"
)

// `linear` (use-exactly-once) carries the must-consume obligation: a linear
// value left unconsumed at scope exit is an error.
func TestLinearKeywordMustBeConsumed(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "linear_kw.elisa", `linear struct L:
    x: bool

def f() -> void:
    a: L = L(true)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "must be consumed before scope exit") {
		t.Fatalf("expected linear value to require consumption; got: %s", all)
	}
}

// `affine` (use-at-most-once) is droppable: an affine value may be abandoned
// without an explicit consume — no must-consume error.
func TestAffineKeywordIsDroppable(t *testing.T) {
	analyzeTreeTestSource(t, "affine_kw.elisa", `affine struct A:
    x: bool

def f() -> void:
    a: A = A(true)
`)
}

// `affine` is still move-only: it may be used at most once, so using it after
// it has been moved is an error (the difference from `linear` is only the
// drop obligation, not the single-use discipline).
func TestAffineKeywordIsStillMoveOnly(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "affine_moveonly.elisa", `affine struct A:
    x: bool

def take(v: A) -> void:
    _ = move v

def f() -> void:
    a: A = A(true)
    take(move a)
    take(move a)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot be used") && !strings.Contains(all, "must be moved") {
		t.Fatalf("expected affine value to remain move-only (use-after-move rejected); got: %s", all)
	}
}
