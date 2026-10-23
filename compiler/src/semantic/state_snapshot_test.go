package semantic

import "testing"

func TestSemanticStateRoundTripAndEdit(t *testing.T) {
	before, r := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	data, err := r.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("state bytes: %d", len(data))
	restored, err := RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	if retainedDiagnostics(restored.result) != retainedDiagnostics(before) {
		t.Fatal("state changed diagnostics")
	}
	for _, source := range []string{retainedAfter, retainedBefore} {
		got, err := restored.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		want := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("restored update differs:\n%s\nwant:\n%s", retainedDiagnostics(got), retainedDiagnostics(want))
		}
	}
}

func TestSemanticStatePreservesOptimizationIdentityDomain(t *testing.T) {
	_, r := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	symbol := &Symbol{Name: "fresh"}
	old := r.a.optimizationSymbolIdentity(symbol)
	data, err := r.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	next := restored.a.optimizationSymbolIdentity(&Symbol{Name: "fresh"})
	if old == next {
		t.Fatal("restored identities collided with new symbols")
	}
}

func TestSemanticStateRejectsOpenBranchJournal(t *testing.T) {
	_, r := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	aliases := map[string]returnBorrowFlow{}
	r.a.beginReturnBorrowBranch(aliases)
	if _, err := r.MarshalState(); err == nil {
		t.Fatal("persisted an open branch journal")
	}
	r.a.endReturnBorrowBranch(aliases)
	for _, frame := range r.a.returnBorrowAliasFrames[:cap(r.a.returnBorrowAliasFrames)] {
		if frame.aliases != 0 {
			t.Fatal("closed frame retained process address")
		}
	}
	if _, err := r.MarshalState(); err != nil {
		t.Fatal(err)
	}
}
