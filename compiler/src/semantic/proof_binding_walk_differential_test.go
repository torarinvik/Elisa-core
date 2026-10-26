//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func proofBindingFactForRow(t *testing.T, candidate, reference func(proofContribution, ProofFact) (ProofFact, string), row proofContribution, text string) {
	t.Helper()
	input := ProofFact{Subject: text, Predicate: text, Missing: text, KnownFacts: []string{text}, ClosedWorldFacts: []string{text}}
	got, gotErr := candidate(row, input)
	want, wantErr := reference(row, input)
	if gotErr != wantErr || !reflect.DeepEqual(got, want) {
		t.Fatalf("typed source walk differs for %q: got %#v / %q, want %#v / %q", text, got, gotErr, want, wantErr)
	}
}

func TestProofBindingTypedWalkMatchesFrozenCollectorAcrossScalarShapes(t *testing.T) {
	const source = `#line 10 proof-map.elisa
law Positive(self: i64) = self > 0

def moved() -> i64:
    in auto:
        return 0

#line 30 proof-map.elisa
def quantified(values: darray[i64]) -> i64 is Positive:
    if forall index in 0..<values.count: values[index] > 0:
        return 1
    return 2

def text() -> String:
    return "ordinary"
`
	baseline := retainedParse(t, source)
	oldMoved := proofBindingFunction(t, baseline, "moved")
	oldRegion, ok := oldMoved.Body[0].(*ast.RegionStmt)
	if !ok || !oldRegion.Lazy {
		t.Fatal("fixture lacks generated region")
	}
	oldQuantified := proofBindingFunction(t, baseline, "quantified")
	oldCondition, ok := oldQuantified.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatal("fixture lacks quantifier condition")
	}
	oldBlock, ok := oldCondition.Cond.(*ast.ExprBlock)
	if !ok {
		t.Fatal("fixture lacks parser-lowered quantifier block")
	}
	oldQuantifier := proofRuntimeQuantifierBinder(oldBlock)
	if oldQuantifier == nil {
		t.Fatal("fixture lacks a valid runtime quantifier producer")
	}

	// A leading physical line changes offset-derived names while #line keeps
	// declaration-relative source identities stable.
	fresh := retainedParse(t, "\n"+source)
	newMoved := proofBindingFunction(t, fresh, "moved")
	newRegion := newMoved.Body[0].(*ast.RegionStmt)
	newQuantified := proofBindingFunction(t, fresh, "quantified")
	newCondition := newQuantified.Body[0].(*ast.IfStmt)
	newQuantifier := proofRuntimeQuantifierBinder(newCondition.Cond.(*ast.ExprBlock))
	if newRegion.Name == oldRegion.Name || newQuantifier == nil || newQuantifier.Name == oldQuantifier.Name {
		t.Fatal("fixture did not create distinct old/new binder epochs")
	}

	// StringLit.Value is source data, even when it happens to equal a producer
	// spelling. It must not suppress an otherwise valid source binding.
	literalFn := proofBindingFunction(t, fresh, "text")
	literal := literalFn.Body[0].(*ast.ReturnStmt).Value.(*ast.StringLit)
	literal.Value = oldRegion.Name
	state := &RetainedAnalysis{a: &Analyzer{file: baseline}, pristine: baseline, src: []byte(source)}
	var marks map[string][][2]int // #line keeps both source identities stable despite offset change.
	candidate := state.prepareProofReportBindings(fresh, marks)
	reference := state.prepareProofReportBindingsPositionReference(fresh, marks)
	regionRow := proofContribution{Key: proofContributionKey{Owner: newMoved, Phase: proofPhaseBody}, Origins: map[ast.Node]bool{newMoved: true}}
	quantifierRow := proofContribution{Key: proofContributionKey{Owner: newQuantified, Phase: proofPhaseBody}, Origins: map[ast.Node]bool{newQuantified: true}}
	proofBindingFactForRow(t, candidate, reference, regionRow, oldRegion.Name+" == "+fmt.Sprintf("%q", oldRegion.Name))
	proofBindingFactForRow(t, candidate, reference, quantifierRow, oldQuantifier.Name+" == 1")
	proofBindingFactForRow(t, candidate, reference, regionRow, "plain subject")

	// A normal string field and lexer.Pos.File are both lookalike source
	// material; retain the exact fail-closed behavior of the old walker.
	textFn := proofBindingFunction(t, fresh, "text")
	originalName, originalFile := textFn.Name, textFn.Position.File
	for _, mutation := range []struct {
		apply   func()
		restore func()
	}{
		{
			apply:   func() { textFn.Name = oldRegion.Name },
			restore: func() { textFn.Name = originalName },
		},
		{
			apply:   func() { textFn.Position.File = oldRegion.Name },
			restore: func() { textFn.Position.File = originalFile },
		},
	} {
		mutation.apply()
		candidate = state.prepareProofReportBindings(fresh, marks)
		reference = state.prepareProofReportBindingsPositionReference(fresh, marks)
		_, gotErr := candidate(regionRow, ProofFact{Subject: oldRegion.Name})
		_, wantErr := reference(regionRow, ProofFact{Subject: oldRegion.Name})
		if gotErr != wantErr || gotErr == "" || !strings.Contains(gotErr, "source material") {
			t.Fatalf("lookalike source spelling was not rejected identically: candidate=%q reference=%q", gotErr, wantErr)
		}
		mutation.restore()
	}
}
