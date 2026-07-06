package main

import "testing"

// docs/120 §6 goldens: thread slots in multi-place assignment. A slot naming its own
// target — the bare binding or a mutating call on it — is a THREAD: the effect executes
// in place and the slot erases (a mutating call "yields" its receiver notationally,
// the §2 erased-return model extended to expression slots). Value slots keep the §1
// simultaneous-assignment semantics. Branches are dataflow; the target list is the
// mutation manifest.
const threadSlotsBody = `
enum Decl:
    Func(name: i64)
    Bad

struct P:
    x: mutable i64

def advance(p: lmut P) -> void:
    p.x <- p.x + 1

def collect(p: lmut P, out: mutable darray[Decl]&, d_opt: Decl?) -> void:
    p, out <-
        if d_opt is d:
            p, out.push(d)
        else:
            p.advance(), out

@test
def thread_slots_all_thread() -> void:
    p: mutable P = P{x: 0}
    out: mutable darray[Decl] = []
    p <- collect(p, out, Decl.Func(7))
    if out.count != 1 or p.x != 0:
        panic("push arm wrong")
    p <- collect(p, out, null)
    if out.count != 1 or p.x != 1:
        panic("advance arm wrong")

@test
def thread_slots_mixed_value_and_thread() -> void:
    p: mutable P = P{x: 0}
    count: mutable i64 = 10
    p, count <-
        if true:
            p.advance(), count + 1
        else:
            p, count
    if p.x != 1 or count != 11:
        panic("mixed form wrong")

@test
def thread_slots_block_arm_with_pre_tail_stmts() -> void:
    p: mutable P = P{x: 0}
    c: mutable i64 = 0
    p, c <-
        if true:
            step: i64 = 5
            p.advance(), c + step
        else:
            p, c
    if p.x != 1 or c != 5:
        panic("block arm wrong")
`

func TestThreadSlots(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "thread_slots", threadSlotsBody)
	assertAllPassed(t, exit, stdout, stderr, "thread_slots_all_thread", "thread_slots_mixed_value_and_thread", "thread_slots_block_arm_with_pre_tail_stmts")
}
