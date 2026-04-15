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

func TestParseCascadeExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Row:\n    ref_count: int\n\ndef nonzero(row: Row) -> bool:\n    return cascade row => .ref_count != 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[1])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cascade, ok := ret.Value.(*ast.CascadeExpr)
	if !ok {
		t.Fatalf("expected cascade expr, got %T", ret.Value)
	}
	if ident, ok := cascade.Target.(*ast.Ident); !ok || ident.Name != "row" {
		t.Fatalf("expected cascade target row, got %#v", cascade.Target)
	}
	binary, ok := cascade.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary body expr, got %T", cascade.Value)
	}
	if got := unparse.FormatExpr(binary.Left); got != ".ref_count" {
		t.Fatalf("expected shorthand left operand, got %q", got)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "cascade row => .ref_count != 0") {
		t.Fatalf("expected formatted output to preserve cascade expr, got:\n%s", formatted)
	}
}

func TestParseCascadeRemainsContextualIdentifier(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(cascade: int) -> int:\n    return cascade\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	if ident, ok := ret.Value.(*ast.Ident); !ok || ident.Name != "cascade" {
		t.Fatalf("expected cascade to remain an identifier, got %#v", ret.Value)
	}
}
