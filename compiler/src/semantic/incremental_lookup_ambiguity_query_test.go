//go:build cgo

package semantic

import (
	"context"
	"elisacore/src/incremental"
	"reflect"
	"testing"
)

func TestLookupAmbiguityInvalidatesParentWhenSelectionStaysEqual(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.currentUsings = []string{"A", "B"}
	a.globalScope.Symbols["A.target"] = &Symbol{Name: "A.target"}
	selection, selectChanges, err := a.lookupSelectionInputs("value", "target", "")
	if err != nil {
		t.Fatal(err)
	}
	ambiguity, ambiguityChanges, err := a.lookupAmbiguityInputs("target")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Name != ambiguity.Name || lookupRecipeInput(selection) == lookupRecipeInput(ambiguity) || lookupBindingInput(selection, "A.target") == lookupBindingInput(ambiguity, "A.target") {
		t.Fatal("selection and ambiguity input domains collide")
	}
	if _, err := readLookupSelection(newLookupSelectionDatabase(), ambiguity); err == nil {
		t.Fatal("ambiguity query accepted as a name-selection product")
	}
	runs := 0
	db := incremental.New(map[string]incremental.Evaluator{
		lookupSelectionQueryKind: evaluateLookupSelection, lookupAmbiguityQueryKind: evaluateLookupAmbiguity,
		"lookup-dependency-group": func(r *incremental.Reader, _ string) (string, error) {
			runs++
			selected, err := r.Query(selection)
			if err != nil {
				return "", err
			}
			matches, err := r.Query(ambiguity)
			if err != nil {
				return "", err
			}
			return selected + "\n" + matches, nil
		},
	})
	db.Apply(append(selectChanges, ambiguityChanges...)...)
	parent := incremental.Key{Kind: "lookup-dependency-group", Name: "caller"}
	first, err := db.Evaluate(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if a.reportUsingAmbiguity("target", a.currentFuncDecl.Position) {
		t.Fatal("unexpected initial ambiguity")
	}
	a.globalScope.Symbols["B.target"] = &Symbol{Name: "B.target"}
	_, selectChanges, err = a.lookupSelectionInputs("value", "target", "")
	if err != nil {
		t.Fatal(err)
	}
	_, ambiguityChanges, err = a.lookupAmbiguityInputs("target")
	if err != nil {
		t.Fatal(err)
	}
	db.Apply(append(selectChanges, ambiguityChanges...)...)
	second, err := db.Evaluate(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	product, err := readLookupSelection(db, selection)
	if err != nil || product.Name != "A.target" || !product.Found {
		t.Fatal("selection changed with lower-priority import")
	}
	if first == second || runs != 2 {
		t.Fatal("ambiguity change did not invalidate parent")
	}
	if !a.reportUsingAmbiguity("target", a.currentFuncDecl.Position) {
		t.Fatal("query change did not correspond to a real diagnostic")
	}
	// Found-nil is selectable by value lookup, but is not an ambiguity match.
	a.globalScope.Symbols["A.target"] = nil
	_, selectChanges, err = a.lookupSelectionInputs("value", "target", "")
	if err != nil {
		t.Fatal(err)
	}
	_, ambiguityChanges, err = a.lookupAmbiguityInputs("target")
	if err != nil {
		t.Fatal(err)
	}
	db.Apply(append(selectChanges, ambiguityChanges...)...)
	third, err := db.Evaluate(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if third != first || runs != 3 || a.reportUsingAmbiguity("target", a.currentFuncDecl.Position) {
		t.Fatal("nil binding policies leaked between query domains")
	}
}
func TestLookupAmbiguityMatchesResolverPolicyAndTracksOverride(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.currentUsings = []string{"B", "A", "B"}
	a.globalScope.Symbols["A.target"] = &Symbol{Name: "A.target"}
	a.globalScope.Symbols["B.target"] = &Symbol{Name: "B.target"}
	db := newLookupSelectionDatabase()
	for step := 0; step < 6; step++ {
		key, changes, err := a.lookupAmbiguityInputs("target")
		if err != nil {
			t.Fatal(err)
		}
		db.Apply(changes...)
		encoded, err := db.Evaluate(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		if err := decodeLookupSelection(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, a.usingAmbiguityMatches("target")) {
			t.Fatalf("step%d diagnostic set differs: %v", step, got)
		}
		switch step {
		case 0:
			a.globalScope.Symbols["Local.target"] = nil // present namespace binding suppresses ambiguity
		case 1:
			delete(a.globalScope.Symbols, "Local.target")
		case 2:
			a.globalScope.Symbols["B.target"] = nil // imported nil does not count
		case 3:
			a.globalScope.Symbols["B.target"] = &Symbol{Name: "B.target", Private: true}
		case 4:
			a.globalScope.Symbols["B.target"].Private = false
		}
	}
	// Qualified requests bypass wildcard ambiguity exactly as the resolver does.
	key, changes, err := a.lookupAmbiguityInputs("A.target")
	if err != nil {
		t.Fatal(err)
	}
	db.Apply(changes...)
	if got, err := db.Evaluate(context.Background(), key); err != nil || got != "null" {
		t.Fatal("qualified lookup charged ambiguity")
	}
	key, changes, err = a.lookupAmbiguityInputs("target")
	if err != nil {
		t.Fatal(err)
	}
	db.Apply(changes...)
	db.Apply(incremental.Change{Key: lookupBindingInput(key, "A.target"), Value: "corrupt"})
	if _, err := db.Evaluate(context.Background(), key); err == nil {
		t.Fatal("malformed ambiguity binding accepted")
	}
	_, changes, err = a.lookupAmbiguityInputs("target")
	if err != nil {
		t.Fatal(err)
	}
	db.Apply(changes...)
	if _, err := db.Evaluate(context.Background(), key); err != nil {
		t.Fatal("failed ambiguity query did not recover")
	}
	// Missing publication and malformed bindings never produce cached outcomes.
	key, err = lookupAmbiguityKey(lookupTestSpec())
	if err != nil {
		t.Fatal(err)
	}
	other := newLookupSelectionDatabase()
	if _, err := other.Evaluate(context.Background(), key); err == nil {
		t.Fatal("unpublished ambiguity accepted")
	}
}
