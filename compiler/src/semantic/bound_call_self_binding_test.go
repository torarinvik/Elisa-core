package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// `e: K = e` followed by `e.u`: once e is in scope its initializer's `e` resolves to the
// same symbol, and boundCallExpr (split-view optimization facts) followed it forever — a
// fatal Go stack overflow. Found by FuzzChecker.
func TestSelfInitializedBindingFieldReadTerminates(t *testing.T) {
	src := "struct K:\n    u: i32\n\ndef a() -> i32:\n    e: K = e\n    return e.u\n"
	p := parser.New(lexer.New("self_init.elisa", []byte(src)).Tokenize())
	file := p.ParseFile("self_init.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	got := strings.Join(Analyze(file).Errors(), "\n")
	if !strings.Contains(got, `undefined identifier "e"`) {
		t.Fatalf("unexpected diagnostics:\n%s", got)
	}
}
