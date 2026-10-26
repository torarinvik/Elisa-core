//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestIRCodegenReadViewObservesNamedTypesAndGlobalSymbols(t *testing.T) {
	intType := &semantic.BuiltinType{Name: "i32"}
	floatType := &semantic.BuiltinType{Name: "f64"}
	symbol := &semantic.Symbol{Name: "Alpha", Kind: semantic.SymbolGlobal, Type: intType}
	recorder := &irInputReadRecorder{}
	result := &semantic.Result{
		NamedTypes:  map[string]semantic.Type{"Zeta": floatType, "Alpha": intType},
		GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{"Zeta": nil, "Alpha": symbol}},
	}
	view := newIRCodegenReadView(result, recorder)

	if got, found := view.LookupNamedType("Alpha"); !found || got != intType {
		t.Fatalf("named type hit = (%v, %t), want (%v, true)", got, found, intType)
	}
	if got, found := view.LookupNamedType("Missing"); found || got != nil {
		t.Fatalf("named type miss = (%v, %t), want (nil, false)", got, found)
	}
	view.NamedTypesMap("test-full-range")
	if got, found := view.LookupGlobalSymbol("Alpha", "test"); !found || got != symbol {
		t.Fatalf("global lookup hit = (%v, %t), want (%v, true)", got, found, symbol)
	}
	if got, found := view.LookupGlobalSymbol("Missing", "test"); found || got != nil {
		t.Fatalf("global lookup miss = (%v, %t), want (nil, false)", got, found)
	}
	view.GlobalSymbolsMap("test-full-range")

	report := recorder.snapshot()
	if len(report.Queries) != 4 || report.Queries[0].Kind != "named-type" || report.Queries[0].Name != "Alpha" || !report.Queries[0].Found || report.Queries[1].Name != "Missing" || report.Queries[1].Found {
		t.Fatalf("named-type hit/miss observations incomplete: %+v", report.Queries)
	}
	if len(report.Ranges) != 2 {
		t.Fatalf("got %d range observations, want 2: %+v", len(report.Ranges), report.Ranges)
	}
	if got := report.Ranges[0].Entries; len(got) != 2 || got[0].Name != "Alpha" || got[1].Name != "Zeta" || got[0].Type != intType || got[1].Type != floatType {
		t.Fatalf("named-type range omitted or reordered entries: %+v", got)
	}
	if got := report.Ranges[1].Entries; len(got) != 2 || got[0].Name != "Alpha" || got[1].Name != "Zeta" || got[0].Symbol != symbol || got[1].Symbol != nil {
		t.Fatalf("global-symbol range omitted nil/present entries: %+v", got)
	}
}

func TestGeneratorReadViewFollowsInstalledAndRestoredResult(t *testing.T) {
	firstType := &semantic.BuiltinType{Name: "First"}
	nextType := &semantic.BuiltinType{Name: "Next"}
	expr := &ast.Ident{Name: "value"}
	first := &semantic.Result{
		NamedTypes:  map[string]semantic.Type{"T": firstType},
		GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{}},
		ExprTypes:   map[ast.Expr]semantic.Type{expr: firstType},
	}
	next := &semantic.Result{
		NamedTypes:  map[string]semantic.Type{"T": nextType},
		GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{}},
		ExprTypes:   map[ast.Expr]semantic.Type{expr: nextType},
	}
	g := &llvmGenerator{result: first, inputReads: &irInputReadRecorder{}, symbolsByNode: map[ast.Node]*semantic.Symbol{}}

	restore := g.installCodegenResult(next)
	if got := g.namedType("T"); got != nextType {
		t.Fatalf("installed view read %v, want %v", got, nextType)
	}
	if got, present := g.semanticReadView().LookupExprTypes(expr); !present || got != nextType {
		t.Fatalf("installed semantic view read (%v, %t), want (%v, true)", got, present, nextType)
	}
	restore()
	if got := g.namedType("T"); got != firstType {
		t.Fatalf("restored view read %v, want %v", got, firstType)
	}
	if got, present := g.semanticReadView().LookupExprTypes(expr); !present || got != firstType {
		t.Fatalf("restored semantic view read (%v, %t), want (%v, true)", got, present, firstType)
	}
	report := g.inputReads.snapshot()
	if len(report.Queries) < 2 || report.Queries[len(report.Queries)-2].Type != nextType || report.Queries[len(report.Queries)-1].Type != firstType {
		t.Fatalf("reads did not follow result installation/restoration: %+v", report.Queries)
	}
	if got := report.Queries[len(report.Queries)-2].Result; got != next {
		t.Fatalf("installed query lost Result identity: got %p, want %p", got, next)
	}
	if got := report.Queries[len(report.Queries)-1].Result; got != first {
		t.Fatalf("restored query lost Result identity: got %p, want %p", got, first)
	}
	if len(report.Codegen) != 2 || report.Codegen[0].Result != next || report.Codegen[0].Value != nextType || report.Codegen[1].Result != first || report.Codegen[1].Value != firstType {
		t.Fatalf("semantic sparse reads did not preserve installed Result contexts: %+v", report.Codegen)
	}
}

