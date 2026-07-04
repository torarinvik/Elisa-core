//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// mergeMatchExprArmTypes: string-LITERAL arms adapt to a string-view result in a
// match-expression, mirroring the contextual ternary (`s: sview = "lit" if c else v`).
// Non-literal `static u8&` values (no knowable length) stay incompatible, as in the
// ternary.

func TestMatchExprLiteralArmsAdaptToSview(t *testing.T) {
	errs := semanticErrorsFor(t, `
def classify(k: int, v: sview) -> sview:
    s: sview = match k:
        1: "one"
        2: v
        _: ""
    return s
`)
	for _, e := range errs {
		if strings.Contains(e, "incompatible") {
			t.Fatalf("literal arms must adapt to the sview arm, got: %v", errs)
		}
	}
}

func TestMatchExprLiteralArmsAdaptToSviewReverseOrder(t *testing.T) {
	// All-literal prefix, view arm last: the accumulated static-u8& result adapts.
	errs := semanticErrorsFor(t, `
def classify(k: int, v: sview) -> sview:
    s: sview = match k:
        1: "one"
        2: "two"
        _: v
    return s
`)
	for _, e := range errs {
		if strings.Contains(e, "incompatible") {
			t.Fatalf("an all-literal prefix must adapt to a trailing sview arm, got: %v", errs)
		}
	}
}

func TestMatchExprNonLiteralStaticRefArmStaysIncompatible(t *testing.T) {
	// A static u8& VALUE (not a literal) has no length — it must not silently merge.
	requireOneErrorContaining(t, `
def classify(k: int, v: sview, p: static u8&) -> sview:
    s: sview = match k:
        1: p
        _: v
    return s
`, "match expression arms are incompatible")
}
