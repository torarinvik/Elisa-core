package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestProofBodyReanalysisClosureIsTransitiveAndKeepsUnknownDeclines(t *testing.T) {
	leaf, caller, outer := &ast.FuncDecl{Name: "leaf"}, &ast.FuncDecl{Name: "caller"}, &ast.FuncDecl{Name: "outer"}
	body := func(owner *ast.FuncDecl, origin ast.Node) proofContribution {
		key := proofContributionKey{Owner: owner, Phase: proofPhaseBody}
		return proofContribution{Key: key, Facts: []ProofFact{{Subject: "actual-obligation"}}, Origins: map[ast.Node]bool{owner: true, origin: true}}
	}
	base := map[proofContributionKey]proofContribution{}
	for _, row := range []proofContribution{body(caller, leaf), body(outer, caller)} {
		base[row.Key] = row
	}
	state := &RetainedAnalysis{a: &Analyzer{}}
	state.a.proofLifecycle.Segments = base
	forced, why := state.proofReportBodyReanalysisClosure([]*ast.FuncDecl{leaf}, nil)
	if why != "" || !forced[caller] || !forced[outer] || len(forced) != 2 {
		t.Fatalf("exact transitive caller body closure missing: %v %s", forced, why)
	}
	if len(base) != 2 {
		t.Fatal("closure planning mutated live journal")
	}
	for _, test := range []struct {
		name string
		key  proofContributionKey
	}{
		{"header", proofContributionKey{Owner: outer, Phase: proofPhaseHeader}},
		{"instance", proofContributionKey{Owner: outer, Phase: proofPhaseBody, Instance: "i64"}},
		{"unknown-site", proofContributionKey{Owner: outer, Phase: proofPhaseBody, Site: leaf}},
		{"non-function", proofContributionKey{Owner: &ast.ConstDecl{Name: "constant"}, Phase: proofPhaseBody}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := map[proofContributionKey]proofContribution{}
			for key, row := range base {
				rows[key] = row
			}
			rows[test.key] = proofContribution{Key: test.key, Facts: []ProofFact{{Subject: "must-not-borrow"}}, Origins: map[ast.Node]bool{test.key.Owner: true, leaf: true}}
			state.a.proofLifecycle.Segments = rows
			if _, why := state.proofReportBodyReanalysisClosure([]*ast.FuncDecl{leaf}, nil); why == "" {
				t.Fatal("unreproducible contribution silently admitted")
			}
		})
	}
}
