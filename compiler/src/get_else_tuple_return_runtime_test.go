package main

import "testing"

// A `get … else return (null, x)` recovery inside a function returning a named
// tuple with an Optional field must lower the tuple with the DECLARED field types.
// The recovery is analyzed outside the return context, so its recorded tuple type
// has a bare-null field; before the fix it lowered to `{ ptr, i64 }` and failed
// LLVM's return-type check (Stage1's ga_field_annotation_scope hit this).
const getElseTupleReturnBody = `
struct Box:
    v: i64

def pick(n: Box, k: i64) -> (annotation: Box?, scope: usize):
    return (null, k.usize()) if k == 0
    inner: (annotation: Box?, scope: usize) = pick(n, k - 1)
    got: Box = get inner.annotation else return (null, inner.scope + 10.usize())
    return (got, k.usize())

def lift(n: Box, k: i64) -> (annotation: Box?, scope: usize):
    return (n, 5.usize()) if k == 0
    inner: (annotation: Box?, scope: usize) = lift(n, k - 1)
    got: Box = get inner.annotation else return (null, 99.usize())
    return (got, inner.scope + 1.usize())

@test
def get_else_tuple_return() -> void:
    can Abort.Panic:
        a: (annotation: Box?, scope: usize) = pick(Box{v: 3}, 2)
        if a.scope != 20.usize():
            panic("recovery return scope")
        missing: Box = get a.annotation else Box{v: -1}
        if missing.v != -1:
            panic("recovery return must be null")
        b: (annotation: Box?, scope: usize) = lift(Box{v: 4}, 2)
        if b.scope != 7.usize():
            panic("success path scope")
        got: Box = get b.annotation else return
        if got.v != 4:
            panic("success path payload")
`

func TestGetElseTupleReturn(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "get_else_tuple_return", getElseTupleReturnBody)
	assertAllPassed(t, exit, stdout, stderr, "get_else_tuple_return")
}
