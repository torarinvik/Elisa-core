//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestPlainDefaultCaptureRejectsNewConstructionResources(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	resources := map[string]func(*Analyzer){
		"no-storage":        func(a *Analyzer) { a.noStorageOverlay = &analyzerNoStorageOverlay{} },
		"symbol-identities": func(a *Analyzer) { a.optimizationSymbolIdentityOverlay = &analyzerOptimizationSymbolIdentityOverlay{} },
		"pattern-binders":   func(a *Analyzer) { a.storeScanBinderOverlay = &analyzerStoreScanBinderOverlay{} },
		"parameter-facts":   func(a *Analyzer) { a.storeScanParamFactsOverlay = &analyzerStoreScanParamFactsOverlay{} },
	}
	for name, activate := range resources {
		t.Run(name, func(t *testing.T) {
			result, state := plainDefaultProductFixture(t, "def first(value: i32 = 1) -> i32:\n    return value\n")
			fn := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
			if plainDefaultProgramInputs(state.a) == "" {
				t.Fatal("valid canonical fixture lacks baseline authority")
			}
			activate(state.a)
			if plainDefaultProgramInputs(state.a) != "" {
				t.Fatal("construction map accepted as full program inputs")
			}
			if _, err := captureRetainedPlainDefaultProduct(state.a, fn); err == nil {
				t.Fatal("construction resource entered canonical ownership ledger")
			}
			if _, err := capturePlainGlobalHeaderRows(state.a, nil); err == nil {
				t.Fatal("construction fact storage entered named canonical prefix inventory")
			}
			if _, err := state.MarshalState(); err == nil {
				t.Fatal("construction resource persisted")
			}
		})
	}
}

func TestCanonicalSymbolIdentityInventoryRejectsOverlayWithoutMutation(t *testing.T) {
	symbol := &Symbol{Name: "value"}
	base := map[*Symbol]string{symbol: "symbol@1"}
	a := &Analyzer{optimizationSymbolIDs: map[*Symbol]string{symbol: "symbol@2"}, optimizationSymbolIdentityOverlay: &analyzerOptimizationSymbolIdentityOverlay{base: base}}
	if rows, err := a.canonicalOptimizationSymbolIdentities(); err == nil || rows != nil {
		t.Fatal("inventory exposed an overlay half as canonical authority")
	}
	if err := a.deleteCanonicalOptimizationSymbolIdentity(symbol); err == nil {
		t.Fatal("canonical deletion entered construction overlay")
	}
	if value, _ := a.lookupOptimizationSymbolIdentity(symbol); value != "symbol@2" || base[symbol] != "symbol@1" {
		t.Fatal("rejected operation mutated local or canonical identity")
	}
	a.optimizationSymbolIdentityOverlay = nil
	rows, err := a.canonicalOptimizationSymbolIdentities()
	if err != nil || rows[symbol] != "symbol@2" {
		t.Fatal("idle canonical inventory failed")
	}
	if err := a.deleteCanonicalOptimizationSymbolIdentity(symbol); err != nil {
		t.Fatal(err)
	}
	if _, present := a.lookupOptimizationSymbolIdentity(symbol); present {
		t.Fatal("checked canonical deletion did not remove exact registration")
	}
}

func TestPlainNamedGlobalInventoryRejectsExpressionFactOverlay(t *testing.T) {
	result, state := plainDefaultProductFixture(t, "def first(value: i32 = 1) -> i32:\n    return value\n")
	_ = result
	state.a.exprFactsOverlay = &analyzerExprFactsOverlay{}
	if _, err := capturePlainGlobalHeaderRows(state.a, nil); err == nil {
		t.Fatal("named table inventory treated local fact storage as canonical absence")
	}
}
