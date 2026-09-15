//go:build cgo

package main

import "testing"

// A bounded view crosses to libc strnlen() as (ptr, len): the callee sees exactly the
// view's bytes and its bound, so a NUL-free 3-byte view yields 3. docs/127 §3.3.
func TestExternViewSplitRuntime(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "extern_view_split", `@callconv(c)
extern strnlen(text: view[u8]) -> usize

def check_view_split() -> i64:
    text: mutable darray[u8] = []
    text.push(104)
    text.push(105)
    text.push(10)
    text.push(0)
    bounded: usize = strnlen(text[0:2])
    unbounded: usize = strnlen(text[0:4])
    return bounded.i64() * 10 + unbounded.i64() - 23

@test
def extern_view_split() -> void:
    can Abort.Panic:
        assert check_view_split() == 0
`)
	if exit != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
}
