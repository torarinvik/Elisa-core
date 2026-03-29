package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/parser"
)

func parseAndAnalyzeOptimizationFactsTest(t *testing.T, filename string, src string) (*ast.File, *Result) {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := Analyze(file)
	return file, result
}

func testFuncDeclByName(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("expected function %q to be present", name)
	return nil
}

func mustVarDeclValueExpr(t *testing.T, stmt ast.Stmt, name string) ast.Expr {
	t.Helper()
	decl, ok := stmt.(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected %q declaration to be a var decl, got %T", name, stmt)
	}
	if decl.Name != name {
		t.Fatalf("expected var decl %q, got %q", name, decl.Name)
	}
	if decl.Value == nil {
		t.Fatalf("expected var decl %q to have a value", name)
	}
	return decl.Value
}

func mustExprStmtCall(t *testing.T, stmt ast.Stmt, name string) *ast.CallExpr {
	t.Helper()
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expression statement for %q, got %T", name, stmt)
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expression for %q, got %T", name, exprStmt.Expr)
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident.Name != name {
		t.Fatalf("expected call to %q, got %#v", name, call.Func)
	}
	return call
}

func TestOptimizationFactsMarkConstantChunksExactItemsDisjoint(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "chunks_exact_disjoint.llcontext", `
def kernel(buf: dview[i32]) -> void:
	ro: dview[i32] = readonly(buf)
	chunks: ChunksExactView[i32] = chunks_exact(ro, 4u)
	first: dview[i32] = chunks[0u]
	second: dview[i32] = chunks[1u]
	third: dview[i32] = chunks[2u]
	pass
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	if len(fn.Body) < 5 {
		t.Fatalf("expected kernel body to contain chunk declarations, got %d statements", len(fn.Body))
	}
	firstExpr := mustVarDeclValueExpr(t, fn.Body[2], "first")
	secondExpr := mustVarDeclValueExpr(t, fn.Body[3], "second")
	thirdExpr := mustVarDeclValueExpr(t, fn.Body[4], "third")

	if !result.ExprsHaveEqualExtentSize(firstExpr, secondExpr) {
		t.Fatalf("expected first and second chunk items to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(firstExpr, thirdExpr) {
		t.Fatalf("expected first and third chunk items to have equal extent size")
	}
	if !result.ExprsAreDisjoint(firstExpr, secondExpr) {
		t.Fatalf("expected first and second chunk items to be provably disjoint")
	}
	if !result.ExprsAreDisjoint(firstExpr, thirdExpr) {
		t.Fatalf("expected first and third chunk items to be provably disjoint")
	}

	firstFacts, ok := result.ExprOptimizationFacts(firstExpr)
	if !ok || firstFacts.Extent == nil {
		t.Fatalf("expected first chunk item to carry optimization facts")
	}
	if firstFacts.Extent.Begin != "0" || firstFacts.Extent.End != "4" {
		t.Fatalf("expected first chunk extent 0:4, got %s:%s", firstFacts.Extent.Begin, firstFacts.Extent.End)
	}
	thirdFacts, ok := result.ExprOptimizationFacts(thirdExpr)
	if !ok || thirdFacts.Extent == nil {
		t.Fatalf("expected third chunk item to carry optimization facts")
	}
	if thirdFacts.Extent.Begin != "8" || thirdFacts.Extent.End != "12" {
		t.Fatalf("expected third chunk extent 8:12, got %s:%s", thirdFacts.Extent.Begin, thirdFacts.Extent.End)
	}
}

func TestAnalyzeZipMapAcceptsDisjointChunksExactItemsFromSharedBuffer(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "zip_map_chunks_exact_disjoint.llcontext", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: dview[i32]) -> void:
	ro: dview[i32] = readonly(buf)
	ro_chunks: ChunksExactView[i32] = chunks_exact(ro, 4u)
	rw_chunks: ChunksExactView[i32] = chunks_exact(buf, 4u)
	zip_map(rw_chunks[0u], ro_chunks[1u], ro_chunks[2u], add)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected zip_map over disjoint chunk items to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	call := mustExprStmtCall(t, fn.Body[len(fn.Body)-1], "zip_map")
	if len(call.Args) != 4 {
		t.Fatalf("expected zip_map to have 4 arguments, got %d", len(call.Args))
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[1]) {
		t.Fatalf("expected zip_map destination and source 1 chunk items to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[2]) {
		t.Fatalf("expected zip_map destination and source 2 chunk items to have equal extent size")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[1]) {
		t.Fatalf("expected zip_map destination and source 1 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[2]) {
		t.Fatalf("expected zip_map destination and source 2 chunk items to be disjoint")
	}
}