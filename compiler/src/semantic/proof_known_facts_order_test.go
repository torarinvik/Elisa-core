package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func knownFactsOrderAnalyze(t *testing.T, src []byte) string {
	t.Helper()
	p := parser.New(lexer.New("order.elisa", src).Tokenize())
	file := p.ParseFile("order.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	r := Analyze(file)
	return strings.Join(r.Errors(), "\n") + "\n--notices--\n" + strings.Join(r.Notices(), "\n")
}

// The "known facts" list of an unproven-precondition warning was filled by ranging over the
// visible range-fact MAP (and capped at five), so the order of `a`/`b` below flipped from
// run to run. Found by FuzzChecker's double-analysis determinism check.
func TestProofKnownFactsAreOrderedDeterministically(t *testing.T) {
	src := []byte(`law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Five = i64 is Bounded[2, 5]

def needs(a: i64, b: i64) -> i64:
    requires a * b <= 10
    return a + b

def caller(a: Five, b: Five) -> i64:
    return needs(a, b)
`)
	first := knownFactsOrderAnalyze(t, src)
	ia := strings.Index(first, "2 <= a <= 5")
	ib := strings.Index(first, "2 <= b <= 5")
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("expected range facts for a then b, got:\n%s", first)
	}
	for i := 0; i < 30; i++ {
		if got := knownFactsOrderAnalyze(t, src); got != first {
			t.Fatalf("run %d differs:\n%s\n=== vs ===\n%s", i, first, got)
		}
	}
}
