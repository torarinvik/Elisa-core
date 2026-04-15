package semantic_test

import (
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
)

func TestAnalyzeCascadeBlocksLowerToOrdinaryStatements(t *testing.T) {
	src := `struct Inner:
    value: mutable int

struct Report:
    inner: Inner

def build(report: mutable any Report&, delta: int) -> int:
    cascade report:
        .inner.value <- delta
        if delta > 0:
            .inner.value <- delta + 1
        cascade .inner:
            .value <- delta + 2
    return report.inner.value
`

	result, errs := parseAndAnalyze(t, "cascade_blocks.llcontext", src)
	requireNoErrors(t, errs)
	build := requireFuncDecl(t, result, "build")

	if len(build.Body) != 4 {
		t.Fatalf("expected cascade lowering to flatten into 4 statements, got %d", len(build.Body))
	}
	for i, stmt := range build.Body[:3] {
		if _, ok := stmt.(*ast.CascadeStmt); ok {
			t.Fatalf("expected no cascade statements after analysis, found one at index %d", i)
		}
	}

	assign0, ok := build.Body[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected first lowered statement to be assignment, got %T", build.Body[0])
	}
	if got := unparse.FormatExpr(assign0.Target); got != "report.inner.value" {
		t.Fatalf("expected first cascade target to lower to report.inner.value, got %q", got)
	}

	ifStmt, ok := build.Body[1].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected second lowered statement to be if, got %T", build.Body[1])
	}
	assign1, ok := ifStmt.Then[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected nested block statement to be assignment, got %T", ifStmt.Then[0])
	}
	if got := unparse.FormatExpr(assign1.Target); got != "report.inner.value" {
		t.Fatalf("expected nested cascade target to lower to report.inner.value, got %q", got)
	}

	assign2, ok := build.Body[2].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected nested cascade to flatten into assignment, got %T", build.Body[2])
	}
	if got := unparse.FormatExpr(assign2.Target); got != "report.inner.value" {
		t.Fatalf("expected nested cascade target to lower to report.inner.value, got %q", got)
	}
}
