package astscan

import (
	"elisacore/src/ast"
	"testing"
)

func TestAllocationCensusNestedBodies(t *testing.T) {
	if got := CountForAllocation(nil); got != (AllocationCensus{}) {
		t.Fatalf("nil file: %+v", got)
	}
	file := &ast.File{Decls: []ast.Decl{
		&ast.NamespaceDecl{Decls: []ast.Decl{
			&ast.FuncDecl{Body: []ast.Stmt{
				&ast.ParallelForStmt{Source: &ast.Ident{Name: "items"}, Body: []ast.Stmt{
					&ast.ReturnStmt{Value: &ast.BinaryExpr{Left: &ast.Ident{Name: "x"}, Right: &ast.IntLit{}}},
				}},
			}},
		}},
		&ast.FuncDecl{},
	}}
	want := AllocationCensus{Exprs: 4, FuncDecls: 2, ParallelFors: 1}
	if got := CountForAllocation(file); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
