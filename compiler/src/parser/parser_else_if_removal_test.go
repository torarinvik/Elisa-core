package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// The two-word `else if` chain has been removed in favor of the canonical `elif`. It now
// fails at parse time with a directed diagnostic (rather than a confusing `expected :`
// cascade), while recovery still treats it as `elif` so the rest of the AST is intact.
func TestParseElseIfRejected(t *testing.T) {
	src := "def f(x: int) -> int:\n" +
		"    if x == 1:\n" +
		"        return 1\n" +
		"    else if x == 2:\n" +
		"        return 2\n" +
		"    else:\n" +
		"        return 3\n"
	_, errs := parseSourceFile(t, src)
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "`else if` is not valid; use `elif COND:`") {
		t.Fatalf("expected else-if removal diagnostic, got: %v", errs)
	}
	// Recovery must be clean: exactly the one directed diagnostic, no cascade.
	if len(errs) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %v", len(errs), errs)
	}
}

// The canonical `elif` chain still parses cleanly.
func TestParseElifChainStillAccepted(t *testing.T) {
	src := "def f(x: int) -> int:\n" +
		"    if x == 1:\n" +
		"        return 1\n" +
		"    elif x == 2:\n" +
		"        return 2\n" +
		"    else:\n" +
		"        return 3\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if _, ok := file.Decls[0].(*ast.FuncDecl); !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
}

// A plain trailing `else:` block (no `if`) is unaffected — it must not trip the else-if guard.
func TestParsePlainElseStillAccepted(t *testing.T) {
	src := "def f(x: int) -> int:\n" +
		"    if x == 1:\n" +
		"        return 1\n" +
		"    else:\n" +
		"        return 2\n"
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("plain else should still parse, got: %v", errs)
	}
}
