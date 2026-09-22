package main

import "testing"

// `&r` where `r` is an immutable reference binding (`r: T&`) is typed `T&&` by the analyzer. It
// must lower to the address of r's own slot: lowering it to the referenced pointer (emitAddress's
// auto-deref, right for `r.field` and `r[i]`) made every `T&&` destination dereference the Counter
// itself as if it were a pointer, and the program segfaulted. A CAST of `&r` is not such a use:
// `(&r).cast[U&]` is the reborrow idiom and reinterprets the referent (see
// TestS4Stage3ReborrowCastPreservesRegion), so it must keep lowering to the Counter.
const referenceParamAddressBody = `struct Counter:
    values: mutable i64[4]
    count: mutable usize

def takes_rr(counter: Counter&&) -> usize:
    counter[0].count

def id[T](x: T) -> T:
    x

def direct(counter: Counter&) -> usize:
    takes_rr(&counter)

def parenthesized(counter: Counter&) -> usize:
    takes_rr((&counter))

def through_local(counter: Counter&) -> usize:
    r = &counter
    takes_rr(r)

def through_generic(counter: Counter&) -> usize:
    r = id(&counter)
    takes_rr(r)

def reborrow_address(counter: Counter&) -> usize:
    can Unsafe.PointerCast:
        p = (&counter).cast[void&]
        return p.cast[usize]

def pointee_address(counter: Counter&) -> usize:
    can Unsafe.PointerCast:
        return counter.cast[void&].cast[usize]

def plus_one(n: i64) -> i64:
    n + 1

def join_all(group: mutable TaskGroup&) -> void:
    can Pool.WaitAll, Memory.Release, Abort.Panic, Atomics.CompareExchange, Atomics.Load, Thread.Join, Unsafe.PointerCast:
        wait all group

def bump(mu: mutable Mutex&, total: mutable i64&) -> void:
    can Sync.Lock, Sync.Unlock, Abort.Panic:
        lock mu as g:
            total <- total + 1

@test
def reference_param_address() -> void:
    c: Counter = Counter{values: [9, 0, 0, 0], count: 5}
    can Abort.Panic:
        if direct(&c) != 5:
            panic("takes_rr(&counter)")
        if parenthesized(&c) != 5:
            panic("takes_rr((&counter))")
        if through_local(&c) != 5:
            panic("r = &counter")
        if through_generic(&c) != 5:
            panic("id(&counter)")
        if reborrow_address(&c) != pointee_address(&c):
            panic("(&counter).cast[void&] is the reference's slot, not the Counter")

@test
def lock_on_reference_param() -> void:
    can Memory.Allocate, Memory.Release, Sync.Lock, Sync.Unlock, Abort.Panic:
        mu: mutable Mutex = mutex()
        total: mutable i64 = 0
        bump(&mu, &total)
        bump(&mu, &total)
        mutex_dispose(&mu)
        if total != 2:
            panic("lock on a Mutex& parameter")

@test
def wait_all_on_reference_param() -> void:
    can Thread.Spawn, Thread.Join, Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange, Unsafe.PointerCast:
        pool workers(2):
            group: mutable TaskGroup = task_group_new()
            first: Task[i64, Pending] = submit plus_one(1)
            task_group_add((&group).cast[TaskGroup&], move first)
            join_all(&group)
            wait all group
`

func TestReferenceParamAddress(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "reference_param_address", referenceParamAddressBody)
	assertAllPassed(t, exit, stdout, stderr, "reference_param_address", "lock_on_reference_param", "wait_all_on_reference_param")
}
