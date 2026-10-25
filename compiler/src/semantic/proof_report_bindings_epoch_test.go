//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strings"
	"testing"
)

// No certificate is injected: actual independently parsed producer syntax is
// retained only in exact source bytes, as happens after comparison projection.
func TestProofBindingPriorSourceEpochWithoutPristineBodies(t *testing.T) {
	baseline := retainedParse(t, proofGeneratedBindingSource)
	old := proofBindingFunction(t, baseline, "moved")
	var block *ast.ExprBlock
	condition := old.Body[0].(*ast.IfStmt)
	block = condition.Cond.(*ast.ExprBlock)
	binder := proofRuntimeQuantifierBinder(block)
	if binder == nil {
		t.Fatal("fixture lacks real producer")
	}
	source := "\n" + proofGeneratedBindingSource
	fresh := retainedParse(t, source)
	owner := proofBindingFunction(t, fresh, "moved")
	current := proofRuntimeQuantifierBinder(owner.Body[0].(*ast.IfStmt).Cond.(*ast.ExprBlock))
	if current == nil || current.Name == binder.Name {
		t.Fatal("fixture lacks distinct source epoch")
	}
	// Mimic precisely the disk comparison projection: declarations/headers stay,
	// bodies are absent. This makes the independently parsed source necessary.
	for _, decl := range baseline.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			fn.Body = nil
		}
	}
	state := &RetainedAnalysis{src: []byte(proofGeneratedBindingSource), pristine: baseline, a: &Analyzer{file: baseline}}
	render := state.prepareProofReportBindings(fresh, map[string][][2]int{old.Pos().File: {{1, 1}}})
	row := proofContribution{Key: proofContributionKey{Owner: owner, Phase: proofPhaseBody}, Origins: map[ast.Node]bool{owner: true}}
	got, why := render(row, ProofFact{Subject: binder.Name})
	if why != "" || got.Subject != current.Name {
		t.Fatalf("exact prior-source binder not recovered: %v %s", got, why)
	}
}
func TestProofBindingBaselineParsePreservesIncludeAttributionAndParity(t *testing.T) {
	const filename = "current-proof-binding.elisa"
	current := []byte("def current() -> i64:\n    return 1\n")
	_ = lexer.New(filename, current)
	before, recorded := lexer.ParitySourceFor(filename)
	source := []byte("#line 7 included-proof-binding.elisa\ndef f() -> bool:\n    return forall i in 0..<2: i > 0\n#line 12 current-proof-binding.elisa\ndef g() -> i64:\n    return 0\n")
	file, why := proofReportBindingSource(filename, source)
	if why != "" {
		t.Fatal(why)
	}
	if len(file.Decls) != 2 || file.Decls[0].Pos().File != "included-proof-binding.elisa" || file.Decls[0].Pos().Line != 7 || file.Decls[1].Pos().File != filename || file.Decls[1].Pos().Line != 12 {
		t.Fatal("include attribution lost", fmt.Sprint(file.Decls))
	}
	after, present := lexer.ParitySourceFor(filename)
	if present != recorded || after != before {
		t.Fatal("old-source syntax parse replaced current parity registration")
	}

}
func TestProofBindingPriorSourceCannotAuthorizeUserLookalike(t *testing.T) {
	const source = "def f() -> i64:\n    __quantifier_90: i64 = 1\n    return __quantifier_90\n"
	file := retainedParse(t, source)
	owner := file.Decls[0].(*ast.FuncDecl)
	state := &RetainedAnalysis{src: []byte(source), pristine: file, a: &Analyzer{file: file}}
	render := state.prepareProofReportBindings(file, nil)
	_, why := render(proofContribution{Key: proofContributionKey{Owner: owner}}, ProofFact{Subject: "__quantifier_90"})
	if !strings.Contains(why, "source material") {
		t.Fatal("source binder got producer authority", why)
	}
}

func TestProofBindingSourceFallbackStaysLazy(t *testing.T) {
	const source = "def f() -> i64:\n    in auto:\n        return 0\n"
	file := retainedParse(t, source)
	owner := file.Decls[0].(*ast.FuncDecl)
	region := owner.Body[0].(*ast.RegionStmt)
	state := &RetainedAnalysis{src: []byte("def : invalid"), pristine: file, a: &Analyzer{file: file}}
	render := state.prepareProofReportBindings(file, nil)
	row := proofContribution{Key: proofContributionKey{Owner: owner}, Origins: map[ast.Node]bool{owner: true}}
	if got, why := render(row, ProofFact{Subject: region.Name}); why != "" || got.Subject != region.Name {
		t.Fatal("already certified producer unnecessarily parsed prior source", got, why)
	}
	if _, why := render(row, ProofFact{Subject: "ordinary subject"}); why != "" {
		t.Fatal("ordinary payload unnecessarily parsed prior source", why)
	}
}
