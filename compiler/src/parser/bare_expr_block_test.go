package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/119 §2: bare block expressions (`x =` NEWLINE INDENT ... tail) parse onto
// ast.ExprBlock at var-decl, tuple-bind, and `<-` assign RHS positions; a bare
// `a, b` line inside an expression block is a tuple tail (and only there); the
// legacy `do:` spelling parses but emits a deprecation notice.

func parseSourceWithNotices(t *testing.T, src string) (*ast.File, []string, []string) {
	t.Helper()
	l := lexer.New("test.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := New(tokens)
	file := p.ParseFile("test.elisa")
	return file, p.Errors(), p.Notices()
}

func TestBareExprBlockOnVarDeclRHS(t *testing.T) {
	_, errs, notices := parseSourceWithNotices(t, "def f() -> i64:\n    y: i64 =\n        a: i64 = 40\n        a + 2\n    return y\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(notices) != 0 {
		t.Fatalf("bare form must not warn, got: %v", notices)
	}
}

func TestBareExprBlockTupleBindAndAssignRHS(t *testing.T) {
	_, errs, _ := parseSourceWithNotices(t, "def f() -> i64:\n    c, d =\n        a: i64 = 40\n        b: i64 = 2\n        a, b\n    m: mutable i64 = 0\n    m <-\n        c + d\n    return m\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
}

func TestBareTupleLineOutsideExprBlockStillErrors(t *testing.T) {
	_, errs, _ := parseSourceWithNotices(t, "def f() -> i64:\n    a: i64 = 1\n    b: i64 = 2\n    a, b\n    return a\n")
	if len(errs) == 0 {
		t.Fatalf("expected a parse error for a bare tuple line outside an expression block")
	}
}

func TestDoBlockEmitsDeprecationNotice(t *testing.T) {
	_, errs, notices := parseSourceWithNotices(t, "def f() -> i64:\n    y: i64 = do:\n        a: i64 = 40\n        a + 2\n    return y\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "`do:` expression blocks are deprecated") {
		t.Fatalf("expected one do: deprecation notice, got: %v", notices)
	}
}
