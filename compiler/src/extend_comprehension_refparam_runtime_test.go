package main

import "testing"

// A comprehension argument to `extend` FUSES — its elements are pushed straight into
// the receiver with no standalone temp. So `dst.extend([x for x in src])` must compile
// wherever `for x in src: dst.push(x)` does, including when `dst` is a `mutable T&` ref
// param (previously the fused comprehension demanded its own `in <arena>:` scope, so the
// exact form -Wperf suggests for a push loop failed to compile on ref-param receivers).
const extendComprehensionRefParamBody = `
def append_mapped(dst: mutable darray[i64]&, src: darray[i64]) -> void:
    can Memory.Allocate, Abort.Panic:
        dst.extend([x * 2 for x in src])

def append_filtered(dst: mutable darray[i64]&, src: darray[i64]) -> void:
    can Memory.Allocate, Abort.Panic:
        dst.extend([x for x in src if x > 0])

@test
def extend_comprehension_refparam() -> void:
    can Abort.Panic:
        d: mutable darray[i64] = [100]
        s: darray[i64] = [1, 2, 3]
        append_mapped(d, s)
        if d.count != 4 or d[1] != 2 or d[3] != 6:
            panic("mapped")
        d2: mutable darray[i64] = []
        s2: darray[i64] = [-1, 5, -3, 7]
        append_filtered(d2, s2)
        if d2.count != 2 or d2[0] != 5 or d2[1] != 7:
            panic("filtered")
`

func TestExtendComprehensionRefParam(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "extend_comprehension_refparam", extendComprehensionRefParamBody)
	assertAllPassed(t, exit, stdout, stderr, "extend_comprehension_refparam")
}
