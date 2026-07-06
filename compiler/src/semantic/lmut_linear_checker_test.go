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
    c, _ <- c.bump(5)
    c, _ <- c.bump(100)
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

// `lmut` × `linear` pairing: a `linear` type (must-consume, single-owner) threaded through an
// `lmut` parameter is NOT consumed — it threads back, so it may be mutated in place across several
// calls and the must-consume obligation still holds. This is the airtight story: `linear` gives
// global single-ownership, `lmut` lets you mutate that owned resource in place without ceremony.
func TestLmutThreadsLinearValueConsumedClean(t *testing.T) {
	analyzeTreeTestSource(t, "lmut_linear_ok.elisa", `linear struct Handle:
    fd: mutable i64

def make() -> Handle:
    return Handle{fd: 1}

def bump(h: lmut Handle) -> void:
    h.fd <- h.fd + 1

def sink(h: Handle) -> void:
    _ = move h

def f() -> void:
    h: mutable Handle = make()
    h <- h.bump()
    h <- h.bump()
    sink(move h)
`)
}

// The other half of the pairing: because `lmut` threads the linear value back (does not consume
// it), a body that mutates it via lmut but never consumes it must STILL be flagged must-consume.
// This pins that lmut does not silently satisfy the linear obligation.
func TestLmutDoesNotConsumeLinearValue(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_linear_leak.elisa", `linear struct Handle:
    fd: mutable i64

def make() -> Handle:
    return Handle{fd: 1}

def bump(h: lmut Handle) -> void:
    h.fd <- h.fd + 1

def f() -> void:
    h: mutable Handle = make()
    bump(h)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "must be consumed") {
		t.Fatalf("expected linear value threaded via lmut to still require consumption; got: %s", all)
	}
}

const strictMutPrelude = `struct P:
    n: mutable i64

def advance(p: lmut P) -> void:
    p.n <- p.n + 1

def push(p: lmut P, v: i64) -> void:
    p.n <- p.n + v
`

// docs/120 §10 uniform enforcement: a bare mutating call on an lmut value is a hidden
// mutation — it must be a reassignment. (Enabled via the rollout env toggle.)
func TestLinearMutationBareCallFlagged(t *testing.T) {
	t.Setenv("ELISA_LINEAR_MUTATION", "1")
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lin_bare.elisa", strictMutPrelude+`
def f(p: lmut P) -> void:
    p.advance()
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "must be a reassignment") {
		t.Fatalf("expected §10 bare-mutation error, got: %s", all)
	}
}

// The §8 reassignment form satisfies §10; direct writes and reads are unaffected.
func TestLinearMutationReassignmentClean(t *testing.T) {
	t.Setenv("ELISA_LINEAR_MUTATION", "1")
	analyzeTreeTestSource(t, "lin_ok.elisa", strictMutPrelude+`
def f(p: lmut P) -> void:
    p <- p.advance()
    p <- p.push(5)
    p.n <- p.n + 1
`)
}

// A plain `mutable T&` param is the escape hatch — never enforced.
func TestLinearMutationMutableRefUnaffected(t *testing.T) {
	t.Setenv("ELISA_LINEAR_MUTATION", "1")
	analyzeTreeTestSource(t, "lin_escape.elisa", `struct P:
    n: mutable i64

def advance(p: mutable P&) -> void:
    p.n <- p.n + 1

def f(p: mutable P&) -> void:
    p.advance()
`)
}

// docs/120 §3 must-use: a call to a declared-threading function must claim every
// declared thread via the rebind form; a plain call is an error.
func TestLmutDeclaredThreadingMustUse(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_mustuse.elisa", `struct Lexer:
    position: mutable i64

def advance_char(lexer: lmut Lexer) -> (ch: i64, lexer: lmut Lexer):
    lexer.position <- lexer.position + 1
    return 65, lexer

def use() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    c: i64 = advance_char(lx)
    _ = c
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "must claim it with the rebind form") {
		t.Fatalf("expected must-use error, got: %s", all)
	}
}

