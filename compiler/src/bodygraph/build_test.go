package bodygraph

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/astscan"
	"elisacore/src/lexer"
)

func TestBuildSeparatesDefaultReferencesFromRecursion(t *testing.T) {
	callee := &ast.FuncDecl{Name: "callee"}
	caller := &ast.FuncDecl{Name: "caller", Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "caller"}}}, Params: []ast.ParamDecl{{DefaultValue: &ast.Ident{Name: "callee"}}}}
	walk := func(stmts []ast.Stmt, visit func(any)) {
		for _, stmt := range stmts {
			visit(stmt.(*ast.ExprStmt).Expr)
		}
	}
	g := Build([]ast.Decl{callee, caller}, walk)
	if !reflect.DeepEqual(g.Deps, [][]int{nil, {0}}) {
		t.Fatalf("body dependencies: %v", g.Deps)
	}
	if len(g.FunctionDeps[1]) != 0 || !g.FuncRecursive[1] {
		t.Fatalf("default call affected recursive function graph: %v %v", g.FunctionDeps, g.FuncRecursive)
	}
	if !reflect.DeepEqual(g.Weight, []int{0, 2}) {
		t.Fatalf("weights: %v", g.Weight)
	}
}

func TestBuildTracksCallsAndOperatorScheduling(t *testing.T) {
	method := &ast.FuncDecl{Name: "Numbers::__add__"}
	combine := &ast.FuncDecl{Name: "combine"}
	callee := &ast.FuncDecl{Name: "callee"}
	caller := &ast.FuncDecl{Name: "caller", Body: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "callee"}}},
		&ast.ExprStmt{Expr: &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: &ast.IntLit{Value: "1"}, Right: &ast.IntLit{Value: "2"}}},
	}}
	walk := func(stmts []ast.Stmt, visit func(any)) { astscan.WalkStatements(stmts, visit, map[uintptr]bool{}) }
	g := Build([]ast.Decl{method, combine, callee, caller}, walk)
	if !reflect.DeepEqual(g.Deps[3], []int{0, 1, 2}) {
		t.Fatalf("body dependencies: %v", g.Deps[3])
	}
	if !reflect.DeepEqual(g.FunctionDeps[3], []int{2}) {
		t.Fatalf("function dependencies: %v", g.FunctionDeps[3])
	}
	if g.FuncRecursive[3] {
		t.Fatal("operator edges must not imply recursion")
	}
}
