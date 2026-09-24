package main

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func parseInterfaceTestSource(t *testing.T, source string) *ast.File {
	t.Helper()
	lex := lexer.New("interface.elisa", []byte(source))
	tokens := lex.Tokenize()
	if errs := lex.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	parse := parser.New(tokens)
	file := parse.ParseFile("interface.elisa")
	if errs := parse.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	return file
}

func TestInterfaceDropsAllPrivateStaticBranches(t *testing.T) {
	source := `static if POSIX:
	@internal
	def posix_only() -> i64:
		return 1
static elif WINDOWS:
	@internal
	def windows_only() -> i64:
		return 2
static else:
	@internal
	def other_only() -> i64:
		return 3
`
	generated := generateModuleInterface(parseInterfaceTestSource(t, source))
	if strings.Contains(generated, "static if") || strings.Contains(generated, "posix_only") || strings.Contains(generated, "windows_only") {
		t.Fatalf("interface retained a conditional with no public declarations:\n%s", generated)
	}
	parseInterfaceTestSource(t, generated)
}

func TestInterfaceKeepsPrivateStaticBranchSyntacticallyValid(t *testing.T) {
	source := `static if POSIX:
	@internal
	def hidden() -> i64:
		return 0
static elif WINDOWS:
	def visible() -> i64:
		return 1
`
	generated := generateModuleInterface(parseInterfaceTestSource(t, source))
	if strings.Contains(generated, "hidden") || !strings.Contains(generated, "static assert true") || !strings.Contains(generated, "visible") {
		t.Fatalf("interface did not preserve the private branch boundary:\n%s", generated)
	}
	parseInterfaceTestSource(t, generated)
}
