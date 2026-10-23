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

func TestSemanticStateClearsClosedStorageViewLoopCallbacks(t *testing.T) {
	_, r := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	a := r.a
	a.pushStorageViewLoopUseFrame(a.globalScope, nil)
	a.storageViewLoopUseFrames[0].mutations = []storageViewLoopMutation{{spare: func(source, candidate string) bool { return source == candidate }}}
	if _, err := r.MarshalState(); err == nil {
		t.Fatal("saved active loop frame")
	}
	frame := a.popStorageViewLoopUseFrame()
	if len(frame.mutations) != 1 || !frame.mutations[0].spare("source", "source") {
		t.Fatal("popping frame discarded its live back-edge callback")
	}
	for _, closed := range a.storageViewLoopUseFrames[:cap(a.storageViewLoopUseFrames)] {
		if closed.outer != nil || closed.body != nil || closed.firstUse != nil || closed.holders != nil || closed.mutations != nil {
			t.Fatal("closed stack slot retained loop state")
		}
	}
	data, err := r.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreAnalysisState(data); err != nil {
		t.Fatal(err)
	}
}

func TestSemanticSnapshotKeepsOnlyPreBodyComparisonState(t *testing.T) {
	_, r := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	if len(r.pre.prepared) == 0 {
		t.Fatal("fixture has no preparation tables")
	}
	// Emulate an older valid snapshot whose idle baseline still owned scratch
	// preparation tables. Loading it must keep its comparisons, not its AST roots.
	data, err := semanticStateRegistry().Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	check := func(r *RetainedAnalysis) {
		t.Helper()
		p := r.pre
		if p == nil || len(p.fingerprint) == 0 || p.global == "" {
			t.Fatal("declaration baseline was discarded")
		}
		if p.keyOf != nil || p.prepared != nil || p.targets != nil || p.regionIndex != nil {
			t.Fatal("idle state still roots scratch preparation data")
		}
	}
	check(restored)
	added := retainedAfter + "\ndef fresh(x: i64) -> i64:\n    return helper(x)\n"
	for _, source := range []string{retainedAfter, added, retainedBefore} {
		file, scratch := retainedParse(t, source), retainedParse(t, source)
		prepared := PrepareRetainedScratch(scratch, AnalyzeOptions{})
		if prepared == nil || len(prepared.pre.prepared) == 0 {
			t.Fatal("scratch preparation unavailable")
		}
		original := prepared.pre
		count := len(original.prepared)
		got, err := restored.UpdatePrepared(file, scratch, []byte(source), prepared)
		if err != nil {
			t.Fatal(err)
		}
		if prepared.pre != original || len(original.prepared) != count {
			t.Fatal("compaction changed caller-owned scratch preparation")
		}
		check(restored)
		want := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatal("compacted update diagnostics differ from cold analysis")
		}
		data, err = restored.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		restored, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		check(restored)
	}
	// Direct serialization compacts a newly analyzed baseline too.
	if _, err = r.MarshalState(); err != nil {
		t.Fatal(err)
	}
	check(r)
}
