//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS (audit, cluster F): signed `INT_MIN / -1` overflows (the true result -INT_MIN is not
// representable; on the arm64 target it wraps to INT_MIN, no trap). The exact unbounded-ℤ model gave
// -INT_MIN (positive), wrongly proving `result >= 0` for `a < 0, b == -1`. The fix abstracts the result
// when the INT_MIN/-1 overflow cannot be ruled out.
func TestSignedDivIntMinOverflowDeclines(t *testing.T) {
	src := `
def negdiv(a: i64, b: i64) -> i64:
    requires a < 0
    requires b == 0 - 1
    ensure result >= 0
    return a / b
`
	errs := strings.Join(analyzeContractStrict(t, "negdiv.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("INT_MIN/-1 overflows; `result >= 0` must NOT be proven for a<0,b==-1, got: %v", errs)
	}
}

// COMPLETENESS: division that cannot hit INT_MIN/-1 still proves — a non-negative dividend, and a
// signed dividend bounded above the type minimum with a constant divisor other than -1.
func TestSignedDivSafeCasesStillProve(t *testing.T) {
	cases := []struct{ name, src string }{
		{"nonneg", `
law B(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type H = i64 is B[0, 100]
def half(n: H) -> i64 is B[0, 50]:
    return n / 2
`},
		{"bounded_signed", `
law B(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type R = i64 is B[0 - 100, 100]
def d(a: R) -> i64 is B[0 - 100, 100]:
    return a / 2
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if errs := analyzeContractStrict(t, "divsafe_"+tc.name+".elisa", tc.src).Errors(); len(errs) != 0 {
				t.Fatalf("a division that cannot overflow should still prove, got: %v", errs)
			}
		})
	}
}
