package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// `const X: i32 = X + 1` used to overflow the Go stack: the identifier branch of analyzeExpr
// followed the const's initializer through functionValueTypeForExpr without the in-progress
// guard that functionValueTypeFollowingBinding keeps. Each case must now terminate with the
// same "must be a compile-time" diagnostic the bare `const X: i32 = Y` cycle already gets.
func TestSelfReferentialConstInitializerTerminates(t *testing.T) {
	for _, src := range []string{
		"const X: i32 = X + 1\n\ndef main() -> i32:\n    return 0\n",
		"const X: i32 = Y + 1\nconst Y: i32 = X + 1\n\ndef main() -> i32:\n    return 0\n",
	} {
		p := parser.New(lexer.New("const_cycle.elisa", []byte(src)).Tokenize())
		file := p.ParseFile("const_cycle.elisa")
		if errs := p.Errors(); len(errs) != 0 {
			t.Fatalf("parse errors: %v", errs)
		}
		got := strings.Join(Analyze(file).Errors(), "\n")
		if !strings.Contains(got, `const "X" initializer must be a compile-time i32 value`) {
			t.Fatalf("unexpected diagnostics for %q:\n%s", src, got)
		}
	}
}
