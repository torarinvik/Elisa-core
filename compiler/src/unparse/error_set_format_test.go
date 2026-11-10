package unparse_test

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/unparse"
)

func TestQualifiedErrorSetsRoundTrip(t *testing.T) {
	const source = "extern load() -> i64 error[Outer::Disk::Fault{Bad, Gone}, *Net::Fault, ...]\n"
	const expected = "extern load() -> i64 error[Outer::Disk::Fault.Bad, Outer::Disk::Fault.Gone, *Net::Fault, ...]"
	parse := func(text string) *ast.File {
		t.Helper()
		const filename = "error_sets.elisai"
		p := parser.New(lexer.New(filename, []byte(text)).Tokenize())
		file := p.ParseFile(filename)
		if len(p.Errors()) != 0 {
			t.Fatalf("parse %q: %v", text, p.Errors())
		}
		return file
	}

	formatted := unparse.FormatFile(parse(source))
	if strings.TrimSpace(formatted) != expected {
		t.Fatalf("unexpected error-set spelling:\n%s", formatted)
	}
	if again := unparse.FormatFile(parse(formatted)); again != formatted {
		t.Fatalf("formatted error set changed on round trip:\n%s", again)
	}
}
