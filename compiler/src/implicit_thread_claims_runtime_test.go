package main

import "testing"

// docs/120 implicit threading: every `lmut` parameter is a compile-time thread slot —
// no §2 declaration needed. A value-returning lmut callee is claimed at the call site
// in either spelling: `lexer, suffix <- lexer.read_type_suffix()` (arrow claim) or
// `rebind suffix: i64, lexer = lexer.read_type_suffix()` (rebind claim). The claim
// erases (mutation is in place); the remaining target binds the real return.
const implicitThreadClaimsBody = `
struct Lexer:
    position: mutable i64

def read_type_suffix(lexer: lmut Lexer) -> i64:
    lexer.position <- lexer.position + 2
    return 40 + lexer.position

@test
def arrow_claim_threads_and_binds() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    suffix: mutable i64 = 0
    lx, suffix <- lx.read_type_suffix()
    if suffix != 42 or lx.position != 2:
        panic("arrow claim wrong")
    lx, suffix <- lx.read_type_suffix()
    if suffix != 44 or lx.position != 4:
        panic("arrow claim second call wrong")

@test
def rebind_claim_threads_and_binds() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    rebind suffix: i64, lx = lx.read_type_suffix()
    if suffix != 42 or lx.position != 2:
        panic("rebind claim wrong")
`

func TestImplicitThreadClaims(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "implicit_thread_claims", implicitThreadClaimsBody)
	assertAllPassed(t, exit, stdout, stderr, "arrow_claim_threads_and_binds", "rebind_claim_threads_and_binds")
}
