//go:build cgo

package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestPlainIdentityLedgerCompleteValuesAndResidualExtentObserver(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	result, state := plainDefaultProductFixture(t, plainDefaultProductPrefix+plainDefaultProductFirst)
	owner := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	product, err := captureRetainedPlainDefaultProduct(state.a, owner)
	if err != nil {
		t.Fatal(err)
	}
	products := map[string]*retainedPlainDefaultProduct{"first": product}
	ledger, err := capturePlainIdentityLedger(state.a, products)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.ownedRows()["optimizationSymbolIDs"] != 2 {
		t.Fatal("fixture does not cover both exclusively observed actual globals")
	}
	symbol := state.a.globalScope.Symbols["values"]
	token := state.a.optimizationSymbolIDs[symbol]
	key := "global-symbol:values"
	before := ledger.Entries[key].Value
	// Full symbol metadata matters even when source, keys and row counts agree.
	symbol.LinkName += "_changed"
	changed, err := captureRetainedPlainDefaultProduct(state.a, owner)
	if err != nil {
		t.Fatal(err)
	}
	after, err := capturePlainIdentityLedger(state.a, map[string]*retainedPlainDefaultProduct{"first": changed})
	if err != nil {
		t.Fatal(err)
	}
	if after.Entries[key].Value == before {
		t.Fatal("complete identity value silently reduced to source/key/cardinality")
	}
	symbol.LinkName = symbol.LinkName[:len(symbol.LinkName)-len("_changed")]
	// A dependency may occur only in Extent, after propagation, with no new
	// optimizationSymbolIdentity call and no base field. It remains a live observer.
	node := state.a.globalScope.Symbols["cursor"].Node.(*ast.GlobalDecl).Value
	facts, present := state.a.lookupExprFacts(node)
	extent := &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: token, End: "3"}
	injected := facts
	injected.Extent = extent
	injected.ReadOnly = true
	injected.Contiguous = true
	state.a.setExprFacts(node, injected)
	defer func() {
		if present {
			state.a.setExprFacts(node, facts)
		} else {
			state.a.deleteExprFacts(node)
		}
	}()
	ledger, err = capturePlainIdentityLedger(state.a, products)
	if err != nil {
		t.Fatal(err)
	}
	if !ledger.Entries[key].Residual || ledger.ownedRows()["optimizationSymbolIDs"] != 1 {
		t.Fatal("propagated Extent-only observer lost from residual authority")
	}
	if !reflect.DeepEqual(injected.Extent, extent) || injected.base != facts.base {
		t.Fatal("observation changed optimization fact payload")
	}
}

func TestPlainIdentityLedgerAliasedAllocationRegistrationDeclines(t *testing.T) {
	result, state := plainDefaultProductFixture(t, plainDefaultProductPrefix+plainDefaultProductFirst)
	owner := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	product, err := captureRetainedPlainDefaultProduct(state.a, owner)
	if err != nil {
		t.Fatal(err)
	}
	left, right := state.a.globalScope.Symbols["values"], state.a.globalScope.Symbols["cursor"]
	saved := state.a.optimizationSymbolIDs[right]
	state.a.optimizationSymbolIDs[right] = state.a.optimizationSymbolIDs[left]
	defer func() { state.a.optimizationSymbolIDs[right] = saved }()
	if _, err := capturePlainIdentityLedger(state.a, map[string]*retainedPlainDefaultProduct{"first": product}); err == nil {
		t.Fatal("two distinct global sources shared one allocation identity")
	}
}
