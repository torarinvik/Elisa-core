package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestReturnElementWalkPreservesMapsCyclesAndBoundaries(t *testing.T) {
	first := &ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}
	second := &ast.ReturnStmt{Value: &ast.IntLit{Value: "2"}}
	hidden := &ast.ReturnStmt{Value: &ast.IntLit{Value: "3"}}
	type futureShape struct {
		Position lexer.Pos
		Name     string
		Body     []ast.Stmt
		Returns  map[string]ast.Stmt
		Next     *futureShape
		Alias    *ast.ReturnStmt
		Function *ast.FuncDecl
		private  map[string]ast.Stmt
	}
	root := &futureShape{
		Position: lexer.Pos{File: "walk.elisa"}, Name: "metadata",
		Body: []ast.Stmt{first, &ast.ExprStmt{Expr: &ast.LambdaExpr{Body: []ast.Stmt{hidden}}}},
		// One map entry gives deterministic order while exercising map values.
		Returns: map[string]ast.Stmt{"only": second}, Alias: first,
		Function: &ast.FuncDecl{Body: []ast.Stmt{hidden}},
		private:  map[string]ast.Stmt{"hidden": hidden},
	}
	root.Next = root
	var got []*ast.ReturnStmt
	returnElementWalk(reflect.ValueOf(root), func(stmt *ast.ReturnStmt) {
		got = append(got, stmt)
	}, map[uintptr]bool{})
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("return callbacks changed order, duplicated aliases, or crossed boundaries: %v", got)
	}
}
