package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
)

func TestParseCascadeStmt(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Inner:\n    value: int\n\nstruct Report:\n    inner: Inner\n\ndef build(report: Report, value: int) -> void:\n    cascade report:\n        .inner.value <- value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	buildDecl, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[2])
	}
	cascade, ok := buildDecl.Body[0].(*ast.CascadeStmt)
	if !ok {
		t.Fatalf("expected cascade statement, got %T", buildDecl.Body[0])
	}
	if ident, ok := cascade.Target.(*ast.Ident); !ok || ident.Name != "report" {
		t.Fatalf("expected cascade target to be report, got %#v", cascade.Target)
	}
	assign, ok := cascade.Body[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected assignment in cascade body, got %T", cascade.Body[0])
	}
	if got := unparse.FormatExpr(assign.Target); got != ".inner.value" {
		t.Fatalf("expected leading-dot shorthand target, got %q", got)
	}
	formatted := unparse.FormatDecl(buildDecl)
	for _, want := range []string{"cascade report:", ".inner.value <- value"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}