func TestIRCodegenReadViewPreservesNilVersusEmptyIndexes(t *testing.T) {
	var recorder irInputReadRecorder
	nilIndexes := &semantic.Result{}
	nilView := newIRCodegenReadView(nilIndexes, &recorder)
	if nilView.HasNamedTypes() || nilView.HasGlobalScope() {
		t.Fatal("nil semantic indexes reported present")
	}
	if nilView.NamedTypesMap("nil-test") != nil || nilView.GlobalSymbolsMap("nil-test") != nil {
		t.Fatal("nil semantic indexes returned nonnil maps")
	}

	emptyIndexes := &semantic.Result{
		NamedTypes:  map[string]semantic.Type{},
		GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{}},
	}
	emptyView := newIRCodegenReadView(emptyIndexes, &recorder)
	if !emptyView.HasNamedTypes() || !emptyView.HasGlobalScope() {
		t.Fatal("allocated empty semantic indexes reported absent")
	}
	if got := emptyView.NamedTypesMap("empty-test"); got == nil || len(got) != 0 {
		t.Fatalf("empty NamedTypes map = %#v, want allocated empty map", got)
	}
	if got := emptyView.GlobalSymbolsMap("empty-test"); got == nil || len(got) != 0 {
		t.Fatalf("empty GlobalScope.Symbols map = %#v, want allocated empty map", got)
	}

	report := recorder.snapshot()
	if len(report.Ranges) != 4 {
		t.Fatalf("got %d range observations, want four", len(report.Ranges))
	}
	for i, wantNil := range []bool{true, true, false, false} {
		if report.Ranges[i].Nil != wantNil {
			t.Fatalf("range %d nil=%t, want %t", i, report.Ranges[i].Nil, wantNil)
		}
	}
	if report.Ranges[0].Result != nilIndexes || report.Ranges[1].Result != nilIndexes || report.Ranges[2].Result != emptyIndexes || report.Ranges[3].Result != emptyIndexes {
		t.Fatal("range observations lost their Result identity")
	}
}

// The suffix path previously selected a member of the current Symbols map;
// replacing that map index with Scope.Lookup would incorrectly search Parent.
func TestReadViewSuffixMemberDoesNotFallThroughToParent(t *testing.T) {
	local := &semantic.Symbol{Name: "Old.call"}
	inherited := &semantic.Symbol{Name: "Old.call"}
	scope := &semantic.Scope{Symbols: map[string]*semantic.Symbol{"Old.call": local}, Parent: &semantic.Scope{Symbols: map[string]*semantic.Symbol{"Old.call": inherited}}}
	g := &llvmGenerator{result: &semantic.Result{GlobalScope: scope}, inputReads: &irInputReadRecorder{}}
	state := &functionState{g: g}
	if got, name, ok := state.lookupVisibleGlobalSymbol("call"); got != local || name != "Old.call" || !ok {
		t.Fatal("fixture failed to select local suffix")
	}
	// Preserve the historical direct-index behavior even if an already selected
	// suffix no longer has a row in the current map. Parent is not that map.
	scope.Symbols = map[string]*semantic.Symbol{"Other.name": {Name: "Other.name"}}
	if got, name, ok := state.lookupVisibleGlobalSymbol("call"); got != nil || name != "Old.call" || !ok {
		t.Fatalf("direct suffix member became parent lookup: got=%p parent=%p name=%s present=%t", got, inherited, name, ok)
	}
}
