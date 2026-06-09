package main

import (
	"strings"
	"testing"
)

// region-return-inference Stage 1, dict/set RUNTIME parity under ASan. The semantic
// classifier accepts a build-local-return builder for dict and set too, but only
// darray/dstr were runtime-verified. These confirm the backend also adopts the dict
// (open-addressing table) and set backings into the caller's threaded region — the
// builder's `__auto_*` region must NOT be freed on return, or the caller's read-back
// would fault under ASan.

const buildLocalReturnDictBody = `
def make_dict(n: usize) -> dict[i64, i64]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        d: mutable dict[i64, i64] = {}
        i: mutable usize = 0
        while i < n:
            d.put(i.i64(), i.i64() * 3)
            i <- i + 1
        return d

@test
def dict_build_local_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            d: dict[i64, i64] = make_dict(5000)
            if d.count != 5000:
                panic("returned dict lost entries")
            sum: mutable i64 = 0
            i: mutable usize = 0
            while i < 5000:
                if not d.contains(i.i64()):
                    panic("returned dict missing key (UAF?)")
                slot: i64&? = d.get(i.i64())
                if slot == null:
                    panic("returned dict get failed")
                else:
                    sum <- sum + slot
                i <- i + 1
            if sum != 37492500:
                panic("returned dict values corrupted")
`

func TestBuildLocalReturnDictAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "build_local_return_dict", buildLocalReturnDictBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "dict_build_local_return_lives")
}

const buildLocalReturnSetBody = `
def make_set(n: usize) -> set[i64]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        s: mutable set[i64] = {}
        i: mutable usize = 0
        while i < n:
            _ = s.add(i.i64())
            i <- i + 1
        return s

@test
def set_build_local_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            s: set[i64] = make_set(5000)
            i: mutable usize = 0
            while i < 5000:
                if not s.contains(i.i64()):
                    panic("returned set missing member (UAF?)")
                i <- i + 1
            if s.contains(5000.i64()):
                panic("returned set has phantom member")
`

func TestBuildLocalReturnSetAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "build_local_return_set", buildLocalReturnSetBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "set_build_local_return_lives")
}
