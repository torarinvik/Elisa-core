package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestASTStorageOverlapDiagnosticNamesPartialViews(t *testing.T) {
	args := []ast.Expr{&ast.IntLit{Value: "1"}, &ast.IntLit{Value: "2"}}
	call := &ast.CallExpr{Args: args, ResolvedArgs: args[1:]}
	fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ExprStmt{Expr: call}}}
	got := firstASTStorageOverlap(fn)
	if !strings.Contains(got, ".Args") || !strings.Contains(got, ".ResolvedArgs") {
		t.Fatalf("missing overlapping field names: %s", got)
	}
	call.ResolvedArgs = args
	if got := firstASTStorageOverlap(fn); got != "no overlapping AST node or slice storage found" {
		t.Fatalf("exact shared slice should deduplicate: %s", got)
	}
	call.Args = []ast.Expr{call}
	call.ResolvedArgs = nil
	if got := firstASTStorageOverlap(fn); got != "no overlapping AST node or slice storage found" {
		t.Fatalf("node cycle should deduplicate: %s", got)
	}
}

func TestASTStorageOverlapReportsIncompleteTraversal(t *testing.T) {
	fn := &ast.FuncDecl{Name: "f"}

	if got := firstASTStorageOverlapWithLimits(fn, 0, 100); !strings.Contains(got, "incomplete") || !strings.Contains(got, "depth limit") {
		t.Fatalf("depth-limited scan should be reported as incomplete: %s", got)
	}
	if got := firstASTStorageOverlapWithLimits(fn, 10, 1); !strings.Contains(got, "incomplete") || !strings.Contains(got, "work limit") {
		t.Fatalf("work-limited scan should be reported as incomplete: %s", got)
	}
}
