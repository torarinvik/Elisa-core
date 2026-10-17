package semantic

import (
	"context"
	"elisacore/src/incremental"
	"encoding/json"
	"strings"
	"testing"
)

func lookupTestSpec() lookupSelectionSpec {
	return lookupSelectionSpec{Kind: "value", Request: "target", Context: lookupDependencyContext{Namespace: "Local", Function: "Local.caller", Origin: "root.elisa", Phase: "function", Usings: []string{"Public"}}}
}
func publishLookup(t *testing.T, db *incremental.Database, spec lookupSelectionSpec, reads []lookupCandidateRead, shadow bool) incremental.Key {
	t.Helper()
	key, changes, err := lookupSelectionChanges(spec, reads, shadow)
	if err != nil {
		t.Fatal(err)
	}
	db.Apply(changes...)
	return key
}
func TestLookupSelectionTrackedMissingVisibilityAndParent(t *testing.T) {
	executions := 0
	db := incremental.New(map[string]incremental.Evaluator{
		lookupSelectionQueryKind: evaluateLookupSelection,
		"parent": func(r *incremental.Reader, name string) (string, error) {
			executions++
			return r.Query(incremental.Key{Kind: lookupSelectionQueryKind, Name: name})
		},
	})
	spec := lookupTestSpec()
	reads := []lookupCandidateRead{{"Local.target", false, false}, {"Public.target", true, true}, {"target", true, true}}
	key := publishLookup(t, db, spec, reads, false)
	parent := incremental.Key{Kind: "parent", Name: key.Name}
	evaluate := func(want string) {
		t.Helper()
		encoded, err := db.Evaluate(context.Background(), parent)
		if err != nil {
			t.Fatal(err)
		}
		var product lookupSelectionProduct
		if err := json.Unmarshal([]byte(encoded), &product); err != nil {
			t.Fatal(err)
		}
		if product.Name != want || product.Found != (want != "") {
			t.Fatalf("stale selection: %+v", product)
		}
	}
	evaluate("Public.target")
	before := db.Stats()
	// Unread lower-priority candidates do not invalidate the requested closure.
	db.Apply(incremental.Change{Key: lookupBindingInput(key, "target"), Remove: true})
	evaluate("Public.target")
	if executions != 1 || db.Stats().Executed != before.Executed {
		t.Fatal("unread suffix reran a parent")
	}
	reads[0] = lookupCandidateRead{"Local.target", true, true}
	publishLookup(t, db, spec, reads, false)
	evaluate("Local.target")
	if executions != 2 {
		t.Fatal("previously missing name did not wake parent")
	}
	reads[0].Visible = false
	publishLookup(t, db, spec, reads, false)
	evaluate("Public.target")
	if executions != 3 {
		t.Fatal("visibility change did not wake parent")
	}
	reads[0].Present = false
	publishLookup(t, db, spec, reads, false)
	evaluate("Public.target")
	if executions != 3 {
		t.Fatal("unchanged selection reran its parent")
	}
	reads[1].Present = false
	reads[1].Visible = false
	reads[2].Present = false
	reads[2].Visible = false
	publishLookup(t, db, spec, reads, false)
	evaluate("")
	reads[2].Present = true
	reads[2].Visible = true
	publishLookup(t, db, spec, reads, false)
	evaluate("target")
	// Candidate generation changes (aliases/import priority) are tracked too.
	reads = []lookupCandidateRead{{"Local.target", false, false}, {"Public.target", false, false}, {"Alias.target", true, true}, {"target", true, true}}
	publishLookup(t, db, spec, reads, false)
	evaluate("Alias.target")
	if db.Stats().DependencyChecks == 0 {
		t.Fatal("no tracked dependency validation")
	}
}
func TestLookupSelectionLexicalShadowSitesStayIndependent(t *testing.T) {
	db := newLookupSelectionDatabase()
	outer := lookupTestSpec()
	outer.Kind = "enclosing"
	outer.Site = "function-source-anchor/outer"
	inner := outer
	inner.Site = "function-source-anchor/inner"
	reads := []lookupCandidateRead{{"Local.target", true, true}}
	outerKey := publishLookup(t, db, outer, reads, false)
	innerKey := publishLookup(t, db, inner, reads, true)
	if outerKey == innerKey {
		t.Fatal("distinct lexical sites conflated")
	}
	if got, err := readLookupSelection(db, outerKey); err != nil || !got.Found {
		t.Fatal("outer lookup lost")
	}
	if got, err := readLookupSelection(db, innerKey); err != nil || got.Found {
		t.Fatal("inner local shadow ignored")
	}
	publishLookup(t, db, inner, reads, false)
	if got, err := readLookupSelection(db, innerKey); err != nil || !got.Found {
		t.Fatal("removed local shadow did not invalidate")
	}
	outer.Site = ""
	if _, _, err := lookupSelectionChanges(outer, reads, false); err == nil {
		t.Fatal("unanchored shadow context admitted")
	}
}
func TestLookupSelectionRejectsIncompleteAndMalformedInputs(t *testing.T) {
	spec := lookupTestSpec()
	db := newLookupSelectionDatabase()
	key, err := lookupSelectionKey(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readLookupSelection(db, key); err == nil {
		t.Fatal("unpublished recipe accepted")
	}
	publishLookup(t, db, spec, []lookupCandidateRead{{"Local.target", true, true}}, false)
	db.Apply(incremental.Change{Key: lookupBindingInput(key, "Local.target"), Value: "corrupt"})
	if _, err := readLookupSelection(db, key); err == nil {
		t.Fatal("malformed binding memoized")
	}
	db.Apply(incremental.Change{Key: lookupBindingInput(key, "Local.target"), Value: "visible"})
	if got, err := readLookupSelection(db, key); err != nil || !got.Found {
		t.Fatal("query did not recover after malformed input")
	}
	for _, recipe := range []string{"null", `{"Candidates":["\ud800"],"LocalShadowed":false}`, string([]byte{255}), `{"Candidates":["x"],"Unexpected":true}`, `{"Candidates":["x","x"]}`, `{"Candidates":["x"]} {}`, strings.Repeat("x", lookupSelectionMaxBytes+1)} {
		db.Apply(incremental.Change{Key: lookupRecipeInput(key), Value: recipe})
		if _, err := readLookupSelection(db, key); err == nil {
			t.Fatal("invalid recipe accepted")
		}
	}
	bad := spec
	bad.Context.Usings = make([]string, lookupTraceMaxCandidates+1)
	if _, err := lookupSelectionKey(bad); err == nil {
		t.Fatal("unbounded imports accepted")
	}
	if _, _, err := lookupSelectionChanges(spec, []lookupCandidateRead{{"x", false, true}}, false); err == nil {
		t.Fatal("impossible visibility accepted")
	}
	if _, _, err := lookupSelectionChanges(spec, []lookupCandidateRead{{"x", true, true}, {"x", true, true}}, false); err == nil {
		t.Fatal("duplicate candidate accepted")
	}
	bad = spec
	bad.Request = string([]byte{255})
	if _, err := lookupSelectionKey(bad); err == nil {
		t.Fatal("lossy UTF8 key accepted")
	}
}
