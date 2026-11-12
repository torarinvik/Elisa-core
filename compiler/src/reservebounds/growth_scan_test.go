package reservebounds

import (
	"elisacore/src/ast"
	"testing"
)

func TestGrowthTargetsExcludeUnboundedNestedLoops(t *testing.T) {
	push := func(name string) ast.Stmt {
		return &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: name}, Field: "push"}, Args: []ast.Expr{&ast.IntLit{Value: "1"}}}}
	}
	body := []ast.Stmt{push("bounded"), push("unbounded"), &ast.WhileStmt{Body: []ast.Stmt{push("unbounded")}}}
	counts, names := CollectGrowthTargetCounts(body)
	if len(names) != 1 || names[0] != "bounded" || counts["bounded"] == nil || counts["unbounded"] != nil {
		t.Fatalf("counts=%v names=%v", counts, names)
	}
	all := CollectGrowthTargetNames(body)
	if !all["bounded"] || !all["unbounded"] || len(all) != 2 {
		t.Fatalf("targets=%v", all)
	}
}
