package main

import "testing"

// docs/120 §10 / docs/119 §4: `return if cond: … else: …` as a value-if whose branches
// each yield the declared lmut thread tuple. A branch may carry a `rebind` claim before its
// tail tuple, threading a nested lmut call in place. Pins that the threaded-back value
// reflects EVERY in-place mutation on both paths (the true arm and the rebind-in-else arm).
const returnValueIfThreadBody = `
struct Lexer:
    pos: mutable i64

def read_rest(lexer: lmut Lexer) -> (tok: i64, lexer: lmut Lexer):
    lexer.pos <- lexer.pos + 1
    return 90 + lexer.pos, lexer

def read_op(lexer: lmut Lexer, matched: bool) -> (tok: i64, lexer: lmut Lexer):
    lexer.pos <- lexer.pos + 1
    return if matched:
        1, lexer
    else:
        rebind rest: i64, lexer = lexer.read_rest()
        rest, lexer

@test
def true_arm_threads() -> void:
    lx: mutable Lexer = Lexer{pos: 0}
    rebind t1: i64, lx = lx.read_op(true)
    if t1 != 1 or lx.pos != 1:
        panic("true arm wrong")

@test
def else_arm_threads_nested_rebind() -> void:
    lx: mutable Lexer = Lexer{pos: 0}
    rebind t2: i64, lx = lx.read_op(false)
    if t2 != 92 or lx.pos != 2:
        panic("else arm wrong")
`

func TestReturnValueIfThread(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "return_value_if_thread", returnValueIfThreadBody)
	assertAllPassed(t, exit, stdout, stderr, "true_arm_threads", "else_arm_threads_nested_rebind")
}
