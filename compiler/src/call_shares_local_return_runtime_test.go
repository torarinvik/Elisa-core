package main

import (
	"strings"
	"testing"
)

// region-return inference, CALL RESULT SHARING A LOCAL: a function that builds a container
// in a local owner and returns the result of an ordinary call that copies it back out
// (`return f(&p)` where `def f(p: P&) -> E: return E.X(p.items)`) returns that local's
// buffer. A darray copy shares its buffer (docs/84), so the callee allocates nothing and is
// never classified itself; the CALLER must thread the outer region.
//
// Before the fix the caller freed its `__auto_*` region at `ret`: the header came back
// intact and every element read as zero — a silent wrong answer in safe code. The direct
// spelling (`return E.X(p.items)` in the builder itself) was already classified and worked.
const callSharesLocalReturnBody = `
struct Bag:
    items: mutable darray[i64]
    n: i64

enum Wrapped:
    Some(darray[i64])
    Nothing

def wrap_ref(p: Bag&) -> Wrapped:
    return Wrapped.Some(p.items)

def wrap_lmut(p: lmut Bag) -> Wrapped:
    return Wrapped.Some(p.items)

def items_of(p: Bag&) -> darray[i64]:
    return p.items

def by_address(n: usize) -> Wrapped:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: mutable Bag = Bag{items: [], n: 0}
        i: mutable usize = 0
        while i < n |b, i|:
            b.items <- b.items.push(i.i64() * 3)
            i <- i + 1
        held: Wrapped = wrap_ref(&b)
        return held

def by_receiver(n: usize) -> Wrapped:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: mutable Bag = Bag{items: [], n: 0}
        i: mutable usize = 0
        while i < n |b, i|:
            b.items <- b.items.push(i.i64() * 3)
            i <- i + 1
        rebind held: Wrapped, b = b.wrap_lmut()
        return held

def by_ref_local(n: usize) -> darray[i64]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: mutable Bag = Bag{items: [], n: 0}
        i: mutable usize = 0
        while i < n |b, i|:
            b.items <- b.items.push(i.i64() * 3)
            i <- i + 1
        r: Bag& = &b
        return items_of(r)

def check(xs: darray[i64], label: sview) -> void:
    can Abort.Panic:
        if xs.count != 30000:
            panic("shared payload lost its count")
        sum: mutable i64 = 0
        for v in xs:
            sum <- sum + v
        if sum != 1349955000:
            panic("shared payload elements corrupted (UAF?)")

def churn() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        ys: mutable darray[i64] = []
        i: mutable usize = 0
        while i < 30000 |ys, i|:
            ys <- ys.push(7)
            i <- i + 1
        return ys[0]

@test
def call_shares_local_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: Wrapped = by_address(30000)
        b: Wrapped = by_receiver(30000)
        c: darray[i64] = by_ref_local(30000)
        _ = churn()
        match a:
            Wrapped.Some(xs):
                check(xs, "address")
            _:
                panic("by_address returned the wrong variant")
        match b:
            Wrapped.Some(xs):
                check(xs, "receiver")
            _:
                panic("by_receiver returned the wrong variant")
        check(c, "ref local")
`

func TestCallSharesLocalReturnAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "call_shares_local_return_uaf", callSharesLocalReturnBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "call_shares_local_return_lives")
}
