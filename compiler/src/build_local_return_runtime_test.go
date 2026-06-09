package main

import (
	"strings"
	"testing"
)

// region-return-inference Stage 1, RUNTIME SOUNDNESS under ASan: a build-local-return
// builder (`def collect(n) -> darray[i64]` that grows a local in the inferred region and
// returns it) must have its `__auto_*` region ADOPTED into the caller's region, so the
// caller can read every element AFTER the call. If the front-end suppressed the escape but
// the backend did NOT adopt (the silent-UAF failure mode the gate guards against), the
// builder's region would be freed on return and ASan would fault on the read-back.
const buildLocalReturnBody = `
def collect(n: usize) -> darray[i64]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        out: mutable darray[i64] = []
        i: mutable usize = 0
        while i < n:
            out.push(i.i64() * 2)
            i <- i + 1
        return out

@test
def build_local_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            xs: darray[i64] = collect(50000)
            if xs.count != 50000:
                panic("returned darray lost its count")
            sum: mutable i64 = 0
            for v in xs:
                sum <- sum + v
            if sum != 2499950000:
                panic("returned darray elements corrupted (UAF?)")
            if xs[0] != 0 or xs[49999] != 99998:
                panic("returned darray boundary corrupted")
`

func TestBuildLocalReturnAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "build_local_return_uaf", buildLocalReturnBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "build_local_return_lives")
}
