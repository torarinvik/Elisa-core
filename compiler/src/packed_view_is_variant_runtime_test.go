package main

import "testing"

// Inside a packed recursive enum's match arm the scrutinee is narrowed to that arm's
// variant view (packedview[E.Pair]). A nested `if e is E.Two(first, w)` names E's
// variants; it used to bind nothing semantically ("undefined identifier first") and the
// backend rejected the view as "requires enum ... got packedview". Pins acceptance and
// the payload values extracted through the or-arm copies (each copy sees a different view).
const packedViewIsVariantBody = `
enum E:
    Leaf(v: i64)
    Pair(inner: E, w: i64)
    Two(inner: E, w: i64)

def f(e: E) -> i64:
    match e:
        E.Pair(_, _) | E.Two(_, _):
            if e is E.Two(first, w):
                return f(first) + w * 100
            if e is E.Pair(first, w):
                return f(first) + w * 10
            return -1
        E.Leaf(v):
            return v

@test
def nested_is_in_or_arm() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        l3: E = new[auto] E.Leaf(3)
        p4: E = new[auto] E.Pair(l3, 4)
        t5: E = new[auto] E.Two(p4, 5)
        if f(t5) != 543:
            panic("two/pair chain wrong")
        l1: E = new[auto] E.Leaf(1)
        t2: E = new[auto] E.Two(l1, 2)
        p3: E = new[auto] E.Pair(t2, 3)
        if f(p3) != 231:
            panic("pair/two chain wrong")
        l9: E = new[auto] E.Leaf(9)
        if f(l9) != 9:
            panic("leaf wrong")
`

func TestPackedViewIsVariantPattern(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "packed_view_is_variant", packedViewIsVariantBody)
	assertAllPassed(t, exit, stdout, stderr, "nested_is_in_or_arm")
}
