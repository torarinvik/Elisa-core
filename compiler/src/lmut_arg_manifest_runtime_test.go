package main

import "testing"

// docs/120 §8 goldens: the arg-manifest form. `lmut` values follow affine value semantics
// at the source level (every mutation is a reassignment that names the value) and mutate
// in place at the machine level. The bridge: a mutating call is written as a reassignment
// of the values it mutates — `x <- x.method(…)`, `t, b <- f(…, t, …, b, …)` — where the
// call returns void and the targets are the mutable bindings it mutates. It erases to just
// the call (zero overhead), so the reassignment is pure source-level dataflow.
const argManifestBody = `
struct T:
    n: mutable i64

def bump(v: i64, a: lmut T, b: lmut T) -> void:
    a.n <- a.n + v
    b.n <- b.n + 1

def push1(b: lmut T) -> void:
    b.n <- b.n + 100

@test
def arg_manifest_multi_and_single() -> void:
    a: mutable T = T{n: 0}
    b: mutable T = T{n: 0}
    # multi-target manifest: this line mutates a and b (no comment needed)
    a, b <- bump(5, a, b)
    if a.n != 5 or b.n != 1:
        panic("multi-target manifest wrong")
    # single-target manifest: this line mutates b
    b <- push1(b)
    if b.n != 101:
        panic("single-target manifest wrong")

@test
def arg_manifest_chained_in_sequence() -> void:
    b: mutable T = T{n: 0}
    b <- push1(b)
    b <- push1(b)
    b <- push1(b)
    if b.n != 300:
        panic("sequential manifest threading wrong")
`

func TestLmutArgManifest(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "arg_manifest", argManifestBody)
	assertAllPassed(t, exit, stdout, stderr, "arg_manifest_multi_and_single", "arg_manifest_chained_in_sequence")
}
