package main

import (
	"testing"
)

// Regression: `try <call> else <fallback>` over a VOID error union used to emit
// `unreachable` in the merge block whenever the success payload was void — the
// phi bookkeeping (incomingBlocks) doubled as the merge-reachability check, and
// void results never append phi incomings. Result: a runtime trap on the OK
// path even though the callee succeeded (`catch` over the same call worked).
// The lowering now tracks merge reachability separately from phi incomings.
const tryVoidElseBody = `
error TryVoidErr:
    Bad

def try_void_ok() -> void error[TryVoidErr]:
    return

def try_void_bad() -> void error[TryVoidErr]:
    raise TryVoidErr.Bad

@test
def try_void_else_ok_path() -> void:
    can Abort.Panic:
        try try_void_ok() else e:
            panic("ok path took the fallback")

@test
def try_void_else_err_path() -> void:
    can Abort.Panic:
        caught: mutable i64 = 0
        try try_void_bad() else e:
            caught <- 1
        if caught != 1:
            panic("error path skipped the fallback")

@test
def try_void_else_control_flow_fallback() -> void:
    can Abort.Panic:
        try try_void_ok() else return
        try try_void_bad() else return
        panic("error path did not take the return fallback")
`

func TestTryVoidElseRuntime(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "try_void_else", tryVoidElseBody)
	assertAllPassed(t, exit, stdout, stderr,
		"try_void_else_ok_path",
		"try_void_else_err_path",
		"try_void_else_control_flow_fallback")
}
