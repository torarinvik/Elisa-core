package main

import "testing"

// docs/120 §6 single-place thread assignment: `x <- if …` where every branch
// threads x in place (mutating call) or yields it unchanged (bare x). The whole
// construct erases to a statement-if over in-place mutations — these goldens pin
// that every path mutates (or doesn't) exactly as written, on the native backend.
const singleThreadAssignBody = `
struct Lexer:
    position: mutable i64

def advance_chars(lexer: lmut Lexer, n: i64) -> void:
    lexer.position <- lexer.position + n

def advance_char(lexer: lmut Lexer) -> void:
    lexer.position <- lexer.position + 1

def step(lexer: lmut Lexer, width: i64) -> void:
    lexer <-
        if width > 1:
            lexer.advance_chars(width)
        elif width == 1:
            lexer.advance_char()
        else:
            lexer

@test
def wide_arm() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    lx <- lx.step(5)
    if lx.position != 5:
        panic("wide arm wrong")

@test
def single_arm() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    lx <- lx.step(1)
    if lx.position != 1:
        panic("single arm wrong")

@test
def neutral_arm() -> void:
    lx: mutable Lexer = Lexer{position: 7}
    lx <- lx.step(0)
    if lx.position != 7:
        panic("neutral arm must be a no-op")

@test
def chained() -> void:
    lx: mutable Lexer = Lexer{position: 0}
    lx <- lx.step(3)
    lx <- lx.step(1)
    lx <- lx.step(0)
    if lx.position != 4:
        panic("chain wrong")
`

func TestSingleThreadAssign(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "single_thread_assign", singleThreadAssignBody)
	assertAllPassed(t, exit, stdout, stderr, "wide_arm", "single_arm", "neutral_arm", "chained")
}
