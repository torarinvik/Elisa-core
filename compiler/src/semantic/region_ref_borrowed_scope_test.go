package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestBorrowedRegionRefScopeKeepsCallerBindingsIsolated(t *testing.T) {
	value, added := &Symbol{Name: "value"}, &Symbol{Name: "added"}
	from, to := &Symbol{Name: "from"}, &Symbol{Name: "to"}
	tests := []struct {
		name   string
		mutate func(*Analyzer)
		check  func(*Analyzer) bool
	}{
		{"insert", func(a *Analyzer) { a.recordResolvedRegionRefBinding(added, regionRefStateFromParamDependency(1)) }, func(a *Analyzer) bool { _, ok := a.currentRegionRefs[added]; return ok }},
		{"replace", func(a *Analyzer) { a.recordResolvedRegionRefBinding(value, regionRefStateFromParamDependency(2)) }, func(a *Analyzer) bool { return a.currentRegionRefs[value].HasDirectParamDep }},
		{"delete", func(a *Analyzer) { a.recordResolvedRegionRefBinding(value, regionRefState{}) }, func(a *Analyzer) bool { _, ok := a.currentRegionRefs[value]; return !ok }},
		{"invalidate", func(a *Analyzer) { a.invalidateRegionRefs(from, nil, "branch") }, func(a *Analyzer) bool { return !a.currentRegionRefs[value].Deps[from].Valid }},
		{"rebase", func(a *Analyzer) { a.rebaseRegionRefs(from, to, 2) }, func(a *Analyzer) bool { _, ok := a.currentRegionRefs[value].Deps[to]; return ok }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caller := map[*Symbol]regionRefState{value: regionRefStateFromDependency(from, 1)}
			a := &Analyzer{currentRegionRefs: caller}
			a.borrowRegionRefStates()
			test.mutate(a)
			if !test.check(a) {
				t.Fatal("branch mutation did not take effect")
			}
			if len(caller) != 1 || caller[value].HasDirectParamDep || !caller[value].Deps[from].Valid {
				t.Fatal("branch mutation changed caller binding")
			}
			if _, ok := caller[value].Deps[to]; ok {
				t.Fatal("branch rebased caller dependency")
			}
		})
	}
}

func TestLambdaRestoresBorrowedRegionRefScope(t *testing.T) {
	const source = "def base() -> i64:\n    return 0\n"
	_, r := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if r == nil {
		t.Fatal("analysis not retained")
	}
	value, added := &Symbol{Name: "value"}, &Symbol{Name: "added"}
	caller := map[*Symbol]regionRefState{value: regionRefStateFromParamDependency(0)}
	a := r.a
	a.currentRegionRefs = caller
	a.borrowRegionRefStates()
	result := a.analyzeLambdaExpr(&ast.LambdaExpr{BodyExpr: &ast.IntLit{Value: "1"}}, nil)
	if IsInvalidType(result) {
		t.Fatalf("lambda failed: %v", a.diagnostics)
	}
	if !a.currentRegionRefsBorrowed {
		t.Fatal("nested lambda lost enclosing borrowed-map ownership")
	}
	a.recordResolvedRegionRefBinding(added, regionRefStateFromParamDependency(1))
	if len(caller) != 1 {
		t.Fatal("binding after lambda escaped into caller snapshot")
	}
}

func TestReadOnlyRegionRefBlockRestoresOwnership(t *testing.T) {
	scope := NewScope(nil)
	a := &Analyzer{currentScope: scope, currentRegionRefs: map[*Symbol]regionRefState{}}
	a.analyzeBlockWithRegionCloneCapture(nil, scope)
	if a.currentRegionRefsBorrowed {
		t.Fatal("return from scoped analysis left caller map borrowed")
	}
}
