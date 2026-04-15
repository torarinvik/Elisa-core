package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
)

func TestAnalyzeIndexFallbackContextualizesFallback(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "index_fallback_contextual.llcontext", `def read(xs: darray[usize]) -> usize:
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

func TestAnalyzeIndexFallbackRejectsTypeMismatch(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "index_fallback_type_mismatch.llcontext", `def read(xs: darray[int], i: usize) -> int:
	return xs[i] else false
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "index fallback expects int, got bool") {
		t.Fatalf("expected index fallback type mismatch diagnostic, got:\n%s", all)
	}
}
