package semantic

import "testing"

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

	// A sibling starts with a fresh copy of the entry facts and may extend it without
	// overwriting the earlier branch's captured survivors.
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
