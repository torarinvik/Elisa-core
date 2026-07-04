package main

import "testing"

// docs/119 §6 goldens: `|capture|` header sugar. A bare name in a loop-expression
// header captures an in-scope mutable, licensing the value-block body to mutate it
// (an E4 exemption via ExprBlock.Captures). The header is the loop's mutation
// contract — reading it tells you every outer binding the loop can change.
const captureBody = `
def sum_and_count(xs: darray[i64]) -> i64:
    total: mutable i64 = 0
    r: i64 =
        for x in xs |acc = 0, total| -> acc:
            total <- total + x
            acc <- acc + 1
    return total * 1000 + r

def two_captures(xs: darray[i64]) -> i64:
    lo: mutable i64 = 999
    hi: mutable i64 = -999
    n: i64 =
        for x in xs |seen = 0, lo, hi| -> seen:
            if x < lo:
                lo <- x
            if x > hi:
                hi <- x
            seen <- seen + 1
    return lo * 10000 + hi * 100 + n

@test
def capture_headers() -> void:
    can Abort.Panic:
        xs: darray[i64] = [10, 20, 30]
        if sum_and_count(xs) != 60003:
            panic("sum_and_count")
        ys: darray[i64] = [5, 2, 9, 4]
        # lo=2, hi=9, n=4 -> 2*10000 + 9*100 + 4 = 20904
        if two_captures(ys) != 20904:
            panic("two_captures")
`

func TestCaptureHeaders(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "capture_headers", captureBody)
	assertAllPassed(t, exit, stdout, stderr, "capture_headers")
}
