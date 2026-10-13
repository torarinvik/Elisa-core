package semantic

import (
	"strings"
	"testing"
)

func TestLocalReturnEquationPreservesFixedWidthArithmetic(t *testing.T) {
	src := `
def inc(x: i64) -> i64:
    y: i64 = x + 1
    return y

def caller(x: i64) -> i64:
    ensure result > x
    return inc(x)
`
	errs := strings.Join(analyzeContractStrict(t, "local_return_overflow.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("local-return equation must respect i64 wraparound, got: %v", errs)
	}
}
