package main

import "testing"

// docs/120 §9 goldens: loop-expression threading. A `while cond |p, …|:` capture header
// (docs/119 §6) threads the outer `lmut` bindings through the loop — each iteration's §8
// arg-manifest reassignment (`p <- p.advance()`) mutates them in place, and the header
// writes the final state back to the outer binding. So the loop is itself a link in the
// linear dataflow chain: the value flows in, is threaded across iterations (visible in the
// header), and flows out. The caller manifests the whole call with `p <- f(p)`. All
// in-place, zero-overhead.
const lmutLoopThreadBody = `
struct Parser:
    pos: mutable i64
    text: mutable i64

struct List:
    total: mutable i64

def more(p: Parser&) -> bool:
    return p.pos < p.text

def advance_one(p: lmut Parser) -> void:
    p.pos <- p.pos + 1

def push(l: lmut List, v: i64) -> void:
    l.total <- l.total + v

def skip_ws(p: lmut Parser) -> void:
    while p.more() |p|:
        p <- p.advance_one()

def collect(p: lmut Parser, items: lmut List) -> void:
    while p.more() |p, items|:
        p <- p.advance_one()
        items <- items.push(p.pos)

@test
def loop_thread_single() -> void:
    p: mutable Parser = Parser{pos: 0, text: 3}
    p <- skip_ws(p)
    if p.pos != 3:
        panic("single-value loop threading wrong")

@test
def loop_thread_multi() -> void:
    p: mutable Parser = Parser{pos: 0, text: 3}
    items: mutable List = List{total: 0}
    p, items <- collect(p, items)
    if p.pos != 3 or items.total != 6:
        panic("multi-value loop threading wrong (want pos=3, total=1+2+3=6)")
`

func TestLmutLoopThread(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "lmut_loop_thread", lmutLoopThreadBody)
	assertAllPassed(t, exit, stdout, stderr, "loop_thread_single", "loop_thread_multi")
}
