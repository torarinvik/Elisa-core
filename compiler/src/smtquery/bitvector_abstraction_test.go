package smtquery

import (
	"strings"
	"testing"
)

func TestBitvectorAbstractionPreservesBoundsAndCongruence(t *testing.T) {
	xor := "(bv2nat (bvxor ((_ int2bv 64) x) ((_ int2bv 64) y)))"
	query := "(assert (= " + xor + " " + xor + "))\n"
	result, ok := AbstractBitvectorReads(query)
	if !ok || strings.Count(result, "(declare-fun bvread$xor$64") != 1 || strings.Count(result, "(bvread$xor$64 x y)") < 2 || !strings.Contains(result, "18446744073709551616") || strings.Contains(result, "bv2nat") {
		t.Fatalf("invalid abstraction (ok=%v): %s", ok, result)
	}
	for _, q := range []string{"(assert (= x 1))", "(assert (= (bv2nat ((_ int2bv 64) x", "(assert (= (bv2nat ((_ int2bv 64) x)) ((_ int2bv 64) y)))"} {
		if got, ok := AbstractBitvectorReads(q); ok {
			t.Fatalf("accepted unsupported query %q: %s", q, got)
		}
	}
}

func TestBitvectorAbstractionMarkerAndWidth(t *testing.T) {
	input := "(assert p)" + AbstractionOnlyMarker + "\n(assert q)\n"
	if got := StripAbstractionOnly(input); got != "(assert q)\n" {
		t.Fatalf("marker stripping: %q", got)
	}
	if width, ok := BitvectorReadWidth("(bv2nat (bvand ((_ int2bv 64) x) ((_ int2bv 64) y)))"); !ok || width != 64 {
		t.Fatalf("width = %d, %v", width, ok)
	}
	if _, ok := BitvectorReadWidth("(bv2nat (bvand ((_ int2bv 64) x) ((_ int2bv 32) y)))"); ok {
		t.Fatal("accepted mixed widths")
	}
}
