package main

import "testing"

// docs/120 goldens: multi-place mutation `place1, place2 <- <tuple expr>` — several
// places updated from one tuple-valued RHS, so a conditional update reads as pure
// values flowing into exactly one visible `<-`. Desugars like `rebind` (temp-tuple
// bind + per-place assign via pendingStmts); the RHS is fully evaluated before any
// place is written, so assignment is simultaneous (swap-safe).
const multiPlaceAssignBody = `
struct Lexer:
    line: mutable i64
    column: mutable i64

def advance(lx: lmut Lexer, nl: bool) -> void:
    lx.line, lx.column <-
        if nl:
            lx.line + 1, 1
        else:
            lx.line, lx.column + 1

@test
def multi_place_conditional() -> void:
    lx: mutable Lexer = Lexer{line: 1, column: 5}
    advance(lx, false)
    if lx.line != 1 or lx.column != 6:
        panic("plain advance wrong")
    advance(lx, true)
    if lx.line != 2 or lx.column != 1:
        panic("newline advance wrong")

@test
def multi_place_swap_is_simultaneous() -> void:
    lx: mutable Lexer = Lexer{line: 7, column: 9}
    lx.line, lx.column <- lx.column, lx.line
    if lx.line != 9 or lx.column != 7:
        panic("swap must read both old values before writing")

@test
def multi_place_mixed_targets() -> void:
    lx: mutable Lexer = Lexer{line: 3, column: 4}
    total: mutable i64 = 0
    # A local and a field place in one multi-place assign.
    total, lx.column <- lx.line + lx.column, 0
    if total != 7 or lx.column != 0 or lx.line != 3:
        panic("mixed local+field targets wrong")
`

func TestMultiPlaceAssign(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "multi_place_assign", multiPlaceAssignBody)
	assertAllPassed(t, exit, stdout, stderr, "multi_place_conditional", "multi_place_swap_is_simultaneous", "multi_place_mixed_targets")
}
