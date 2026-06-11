package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeIndexFallbackContextualizesFallback(t *testing.T) {
	// `get xs[0] else 0`: the `else 0` is consumed at the postfix `[0]` level, so
	// the AST is GetExpr{Value: IndexExpr{xs, 0, Fallback: 0}, Fallback: nil}.
	result := analyzeFunctionAnalysisTestSource(t, "index_fallback_contextual.elisa", `def read(xs: darray[usize]) -> usize:
	return get xs[0] else 0
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
	getExpr, ok := ret.Value.(*ast.GetExpr)
	if !ok {
		t.Fatalf("expected get expr, got %T", ret.Value)
	}
	indexExpr, ok := getExpr.Value.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected index expr inside get, got %T", getExpr.Value)
	}
	if got := result.ExprTypes[indexExpr.Index].String(); got != "usize" {
		t.Fatalf("expected index to contextualize to usize, got %s", got)
	}
	if got := result.ExprTypes[indexExpr.Fallback].String(); got != "usize" {
		t.Fatalf("expected fallback to contextualize to usize, got %s", got)
	}
}

func TestAnalyzeIndexFallbackContextualizesStringReferenceFallback(t *testing.T) {
	// Same postfix-level consumption: AST is GetExpr{IndexExpr{xs, i, Fallback: ""}}.
	result := analyzeFunctionAnalysisTestSource(t, "index_fallback_string_ref.elisa", `def read(xs: darray[u8&], i: usize) -> u8&:
	return get xs[i] else ""
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
	getExpr, ok := ret.Value.(*ast.GetExpr)
	if !ok {
		t.Fatalf("expected get expr, got %T", ret.Value)
	}
	indexExpr, ok := getExpr.Value.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected index expr inside get, got %T", getExpr.Value)
	}
	if got := result.ExprTypes[indexExpr].String(); got != "u8&" {
		t.Fatalf("expected index result to be u8&, got %s", got)
	}
	if got := result.ExprTypes[indexExpr.Fallback].String(); got != "u8&" {
		t.Fatalf("expected fallback to contextualize to u8&, got %s", got)
	}
}

func TestAnalyzeIndexFallbackRejectsImplicitElse(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "index_fallback_implicit_else.elisa", `def read(xs: darray[int], i: usize) -> int:
	return xs[i] else 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "implicit `else` index fallback has been removed") {
		t.Fatalf("expected hard-rejection diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeGetIndexFallbackRejectsTypeMismatch(t *testing.T) {
	// Simple-value fallbacks are consumed at the postfix `[i]` level (get xs[i] else v
	// = GetExpr{IndexExpr{Fallback: v}}) so the type-mismatch fires from analyzeIndexExpr.
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "index_fallback_type_mismatch.elisa", `def read(xs: darray[int], i: usize) -> int:
	return get xs[i] else false
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "index fallback expects int, got bool") {
		t.Fatalf("expected index fallback type mismatch diagnostic, got:\n%s", all)
	}
}
