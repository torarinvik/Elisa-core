package main

import "testing"

// docs/120 §2 goldens: declared lmut threading. `lmut` in a return-tuple slot is
// NOTATION — the checker enforces that every return path threads the same-named
// lmut parameter in its declared position, then ERASES the slot from the return
// type and every return expression. Codegen emits exactly the plain-lmut function
// (in-place mutable ref, scalar/kept-tuple return): the manifest costs nothing.
const lmutDeclaredThreadingBody = `
const NULL_CHAR: u8 = 0

struct Lexer:
    position: mutable i64
    line: mutable i64
    column: mutable i64

def is_eof(lx: Lexer&) -> bool:
    return lx.position >= 3

def advance_char(lexer: lmut Lexer) -> (ch: u8, lexer: lmut Lexer):
    if lexer.is_eof():
        return NULL_CHAR, lexer
    ch: u8 = 65
    lexer.position <- lexer.position + 1
    lexer.line, lexer.column <-
        if ch == 10:
            lexer.line + 1, 1
        else:
            lexer.line, lexer.column + 1
    return ch, lexer

def touch(lx: lmut Lexer) -> (lx: lmut Lexer):
    lx.position <- lx.position + 1
    return lx

@test
def declared_threading_erases_to_plain_lmut() -> void:
    lx: mutable Lexer = Lexer{position: 0, line: 1, column: 1}
    # docs/120 §3 must-use: a declaring fn is called via the rebind form, which
    # claims the threaded arg (in place, erased) and binds the value slots.
    rebind c: u8, lx = lx.advance_char()
    if c != 65:
        panic("returned char wrong")
    if lx.position != 1 or lx.column != 2 or lx.line != 1:
        panic("lexer did not thread in place")
    rebind c2: u8, lx = lx.advance_char()
    rebind c3: u8, lx = lx.advance_char()
    if c2 != c3:
        panic("steady advance wrong")
    # position hits the EOF bound; the early-return path also threads
    rebind eof: u8, lx = lx.advance_char()
    if eof != NULL_CHAR:
        panic("eof path wrong")
    if lx.position != 3:
        panic("eof path must not advance")

@test
def all_slots_threaded_erases_to_void() -> void:
    lx: mutable Lexer = Lexer{position: 0, line: 1, column: 1}
    rebind lx = touch(lx)
    rebind lx = touch(lx)
    if lx.position != 2:
        panic("void-erased threading broken")
`

func TestLmutDeclaredThreading(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "lmut_declared_threading", lmutDeclaredThreadingBody)
	assertAllPassed(t, exit, stdout, stderr, "declared_threading_erases_to_plain_lmut", "all_slots_threaded_erases_to_void")
}
