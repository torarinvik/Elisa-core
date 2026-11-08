package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestBodyScheduleReverseEdgesMatchDependencies(t *testing.T) {
	call := func(name string) ast.Stmt { return &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: name}}} }
	bodies := []scopedDecl{
		{Decl: &ast.FuncDecl{Name: "a", Body: []ast.Stmt{call("b"), call("c")}}},
		{Decl: &ast.FuncDecl{Name: "b", Body: []ast.Stmt{call("c")}}},
		{Decl: &ast.FuncDecl{Name: "c"}},
		{Decl: &ast.FuncDecl{Name: "d"}},
	}
	s := planBodySchedule(bodies, 2)
	want := make([][]int, len(s.units))
	for u, deps := range s.deps {
		for _, d := range deps {
			want[d] = append(want[d], u)
		}
	}
	if !reflect.DeepEqual(s.rdeps, want) {
		t.Fatalf("reverse dependencies changed: got %v, want %v", s.rdeps, want)
	}
}

func BenchmarkBodyScheduleFanout(b *testing.B) {
	const count = 128
	bodies := make([]scopedDecl, count)
	for i := range bodies {
		fn := &ast.FuncDecl{Name: fmt.Sprintf("f%d", i)}
		for j := 1; j <= 16 && j <= i; j++ {
			fn.Body = append(fn.Body, &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: fmt.Sprintf("f%d", i-j)}}})
		}
		bodies[i] = scopedDecl{Decl: fn}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if s := planBodySchedule(bodies, 4); len(s.units) != count {
			b.Fatal("lost bodies")
		}
	}
}
