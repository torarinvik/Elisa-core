package main

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"elisacore/src/unparse"
)

func parseForFormat(t *testing.T, text string) *ast.File {
	t.Helper()
	l := lexer.New("visibility.elisa", []byte(text))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("visibility.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return file
}

// A module's `public:` / `private:` sections survive formatting. Dropping them turned
// every private member public, so the formatter was silently changing what the program
// means -- the one thing a formatter may never do.
func TestFormatKeepsVisibilitySections(t *testing.T) {
	src := "module Pack:\n" +
		"    public:\n" +
		"        def make() -> int:\n" +
		"            return 3\n" +
		"    private:\n" +
		"        def hidden() -> int:\n" +
		"            return 1\n"

	formatted := unparse.FormatFile(parseForFormat(t, src))
	for _, want := range []string{"    public:\n", "        def make() -> int:\n", "    private:\n", "        def hidden() -> int:\n"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted output lost %q:\n%s", want, formatted)
		}
	}

	// Formatting is idempotent, and the privacy it prints is the privacy the analyzer
	// then enforces -- the round trip keeps `hidden` unreachable from outside `Pack`.
	if again := unparse.FormatFile(parseForFormat(t, formatted)); again != formatted {
		t.Fatalf("formatting is not idempotent:\nfirst:\n%s\nsecond:\n%s", formatted, again)
	}
	probe := formatted + "\ndef main() -> int:\n    return Pack::hidden()\n"
	result := semantic.Analyze(parseForFormat(t, probe))
	private := false
	for _, diagnostic := range result.Diagnostics {
		if strings.Contains(diagnostic.Message, "is private to module") {
			private = true
		}
	}
	if !private {
		t.Fatalf("reformatted source no longer hides `hidden`: %v", result.Diagnostics)
	}
}
