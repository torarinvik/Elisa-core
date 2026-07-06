package semantic

import (
	"strings"
	"testing"
)

// lmut (linear-mutable) Phase 2: the call-site checker. `lmut T` is codegen-identical to
// `mutable T&`, but carries a checker contract — an `lmut` argument must be a movable mutable
// place, and it may not alias any other argument of the same call (the single-live-binding rule).

const lmutCheckerPrelude = `struct Counter:
    value: mutable i64

def bump(c: lmut Counter, by: i64) -> i64:
    c.value <- c.value + by
    return c.value

def combine(a: lmut Counter, b: lmut Counter) -> void:
    a.value <- a.value + b.value
`

// A straight-line thread through an `lmut` param is clean: mutable source, no aliasing.
func TestLmutThreadMutableSourceIsClean(t *testing.T) {
	analyzeTreeTestSource(t, "lmut_clean.elisa", lmutCheckerPrelude+`
def use() -> void:
    c: mutable Counter = Counter{value: 10}
    _ = bump(c, 5)
    _ = bump(c, 100)
`)
}

// Passing an immutable binding to an `lmut` parameter must error: the source has to be mutable so
// it can be moved out and threaded back.
func TestLmutImmutableSourceErrors(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_immutable.elisa", lmutCheckerPrelude+`
def use() -> void:
    c: Counter = Counter{value: 10}
    _ = bump(c, 5)
`)
	// Passing an immutable binding to a mutable ref is already rejected by the ordinary argument
	// typecheck ("expects mutable Counter&, got Counter"); the lmut-specific "must be mutable"
	// message only surfaces for a mutable-but-otherwise-invalid source. Either way it must error.
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "mutable") {
		t.Fatalf("expected immutable-source error; got: %s", all)
	}
}

// Passing the same place to two `lmut` parameters of one call aliases a linear-mutable value across
// two live bindings — must error.
func TestLmutAliasingArgsErrors(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_alias.elisa", lmutCheckerPrelude+`
def use() -> void:
    c: mutable Counter = Counter{value: 10}
    combine(c, c)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "aliases another argument") {
		t.Fatalf("expected aliasing error; got: %s", all)
	}
}

// Passing a temporary (a fresh struct literal) to an `lmut` parameter must error: there is no
// movable place to reacquire after the call.
func TestLmutTemporarySourceErrors(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_temp.elisa", lmutCheckerPrelude+`
def use() -> void:
    _ = bump(Counter{value: 1}, 5)
`)
	// A temporary is not an lvalue for a mutable ref, so the ordinary arg typecheck already
	// rejects it; the lmut "movable mutable place" message is the belt-and-suspenders backstop.
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "mutable") {
		t.Fatalf("expected temporary-source error; got: %s", all)
	}
}

// The lexer's real pattern: an `lmut` parameter forwarded via a UFCS method-call receiver to
// another `lmut` parameter. A forwarded param is a single, non-aliasing, valid pass — must stay
// clean even though an lmut/mutable-ref param binding is not itself Symbol.Mutable.
func TestLmutForwardReceiverIsClean(t *testing.T) {
	analyzeTreeTestSource(t, "lmut_forward.elisa", `struct Lexer:
    pos: mutable i64

def advance(lx: lmut Lexer) -> void:
    lx.pos <- lx.pos + 1

def scan(lx: lmut Lexer) -> void:
    lx.advance()
    lx.advance()
`)
}

// Disjoint fields of the same root passed to two `lmut` params do NOT alias — must stay clean.
func TestLmutDisjointFieldsClean(t *testing.T) {
	analyzeTreeTestSource(t, "lmut_disjoint.elisa", `struct Counter:
    value: mutable i64

struct Pair:
    a: mutable Counter
    b: mutable Counter

def combine(x: lmut Counter, y: lmut Counter) -> void:
    x.value <- x.value + y.value

def use() -> void:
    p: mutable Pair = Pair{a: Counter{value: 1}, b: Counter{value: 2}}
    combine(p.a, p.b)
`)
}
