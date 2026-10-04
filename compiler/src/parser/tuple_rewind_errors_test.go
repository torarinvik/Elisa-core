package parser

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
)

// A statement-level comma list that is not a valid tuple is abandoned and the parser rewinds
// to the token after the comma. The abandoned parse's diagnostics used to survive the rewind,
// so a run of n commas reported each later comma O(n) times: O(n^2) output and time (found by
// FuzzParser as a 4000-comma hang). Each diagnostic must now appear once.
func TestRewoundTupleParseDoesNotDuplicateDiagnostics(t *testing.T) {
	src := "def f() -> i64:\n    a: mu" + strings.Repeat(",", 300) + "table i64 = 0\n    return 0\n"
	p := New(lexer.New("commas.elisa", []byte(src)).Tokenize())
	p.ParseFile("commas.elisa")
	errs := p.Errors()
	seen := map[string]bool{}
	for _, e := range errs {
		if seen[e] {
			t.Fatalf("duplicate diagnostic %q (%d diagnostics total)", e, len(errs))
		}
		seen[e] = true
	}
	if len(errs) == 0 || len(errs) > 4*300 {
		t.Fatalf("expected a linear number of diagnostics, got %d", len(errs))
	}
}
