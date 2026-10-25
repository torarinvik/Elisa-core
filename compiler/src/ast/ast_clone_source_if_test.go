package ast

import "testing"

// Source-written branches and compiler-synthesized guards have different flow
// analysis meaning. Plain and substituting clones must preserve that distinction
// while owning the expression/branch nodes they may subsequently rewrite.
func TestCloneIfStatementPreservesSourceBranchAndIsolation(t *testing.T) {
	for _, fromSource := range []bool{false, true} {
		original := &IfStmt{
			FromSource: fromSource,
			Cond:       &Ident{Name: "condition"},
			Then:       []Stmt{&ReturnStmt{Value: &Ident{Name: "value"}}},
			Else:       []Stmt{&IfStmt{FromSource: true, Cond: &Ident{Name: "fallback"}, Then: []Stmt{&ReturnStmt{Value: &Ident{Name: "other"}}}}},
		}
		for _, substitute := range []bool{false, true} {
			var cloned *IfStmt
			if substitute {
				cloned = CloneStmtSubst(original, map[string]Expr{"condition": &Ident{Name: "replacement"}}).(*IfStmt)
			} else {
				block, ok := CloneBlock([]Stmt{original})
				if !ok {
					t.Fatal("ordinary source branch did not clone")
				}
				cloned = block[0].(*IfStmt)
			}
			if cloned.FromSource != fromSource || !cloned.Else[0].(*IfStmt).FromSource {
				t.Fatalf("source branch provenance lost: outer=%v nested=%v", cloned.FromSource, cloned.Else[0].(*IfStmt).FromSource)
			}
			wantCondition := "condition"
			if substitute {
				wantCondition = "replacement"
			}
			if cloned.Cond.(*Ident).Name != wantCondition {
				t.Fatal("clone did not preserve requested substitution semantics")
			}
			cloned.Cond.(*Ident).Name = "changed"
			cloned.Then[0].(*ReturnStmt).Value.(*Ident).Name = "changed"
			cloned.Else[0].(*IfStmt).Cond.(*Ident).Name = "changed"
			if original.Cond.(*Ident).Name != "condition" || original.Then[0].(*ReturnStmt).Value.(*Ident).Name != "value" || original.Else[0].(*IfStmt).Cond.(*Ident).Name != "fallback" {
				t.Fatal("cloned source branch aliases the original syntax")
			}
		}
	}
}
