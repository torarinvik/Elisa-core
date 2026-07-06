package main

import "testing"

// docs/120 §8 place-manifest goldens: `place <- place.push(v)`. A mutating builtin
// collection method (push/truncate/…) returns its receiver ref, not void, so it can never
// satisfy the void arg-manifest — yet `xs <- xs.push(v)` and `b.items <- b.items.push(v)`
// are exactly the visible reassignment the linear model asks for. The place-manifest
// recognizes a `<-` whose RHS is a mutating builtin on the SAME place as the target and
// erases it to the in-place call (the redundant ref result is discarded). This pins that
// both a bare-ident place and a field place mutate in place and run.
const placeManifestBody = `
struct Bag:
    items: mutable darray[i64]

def add_bare(xs: lmut darray[i64], v: i64) -> void:
    xs <- xs.push(v)

def add_field(b: lmut Bag, v: i64) -> void:
    b.items <- b.items.push(v)

@test
def place_manifest_bare() -> void:
    xs: mutable darray[i64] = []
    xs <- add_bare(xs, 7)
    xs <- add_bare(xs, 8)
    if xs.count != 2 or xs[0] != 7 or xs[1] != 8:
        panic("bare-place push manifest wrong")

@test
def place_manifest_field() -> void:
    b: mutable Bag = Bag{items: []}
    b <- add_field(b, 3)
    b <- add_field(b, 4)
    b <- add_field(b, 5)
    if b.items.count != 3 or b.items[0] != 3 or b.items[2] != 5:
        panic("field-place push manifest wrong")
`

func TestLmutPlaceManifest(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "place_manifest", placeManifestBody)
	assertAllPassed(t, exit, stdout, stderr, "place_manifest_bare", "place_manifest_field")
}
