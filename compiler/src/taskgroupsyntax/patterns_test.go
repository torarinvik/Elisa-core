package taskgroupsyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestNestedPatternShadowsAPI(t *testing.T) {
	pattern := &ast.MatchOrPattern{Options: []ast.MatchPattern{&ast.MatchWildcardPattern{}, &ast.MatchTuplePattern{Elems: []ast.MatchPattern{&ast.MatchBindPattern{Binder: "task_group_wait_all"}}}}}
	if !MatchPatternShadowsAPI(pattern) {
		t.Fatal("nested binder must shadow the runtime API")
	}
	if MatchPatternShadowsAPI(&ast.MatchBindPattern{Name: "ordinary"}) {
		t.Fatal("ordinary binder does not shadow the runtime API")
	}
}

func TestWalkStmtListsVisitsNestedBranches(t *testing.T) {
	leaf := &ast.PassStmt{}
	root := []ast.Stmt{&ast.IfStmt{Elifs: []ast.ElifClause{{Body: []ast.Stmt{&ast.MatchStmt{Arms: []ast.MatchArm{{Body: []ast.Stmt{leaf}}}}}}}}}
	var seen int
	WalkStmtLists(root, func(body []ast.Stmt) {
		if len(body) == 1 {
			seen++
		}
	})
	if seen != 3 {
		t.Fatalf("visited %d non-empty statement lists, want 3", seen)
	}
}
