package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestBodyScheduleWaitsForDefaultCallSummary(t *testing.T) {
	producer := &ast.FuncDecl{Name: "make_callback"}
	consume := &ast.FuncDecl{
		Name: "consume",
		Params: []ast.ParamDecl{{
			Name:         "callback",
			DefaultValue: &ast.CallExpr{Func: &ast.Ident{Name: "make_callback"}},
		}},
	}
	caller := &ast.FuncDecl{
		Name: "caller",
		Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "consume"}}}},
	}
	schedule := planBodySchedule([]scopedDecl{{Decl: producer}, {Decl: consume}, {Decl: caller}}, 2)
	producerUnit, consumeUnit, callerUnit := schedule.unitOf[0], schedule.unitOf[1], schedule.unitOf[2]
	if schedule.pending[consumeUnit] != 1 || schedule.pending[callerUnit] != 1 {
		t.Fatalf("pending dependencies: consume=%d caller=%d, want one callee each", schedule.pending[consumeUnit], schedule.pending[callerUnit])
	}
	for _, want := range []int{producerUnit, consumeUnit, callerUnit} {
		if got := schedule.claim(0); got != want {
			t.Fatalf("claimed unit %d, want callee-first unit %d", got, want)
		}
		schedule.finish(want)
	}
}
