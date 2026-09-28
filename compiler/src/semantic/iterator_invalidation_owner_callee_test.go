//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Iterator invalidation through the OWNER of an iterated field: `for v in p.items: step(p)` where
// the callee reaches p.items through p. Before the fix both compilers accepted it, and a callee that
// cleared and refilled p.items made the loop walk the overwritten buffer (exit 151, not 10).

const iterOwnerCalleePrelude = `struct P:
    items: mutable darray[i64]
    n: mutable i64

def step(p: mutable P&, v: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        if v == 1:
            p.items.clear()
            p.items.push(50)
        p.n <- p.n + v

def lstep(p: lmut P, v: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        p.items <- p.items.push(v)

def peek(p: P&) -> i64:
    return p.n

`

func TestIterInvalidationOwnerCalleeRejected(t *testing.T) {
	cases := map[string]string{
		"mutable_ref": `def run(p: mutable P&) -> void:
    can Memory.Allocate, Abort.Panic:
        for v in p.items:
            step(p, v)
`,
		"address_of": `def run() -> void:
    can Memory.Allocate, Abort.Panic:
        p: mutable P = P{items: [1, 2], n: 0}
        for v in p.items:
            step(&p, v)
`,
		"lmut_threaded": `def run(p: lmut P) -> void:
    can Memory.Allocate, Abort.Panic:
        for v in p.items |p|:
            p <- p.lstep(v)
`,
	}
	for name, body := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "iter_owner_"+name+".elisa", iterOwnerCalleePrelude+body)
		all := strings.Join(result.Errors(), "\n")
		if !strings.Contains(all, `while "p.items" is being iterated`) {
			t.Fatalf("%s: passing the owner of an iterated field by mutable reference must be rejected, got: %s", name, all)
		}
	}
}

func TestIterInvalidationOwnerCalleeAllowed(t *testing.T) {
	cases := map[string]string{
		// A read-only borrow of the owner cannot relocate the field.
		"immutable_ref": `def run(p: mutable P&) -> i64:
    can Memory.Allocate, Abort.Panic:
        total: mutable i64 = 0
        for v in p.items:
            total <- total + peek(p)
        return total
`,
		// Iterating by index up to a saved count re-reads a bounds-checked element each time.
		"index_loop": `def run(p: mutable P&) -> void:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<p.items.count:
            step(p, p.items[i])
`,
		// Passing the owner after the loop is outside the iteration.
		"after_loop": `def run(p: mutable P&) -> void:
    can Memory.Allocate, Abort.Panic:
        total: mutable i64 = 0
        for v in p.items:
            total <- total + v
        step(p, total)
`,
	}
	for name, body := range cases {
		result := analyzeFunctionAnalysisTestSource(t, "iter_owner_ok_"+name+".elisa", iterOwnerCalleePrelude+body)
		if errs := result.Errors(); len(errs) != 0 {
			t.Fatalf("%s: must be allowed, got: %s", name, strings.Join(errs, "\n"))
		}
	}
}
