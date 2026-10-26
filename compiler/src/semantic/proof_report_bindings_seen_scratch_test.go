//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestProofBindingSeenScratchDistinctRootsMatchesReference(t *testing.T) {
	const base = "def first() -> i64:\n    return 0\n\ndef moved() -> i64:\n    in auto:\n        return 0\n"
	// Each collector root is an independent parse, as in a retained update.
	// The two old roots also carry separate source blockers so the test checks
	// that their blocker maps are still merged after sharing the visit scratch.
	fresh := retainedParse(t, "\n"+base)
	live := retainedParse(t, base+"\ndef live_blocker() -> i64:\n    __auto_900001: i64 = 0\n    return __auto_900001\n")
	pristine := retainedParse(t, base+"\ndef pristine_blocker() -> i64:\n    __quantifier_900002: i64 = 0\n    return __quantifier_900002\n")
	if fresh == live || fresh == pristine || live == pristine {
		t.Fatal("fixture roots must be distinct parses")
	}
	oldRegion := proofBindingFunction(t, pristine, "moved").Body[0].(*ast.RegionStmt)
	current := proofBindingFunction(t, fresh, "moved")
	newRegion := current.Body[0].(*ast.RegionStmt)
	state := &RetainedAnalysis{
		a:        &Analyzer{file: live},
		pristine: pristine,
		src:      []byte(base),
	}
	marks := map[string][][2]int{oldRegion.Pos().File: {{1, 1}}}
	candidate := state.prepareProofReportBindings(fresh, marks)
	reference := state.prepareProofReportBindingsPositionReference(fresh, marks)
	row := proofContribution{Key: proofContributionKey{Owner: current, Phase: proofPhaseBody}, Origins: map[ast.Node]bool{current: true}}

	for _, text := range []string{
		"ordinary subject",
		oldRegion.Name + " == " + oldRegion.Name,
		fmt.Sprintf("literal == %q", oldRegion.Name),
		"__auto_999999 == 0",
		"__quantifier_999999 == 0",
		"__auto_900001",
		"__quantifier_900002",
		"__unknown_123",
	} {
		input := ProofFact{Subject: text, Predicate: text, KnownFacts: []string{text}, ClosedWorldFacts: []string{text}, Missing: text}
		got, gotErr := candidate(row, input)
		want, wantErr := reference(row, input)
		if gotErr != wantErr || !reflect.DeepEqual(got, want) {
			t.Fatalf("adapter differs for %q: got %#v / %q, want %#v / %q", text, got, gotErr, want, wantErr)
		}
	}
	for _, blocked := range []string{"__auto_900001", "__quantifier_900002"} {
		if _, why := candidate(row, ProofFact{Subject: blocked}); !strings.Contains(why, "source material") {
			t.Fatalf("blocker from its independent prior root was lost for %s: %q", blocked, why)
		}
	}
	wantSubject := newRegion.Name + " == " + newRegion.Name
	got, why := candidate(row, ProofFact{Subject: oldRegion.Name + " == " + oldRegion.Name})
	if why != "" || got.Subject != wantSubject {
		t.Fatalf("prior binder did not map to fresh producer: got %q (%s), want %q", got.Subject, why, wantSubject)
	}
}

func proofBindingSeenScratchBenchmarkSource(extraFunctions int) string {
	var source = "def first() -> i64:\n    return 0\n\ndef moved() -> i64:\n    in auto:\n        return 0\n"
	for i := 0; i < extraFunctions; i++ {
		source += fmt.Sprintf("\ndef other_%d() -> i64:\n    return %d\n", i, i)
	}
	return source
}

var proofBindingSeenScratchRendererSink func(proofContribution, ProofFact) (ProofFact, string)

func BenchmarkProofBindingSeenScratchWholeAdapterDistinctRoots(b *testing.B) {
	cases := []struct {
		name                   string
		fresh, live, pristine  int
		stripPristineBodyNodes bool
	}{
		{name: "balanced_512_each", fresh: 512, live: 512, pristine: 512},
		{name: "fresh_512_prior_64", fresh: 512, live: 64, pristine: 64},
		{name: "fresh_64_prior_512", fresh: 64, live: 512, pristine: 512},
		{name: "disk_restore_512_fresh_512_live_pristine_headers", fresh: 512, live: 512, pristine: 512, stripPristineBodyNodes: true},
	}
	for _, tc := range cases {
		freshSource := proofBindingSeenScratchBenchmarkSource(tc.fresh)
		liveSource := proofBindingSeenScratchBenchmarkSource(tc.live)
		pristineSource := proofBindingSeenScratchBenchmarkSource(tc.pristine)
		fresh, why := proofReportBindingSource("benchmark.elisa", []byte(freshSource))
		if why != "" {
			b.Fatal(why)
		}
		live, why := proofReportBindingSource("benchmark.elisa", []byte(liveSource))
		if why != "" {
			b.Fatal(why)
		}
		pristine, why := proofReportBindingSource("benchmark.elisa", []byte(pristineSource))
		if why != "" {
			b.Fatal(why)
		}
		if tc.stripPristineBodyNodes {
			for _, decl := range pristine.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					fn.Body = nil
				}
			}
		}
		state := &RetainedAnalysis{a: &Analyzer{file: live}, pristine: pristine, src: []byte(pristineSource)}
		for _, reference := range []bool{true, false} {
			name := "current_policy"
			if reference {
				name = "independent_maps_reference"
			}
			b.Run(tc.name+"/"+name, func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if reference {
						proofBindingSeenScratchRendererSink = state.prepareProofReportBindingsPositionReference(fresh, nil)
					} else {
						proofBindingSeenScratchRendererSink = state.prepareProofReportBindings(fresh, nil)
					}
				}
			})
		}
	}
}
