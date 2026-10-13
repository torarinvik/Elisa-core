package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestTaskGroupWindowRejectsShadowBindings(t *testing.T) {
	shadowingStatements := []struct {
		name string
		stmt ast.Stmt
	}{
		{
			name: "tuple bind",
			stmt: &ast.TupleBindStmt{Names: []ast.TupleBindName{{Name: "group"}}, Declare: true},
		},
		{
			name: "move tuple destructure",
			stmt: &ast.MoveBindStmt{Pattern: &ast.MoveBindTuplePattern{Args: []ast.MoveBindArg{{Name: "group"}}}},
		},
		{
			name: "match binder",
			stmt: &ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: &ast.MatchBindPattern{Name: "group"}}}},
		},
		{
			name: "match as binder",
			stmt: &ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: &ast.MatchStructPattern{As: "group"}}}},
		},
		{
			name: "match struct nested binder",
			stmt: &ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: &ast.MatchStructPattern{Args: []ast.MatchPatternArg{{Name: "field", Pattern: &ast.MatchBindPattern{Name: "group"}}}}}}},
		},
		{
			name: "match variant nested binder",
			stmt: &ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: &ast.MatchVariantPattern{Args: []ast.MatchPatternArg{{Name: "field", Pattern: &ast.MatchBindPattern{Name: "group"}}}}}}},
		},
	}

	for _, tc := range shadowingStatements {
		t.Run(tc.name, func(t *testing.T) {
			window := &ast.CanStmt{Body: []ast.Stmt{tc.stmt}}
			var submits []taskGroupSubmitCandidate
			if taskGroupWindowStmtSafe(window, "group", &submits) {
				t.Fatalf("candidate window must be rejected when a binding shadows its TaskGroup name")
			}
		})
	}
}

func TestTaskGroupWindowIgnoresMatchFieldLabelsAsBinders(t *testing.T) {
	stmt := &ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: &ast.MatchStructPattern{
		Args: []ast.MatchPatternArg{{Name: "group", Pattern: &ast.MatchBindPattern{Name: "value"}}},
	}}}}
	var submits []taskGroupSubmitCandidate
	if !taskGroupWindowStmtSafe(stmt, "group", &submits) {
		t.Fatal("a field label matching the TaskGroup name must not count as a binding shadow")
	}
}

func TestTaskGroupFunctionRejectsEnclosingMatchAPIBinder(t *testing.T) {
	fn := &ast.FuncDecl{Body: []ast.Stmt{
		&ast.MatchStmt{Arms: []ast.MatchArm{{
			Pattern: &ast.MatchBindPattern{Name: "task_group_add"},
			Body:    []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "task_group_add"}}}},
		}}},
	}}
	if !taskGroupFunctionShadowsAPI(fn) {
		t.Fatal("a match-arm API binder must disable the TaskGroup proof for its enclosing function")
	}
}
