//go:build cgo

package main

import "testing"

// An extern resource is released exactly once at scope exit through its __drop__, a borrow
// does not consume it, and a failed constructor releases nothing. docs/127 §3.2.
func TestExternResourceRuntime(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "extern_resource", `extern resource CFile

global mutable releases: i64 = 0

def __drop__(self: CFile) -> void:
    releases <- releases + 1
    _ = fclose(move self)

@callconv(c)
extern fopen(path: cstr, mode: cstr) -> CFile?
@callconv(c)
extern fclose(file: CFile) -> i32
@callconv(c)
extern fgetc(file: CFile&) -> i32

def first_byte(path: cstr) -> i64:
    file: CFile = get fopen(path, "r") else return -1
    return fgetc(file).i64()

def check_resource() -> i64:
    first: i64 = first_byte("/etc/hosts")
    return 1 if first < 0
    return 2 if releases != 1
    missing: i64 = first_byte("/nonexistent/x")
    return 3 if missing != -1
    return 4 if releases != 1
    return 0

@test
def extern_resource() -> void:
    can Abort.Panic:
        assert check_resource() == 0
`)
	if exit != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
}
