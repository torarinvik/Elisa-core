package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Returning an `if`/`elif`/`while` condition binder of a region-polymorphic call's result
// returns region-allocated storage. The function was not classified region-polymorphic, so
// it handed the callee a per-call arena and freed it at `ret`: the returned struct's buffer
// was reused by the caller's next allocations (a silent wrong sum). The `get … else`
// spelling of the same function was already classified.
const regionPolyConditionBinderSource = `
struct Outer:
    x: u64
    items: darray[u64]

def make(n: u64) -> Outer? can[Memory.Allocate]:
    return null if n == 2
    items: mutable darray[u64] = []
    for i in 0..<n |items, n|:
        items.push(i + 100 * n)
    Outer{x: n, items: items}

def SHAPE(k: u64) -> Outer? can[Memory.Allocate]:
BODY

def SHAPE_total() -> u64 can[Memory.Allocate]:
    junk: mutable darray[u64] = []
    sum: mutable u64 = 0
    for k in 1..<6 |sum, junk|:
        if SHAPE(k.u64()) is outer:
            for i in 0..<8 |junk|:
                junk.push(77)
            for item in outer.items |sum|:
                sum <- sum + item
    sum

@test
def SHAPE_test() -> void:
    can Memory.Allocate, Abort.Panic:
        if SHAPE_total() != 5119:
            panic("a returned condition binder lost its items")
`

func TestRegionPolyConditionBinderOutlivesCall(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	shapes := map[string]string{
		"if_binder": `    if make(k) is outer:
        return outer
    null`,
		"elif_binder": `    if k == 100:
        return null
    elif make(k) is outer:
        return outer
    null`,
		"while_binder": `    cursor: mutable u64 = 0
    while cursor < 1 and make(k) is outer |cursor, k|:
        return outer
    null`,
		"loop_if_binder": `    for cursor in 1..<4 |k|:
        if make(k * cursor.u64() / cursor.u64()) is outer:
            return outer
    null`,
	}
	for shape, body := range shapes {
		t.Run(shape, func(t *testing.T) {
			src := strings.ReplaceAll(strings.ReplaceAll(regionPolyConditionBinderSource, "BODY", body), "SHAPE", shape)
			runSingleTestProgramExpectOK(t, shape+"_test", src, "a returned condition binder lived in a per-call arena")
		})
	}
}
