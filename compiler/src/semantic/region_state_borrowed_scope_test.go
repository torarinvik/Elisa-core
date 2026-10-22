package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestBorrowedRegionStateMutationsKeepCallerIsolated(t *testing.T) {
	child := &Symbol{Name: "child", Kind: SymbolRegion}
	parent := &Symbol{Name: "parent", Kind: SymbolRegion}
	added := &Symbol{Name: "added", Kind: SymbolLocal}
	initial := regionState{Generation: 2, Backing: normalizeBacking("")}
	cases := []struct {
		name    string
		mutate  func(*Analyzer)
		changed func(*Analyzer) bool
	}{
		{"register", func(a *Analyzer) { a.registerOwnedStoreOwner(added) }, func(a *Analyzer) bool { _, ok := a.currentRegions[added]; return ok }},
		{"allocate", func(a *Analyzer) { a.markRegionAllocatedByOwnerExpr(&ast.Ident{Name: "child"}) }, func(a *Analyzer) bool { return a.currentRegions[child].Allocated }},
		{"transfer", func(a *Analyzer) { a.transferRegionOwnerOut(child, "test") }, func(a *Analyzer) bool { return a.currentRegions[child].Destroyed }},
		{"destroy", func(a *Analyzer) { a.analyzeStmt(&ast.DestroyStmt{Name: "child"}) }, func(a *Analyzer) bool { return a.currentRegions[child].Destroyed }},
		{"reset", func(a *Analyzer) { a.analyzeResetStmt(&ast.ResetStmt{Name: "child"}) }, func(a *Analyzer) bool { return a.currentRegions[child].Generation == 0 }},
		{"adopt", func(a *Analyzer) { a.analyzeAdoptStmt(&ast.AdoptStmt{Child: "child", Parent: "parent"}) }, func(a *Analyzer) bool { return a.currentRegions[child].Destroyed }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			scope := NewScope(nil)
			scope.Define(child)
			scope.Define(parent)
			caller := map[*Symbol]regionState{child: initial, parent: initial}
			a := &Analyzer{currentScope: scope, currentRegions: caller}
			a.borrowRegionStates()
			test.mutate(a)
			if !test.changed(a) {
				t.Fatalf("mutation did not take effect: %+v", a.diagnostics)
			}
			if len(caller) != 2 || caller[child] != initial || caller[parent] != initial {
				t.Fatal("mutation escaped into caller map")
			}
			if a.currentRegionsBorrowed {
				t.Fatal("first write did not establish ownership")
			}
		})
	}
}

func TestRegionStateBlockMutationAndBorrowedOwnershipRestore(t *testing.T) {
	sym := &Symbol{Name: "owner", Kind: SymbolRegion}
	scope := NewScope(nil)
	scope.Define(sym)
	caller := map[*Symbol]regionState{sym: {Generation: 1}}
	a := &Analyzer{currentScope: scope, currentRegions: caller}
	a.borrowRegionStates()
	stmts := []ast.Stmt{&ast.DestroyStmt{Name: "owner"}}
	a.analyzeBlockWithRegionClone(stmts, scope)
	if caller[sym].Destroyed || !a.currentRegionsBorrowed {
		t.Fatal("discarded block changed caller or lost borrowed ownership")
	}
	captured := a.analyzeBlockWithRegionCloneCapture(stmts, scope)
	if !captured[sym].Destroyed || caller[sym].Destroyed || !a.currentRegionsBorrowed {
		t.Fatal("captured branch lost its own state or changed caller ownership")
	}
	a.setCurrentRegionState(sym, regionState{Generation: 3})
	if caller[sym].Generation != 1 {
		t.Fatal("write after nested block changed borrowed caller map")
	}
}

func TestLambdaRestoresBorrowedRegionStateScope(t *testing.T) {
	const source = "def base() -> i64:\n    return 0\n"
	_, r := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if r == nil {
		t.Fatal("analysis not retained")
	}
	sym := &Symbol{Name: "owner", Kind: SymbolRegion}
	caller := map[*Symbol]regionState{sym: {Generation: 1}}
	a := r.a
	a.currentRegions = caller
	a.borrowRegionStates()
	result := a.analyzeLambdaExpr(&ast.LambdaExpr{BodyExpr: &ast.IntLit{Value: "1"}}, nil)
	if IsInvalidType(result) {
		t.Fatalf("lambda failed: %v", a.diagnostics)
	}
	if !a.currentRegionsBorrowed {
		t.Fatal("lambda lost enclosing borrowed ownership")
	}
	a.setCurrentRegionState(sym, regionState{Generation: 3})
	if caller[sym].Generation != 1 {
		t.Fatal("write after lambda changed borrowed caller map")
	}
}
