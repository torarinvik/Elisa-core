package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeIndexFallbackContextualizesFallback(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "index_fallback_contextual.elisa", `def read(xs: darray[usize]) -> usize:
	return xs[0] else 0
`)
	sym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected read func decl, got %T", sym.Node)
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	indexExpr, ok := ret.Value.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected index expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[indexExpr.Index].String(); got != "usize" {
		t.Fatalf("expected index to contextualize to usize, got %s", got)
	}
	if got := result.ExprTypes[indexExpr.Fallback].String(); got != "usize" {
		t.Fatalf("expected fallback to contextualize to usize, got %s", got)
	}
}

func TestAnalyzeIndexFallbackContextualizesStringReferenceFallback(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "index_fallback_string_ref.elisa", `def read(xs: darray[u8&], i: usize) -> u8&:
	return xs[i] else ""
`)
	sym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected read func decl, got %T", sym.Node)
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	indexExpr, ok := ret.Value.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected index expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[indexExpr].String(); got != "u8&" {
		t.Fatalf("expected index fallback result to be u8&, got %s", got)
	}
	if got := result.ExprTypes[indexExpr.Fallback].String(); got != "u8&" {
		t.Fatalf("expected fallback to contextualize to u8&, got %s", got)
	}
}

func TestAnalyzeIndexFallbackRejectsTypeMismatch(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "index_fallback_type_mismatch.elisa", `def read(xs: darray[int], i: usize) -> int:
	return xs[i] else false
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "index fallback expects int, got bool") {
		t.Fatalf("expected index fallback type mismatch diagnostic, got:\n%s", all)
	}
}
