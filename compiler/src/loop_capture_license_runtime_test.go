package main

import "testing"

// docs/120 §9: `while cond |p|:` — the capture licenses the mutating condition call
// under §10 and the loop threads in place.
const loopCaptureLicenseBody = `
struct P:
    pos: mutable i64

def accept(p: lmut P, want: i64) -> bool:
    if p.pos == want:
        p.pos <- p.pos + 1
        return true
    return false

def skip(p: lmut P) -> void:
    want: mutable i64 = 0
    while p.accept(want) |p, want|:
        want <- want + 1
        break if p.pos >= 3

@test
def captured_loop_threads() -> void:
    p: mutable P = P{pos: 0}
    p <- p.skip()
    if p.pos != 3:
        panic("captured loop did not thread")
`

func TestLoopCaptureLicense(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "loop_capture_license", loopCaptureLicenseBody)
	assertAllPassed(t, exit, stdout, stderr, "captured_loop_threads")
}
