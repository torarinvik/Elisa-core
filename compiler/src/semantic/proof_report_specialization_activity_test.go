//go:build cgo

package semantic

import (
	"reflect"
	"strings"
	"testing"
)

// The actual public API must refuse before memo lookup, not merely place an
// unread Unknown flag inside a discarded construction analyzer.
func TestLiveProofReportSpecializationRejectsActiveCanonicalContextBeforeMemo(t *testing.T) {
	root, fn := liveProofSpecializationFixture(t)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	args := []Type{root.NamedTypes["i64"]}
	ready, err := root.SpecializeFunction(fn, args)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Analyzer){
		"owner":           func(a *Analyzer) { a.proofLifecycle.Current = &proofContribution{} },
		"probe":           func(a *Analyzer) { a.proofLifecycle.ProbeDepth = 1 },
		"synthetic":       func(a *Analyzer) { a.proofLifecycle.Synthetic = fn },
		"synthetic_owner": func(a *Analyzer) { a.proofLifecycle.SyntheticOwner = fn },
		"category":        func(a *Analyzer) { a.currentProofCategory = ProofCatReturnRefinement },
	}
	for name, activate := range cases {
		t.Run(name, func(t *testing.T) {
			previous, category := root.analyzer.proofLifecycle, root.analyzer.currentProofCategory
			defer func() { root.analyzer.proofLifecycle = previous; root.analyzer.currentProofCategory = category }()
			count := len(root.shared.bodies)
			activate(root.analyzer)
			product, err := root.SpecializeFunction(fn, args)
			if product != nil || err == nil || !strings.Contains(err.Error(), "active proof-report context") {
				t.Fatalf("active API/memo admission succeeded: product=%v error=%v", product, err)
			}
			if len(root.shared.bodies) != count {
				t.Fatal("active context changed successful memo")
			}
		})
	}
	again, err := root.SpecializeFunction(fn, args)
	if err != nil || again != ready {
		t.Fatal("restored idle API/memo changed", err)
	}
}

func TestLiveProofReportCoverageFlagDoesNotGrantOrRemoveCodegenAuthority(t *testing.T) {
	root, fn := liveProofSpecializationFixture(t)
	before := append([]ProofFact(nil), root.ProofReport...)
	summary := root.ElisionSummary
	root.analyzer.proofLifecycle.Unknown = "source reporter coverage is unavailable"
	// This coverage marker denies persistence; it cannot manufacture proof facts
	// or suppress concrete diagnostics. A checked fresh program can still lower.
	if root.analyzer.proofReportCheckpointError() == "" {
		t.Fatal("coverage marker allowed persistence")
	}
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(product.Result.ProofReport, before) || !reflect.DeepEqual(product.Result.ElisionSummary, summary) {
		t.Fatal("coverage marker changed full published report/summary")
	}
	if product.Result.analyzer != nil {
		t.Fatal("unknown private construction analyzer escaped publication")
	}
}
