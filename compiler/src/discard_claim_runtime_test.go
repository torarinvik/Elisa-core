package main

import "testing"

// docs/120 §11: the discard claim `parser, _ <- parser.advance()` — the lmut thread
// is claimed (in-place mutation, erased), the real return dropped visibly via `_`.
const discardClaimBody = `
struct Parser:
    pos: mutable i64

def advance(parser: lmut Parser) -> i64:
    parser.pos <- parser.pos + 1
    return parser.pos

@test
def discard_claim_threads() -> void:
    p: mutable Parser = Parser{pos: 0}
    p, _ <- p.advance()
    p, _ <- p.advance()
    if p.pos != 2:
        panic("discard claim did not thread")
    v: mutable i64 = 0
    p, v <- p.advance()
    if v != 3 or p.pos != 3:
        panic("value claim after discard wrong")
`

func TestDiscardClaim(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "discard_claim", discardClaimBody)
	assertAllPassed(t, exit, stdout, stderr, "discard_claim_threads")
}
