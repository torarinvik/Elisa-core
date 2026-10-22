package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestRestoreSMTFactFrameKeepsBranchSurvivorsAcrossRestore(t *testing.T) {
	entryFact := smtFact{Deps: map[string]bool{"entry": true}}
	firstBranchFact := smtFact{Deps: map[string]bool{"first": true}}
	secondBranchFact := smtFact{Deps: map[string]bool{"second": true}}
	scope := &Scope{smtAssertFacts: []smtFact{entryFact}}
	a := &Analyzer{currentScope: scope}
	entry := a.snapshotSMTFactFrame()

	firstFacts := []smtFact{entryFact, firstBranchFact}
	scope.smtAssertFacts = firstFacts
	first := a.restoreSMTFactFrame(entry)
	if len(first.facts[0]) != 2 || &first.facts[0][0] != &firstFacts[0] {
		t.Fatal("restore did not retain the branch survivor slice")
	}

	// A sibling may extend its entry facts without overwriting the earlier
	// branch's captured survivors.
	scope.smtAssertFacts = append(scope.smtAssertFacts, secondBranchFact)
	second := a.restoreSMTFactFrame(entry)
	if len(first.facts[0]) != 2 || !first.facts[0][1].Deps["first"] {
		t.Fatal("restoring the next branch changed the previous branch's survivors")
	}
	if len(second.facts[0]) != 2 || !second.facts[0][1].Deps["second"] {
		t.Fatal("second branch survivor was not captured")
	}
	if len(scope.smtAssertFacts) != 1 || !scope.smtAssertFacts[0].Deps["entry"] {
		t.Fatal("scope was not reset to the entry fact frame")
	}
}

func BenchmarkSMTIfJoinBranchFacts(b *testing.B) {
	scope := &Scope{}
	a := &Analyzer{currentScope: scope}
	facts := func(n int) []smtFact {
		out := make([]smtFact, n)
		for i := range out {
			out[i].Expr = &ast.Ident{Name: "fact"}
		}
		return out
	}
	common := facts(64)
	entry := smtFactFrame{scopes: []*Scope{scope}, facts: [][]smtFact{common}}
	survivors := []smtFactFrame{
		{scopes: entry.scopes, facts: [][]smtFact{append(append([]smtFact(nil), common...), facts(16)...)}},
		{scopes: entry.scopes, facts: [][]smtFact{append(append([]smtFact(nil), common...), facts(16)...)}},
	}
	branches := [][]smtFact{facts(4), facts(4)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.joinIfSMTFacts(entry, survivors, branches, true)
		if len(scope.smtAssertFacts) != 65 {
			b.Fatal("lost common or joined facts")
		}
	}
}

func TestSMTIfJoinKeepsCommonAndBranchFactInputsIsolated(t *testing.T) {
	fact := func(name string) smtFact {
		return smtFact{Expr: &ast.Ident{Name: name}, Deps: map[string]bool{name: true}}
	}
	scope := &Scope{}
	a := &Analyzer{currentScope: scope}
	common := fact("common")
	left, right := fact("left"), fact("right")
	leftEnd, rightEnd := fact("left_end"), fact("right_end")
	entry := smtFactFrame{scopes: []*Scope{scope}, facts: [][]smtFact{{common}}}
	survivors := []smtFactFrame{
		{scopes: entry.scopes, facts: [][]smtFact{{common, left}}},
		{scopes: entry.scopes, facts: [][]smtFact{{common, right}}},
	}
	branches := [][]smtFact{{leftEnd}, {rightEnd}}
	a.joinIfSMTFacts(entry, survivors, branches, true)
	if len(scope.smtAssertFacts) != 2 || scope.smtAssertFacts[0].Expr != common.Expr {
		t.Fatal("common fact was lost")
	}
	joined := scope.smtAssertFacts[1]
	for _, name := range []string{"left", "right", "left_end", "right_end"} {
		if !joined.Deps[name] {
			t.Fatalf("joined fact lost %s", name)
		}
	}
	scope.smtAssertFacts[0] = smtFact{}
	if entry.facts[0][0].Expr != common.Expr || survivors[0].facts[0][1].Expr != left.Expr || branches[0][0].Expr != leftEnd.Expr {
		t.Fatal("join output aliases an input fact slice")
	}
	if len(survivors[0].facts[0]) != 2 || len(survivors[1].facts[0]) != 2 || len(branches[0]) != 1 || len(branches[1]) != 1 {
		t.Fatal("join changed input fact lists")
	}
}

func BenchmarkSMTFactFrameRoundTrip(b *testing.B) {
	var scope *Scope
	for i := 0; i < 8; i++ {
		scope = &Scope{Parent: scope, smtAssertFacts: []smtFact{{Expr: &ast.Ident{Name: "fact"}}}}
	}
	a := &Analyzer{currentScope: scope}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entry := a.snapshotSMTFactFrame()
		survivors := a.restoreSMTFactFrame(entry)
		if len(survivors.scopes) != 8 || len(survivors.facts) != 8 {
			b.Fatal("lost scope facts")
		}
	}
}

func TestSMTFactFrameRestoresEveryScopeIndependently(t *testing.T) {
	parentFact := smtFact{Expr: &ast.Ident{Name: "parent"}, Deps: map[string]bool{"parent": true}}
	childFact := smtFact{Expr: &ast.Ident{Name: "child"}, Deps: map[string]bool{"child": true}}
	parent := &Scope{smtAssertFacts: []smtFact{parentFact}}
	child := &Scope{Parent: parent, smtAssertFacts: []smtFact{childFact}}
	a := &Analyzer{currentScope: child}
	entry := a.snapshotSMTFactFrame()
	// Invalidate through production operations: published frame arrays are
	// immutable, so direct writes to their elements are no longer permitted.
	a.invalidateSMTAssertFactsForTarget(&ast.Ident{Name: "child"})
	a.invalidateSMTAssertFactsForTarget(&ast.Ident{Name: "parent"})
	survivors := a.restoreSMTFactFrame(entry)
	if len(survivors.facts[0]) != 0 || len(survivors.facts[1]) != 0 {
		t.Fatal("lost branch invalidation")
	}
	if child.smtAssertFacts[0].Expr != childFact.Expr || parent.smtAssertFacts[0].Expr != parentFact.Expr {
		t.Fatal("failed to restore every scope")
	}
	a.invalidateSMTAssertFactsForTarget(&ast.Ident{Name: "child"})
	a.invalidateSMTAssertFactsForTarget(&ast.Ident{Name: "parent"})
	if entry.facts[0][0].Expr != childFact.Expr || entry.facts[1][0].Expr != parentFact.Expr {
		t.Fatal("invalidation changed entry snapshot")
	}
}
