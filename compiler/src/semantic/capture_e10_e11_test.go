//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/119 §6: a `|capture|` target must name an existing MUTABLE binding —
// E10 (immutable) / E11 (undefined). A licensed capture that mutates its target is
// otherwise exempt from E4.

func TestCaptureOfImmutableIsE10(t *testing.T) {
	requireOneErrorContaining(t, `
def f(xs: darray[i64]) -> i64:
    total: i64 = 0
    r: i64 =
        for x in xs |acc = 0, total| -> acc:
            acc <- acc + 1
    return total + r
`, `capture "total" is not a `+"`mutable`"+` binding`)
}

func TestCaptureOfUndefinedIsE11(t *testing.T) {
	requireOneErrorContaining(t, `
def f(xs: darray[i64]) -> i64:
    r: i64 =
        for x in xs |acc = 0, missing| -> acc:
            acc <- acc + 1
    return r
`, `capture "missing" names no binding in scope`)
}

func TestCaptureLicensesOuterMutation(t *testing.T) {
	// A captured mutable may be directly assigned inside the value block (no E4).
	errs := semanticErrorsFor(t, `
def f(xs: darray[i64]) -> i64:
    total: mutable i64 = 0
    r: i64 =
        for x in xs |acc = 0, total| -> acc:
            total <- total + x
            acc <- acc + 1
    return total + r
`)
	for _, e := range errs {
		if strings.Contains(e, "value block may not mutate") {
			t.Fatalf("a captured mutable must be licensed for mutation, got: %v", errs)
		}
	}
}