// docs/120 §3 rebind form: claiming the thread satisfies must-use and binds the
// value slots; a false claim (callee declares no threading) is rejected.
func TestLmutDeclaredThreadingRebindFormClean(t *testing.T) {
	analyzeTreeTestSource(t, "lmut_rebindform.elisa", `struct Lexer:
    position: mutable i64

def advance_char(lexer: lmut Lexer) -> (ch: i64, lexer: lmut Lexer):
    lexer.position <- lexer.position + 1
    return 65, lexer

def use() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    rebind c: i64, lx = lx.advance_char()
    _ = c
`)
}

func TestLmutFalseThreadClaimRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_falseclaim.elisa", `struct Lexer:
    position: mutable i64

def plain(lx: mutable Lexer&) -> (a: i64, b: i64):
    return 1, 2

def use() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    rebind x: i64, lx = plain(&lx)
    _ = x
`)
	// A `mutable Lexer&` param (the escape hatch) is not lmut, so the claim is invalid
	// under both declared and implicit threading.
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "neither declares lmut threading") {
		t.Fatalf("expected false-claim error, got: %s", all)
	}
}

// Soundness-contract pin: an `lmut` parameter cannot be dropped/consumed whole. `move c` on an
// lmut param is a whole-value move out of a reference, which the ordinary typecheck rejects
// (a ref is not an owning binding) — so the "always threads back, no persistent consume" guarantee
// holds by construction, not by a bespoke lmut check. Documents that this case is vacuous.
func TestLmutWholeMoveRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lmut_wholemove.elisa", `struct C:
    value: mutable i64

def sink(x: C) -> void:
    _ = move x

def f(c: lmut C) -> void:
    sink(move c)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "C") {
		t.Fatalf("expected whole-move of an lmut param to be rejected; got: %s", all)
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
    lx <- lx.advance()
    lx <- lx.advance()
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
    p <- combine(p.a, p.b)
`)
}

// docs/120 implicit threading: every lmut param is a compile-time thread slot — a
// rebind claim against a plain (undeclared) lmut callee is valid; the remaining
// target binds the real return.
func TestLmutImplicitClaimRebindClean(t *testing.T) {
	analyzeTreeTestSource(t, "lmut_implicit_rebind.elisa", `struct Lexer:
    position: mutable i64

def read_suffix(lexer: lmut Lexer) -> i64:
    lexer.position <- lexer.position + 1
    return lexer.position

def use() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    rebind s: i64, lx = lx.read_suffix()
    _ = s
`)
}

// The arrow spelling of the same claim: `lx, s <- lx.read_suffix()` — the lmut
// target claims the implicit thread, the value target binds the return.
func TestLmutImplicitClaimArrowClean(t *testing.T) {
	analyzeTreeTestSource(t, "lmut_implicit_arrow.elisa", `struct Lexer:
    position: mutable i64

def read_suffix(lexer: lmut Lexer) -> i64:
    lexer.position <- lexer.position + 1
    return lexer.position

def use() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    s: mutable i64 = 0
    lx, s <- lx.read_suffix()
    _ = s
`)
}

// docs/120 §6 single-place: `x <- if …` where every branch threads x or yields it
// unchanged compiles (erases to a statement-if) and satisfies §10 enforcement.
func TestLmutSingleThreadAssignClean(t *testing.T) {
	t.Setenv("ELISA_LINEAR_MUTATION", "1")
	analyzeTreeTestSource(t, "lmut_single_thread.elisa", `struct Lexer:
    position: mutable i64

def advance_char(lexer: lmut Lexer) -> void:
    lexer.position <- lexer.position + 1

def step(lexer: lmut Lexer, wide: bool) -> void:
    lexer <-
        if wide:
            lexer.advance_char()
        else:
            lexer
`)
}
