//go:build cgo

package semantic

import (
	"context"
	"elisacore/src/incremental"
	"errors"
	"testing"
)

func TestLookupPublicationSessionComposesAndInvalidates(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.currentUsings = []string{"A", "B"}
	a.globalScope.Symbols["A.target"] = &Symbol{Name: "A.target"}
	selected, _, err := a.lookupSelectionInputs("value", "target", "")
	if err != nil {
		t.Fatal(err)
	}
	ambiguous, _, err := a.lookupAmbiguityInputs("target")
	if err != nil {
		t.Fatal(err)
	}
	registry := lookupQueryEvaluators()
	parent := incremental.Key{Kind: "lookup-parent", Name: "caller"}
	registry[parent.Kind] = func(r *incremental.Reader, _ string) (string, error) {
		first, err := r.Query(selected)
		if err != nil {
			return "", err
		}
		second, err := r.Query(ambiguous)
		return first + "\n" + second, err
	}
	session, err := incremental.NewSession(registry, incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	publish := func() incremental.Published {
		t.Helper()
		var publications []incremental.Publication
		for _, ambiguity := range []bool{false, true} {
			var key incremental.Key
			var changes []incremental.Change
			var err error
			if ambiguity {
				key, changes, err = a.lookupAmbiguityInputs("target")
			} else {
				key, changes, err = a.lookupSelectionInputs("value", "target", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			publication, err := lookupPublication(key, changes)
			if err != nil {
				t.Fatal(err)
			}
			publications = append(publications, publication)
		}
		publications = append(publications, incremental.Publication{Key: parent, MaxResultBytes: 256, MaxDependencies: 2})
		result, err := session.Publish(context.Background(), publications)
		if err != nil || len(result.Skipped) != 0 {
			t.Fatalf("publication failed: %v", err)
		}
		return result
	}
	first := publish()
	before, err := first.Revision.Evaluate(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	stats := session.Stats()
	repeated := publish()
	same, err := repeated.Revision.Evaluate(context.Background(), parent)
	if err != nil || same != before || session.Stats().Queries.Executed != stats.Queries.Executed {
		t.Fatal("unchanged publication lost reuse")
	}
	a.globalScope.Symbols["B.target"] = &Symbol{Name: "B.target"}
	changed := publish()
	after, err := changed.Revision.Evaluate(context.Background(), parent)
	if err != nil || after == before {
		t.Fatal("atomic changed ambiguity did not wake parent")
	}
	if _, err := repeated.Revision.Evaluate(context.Background(), parent); !errors.Is(err, incremental.ErrStaleRevision) {
		t.Fatal("old world not invalidated")
	}
	if session.Stats().Queries.Executed != stats.Queries.Executed+2 {
		t.Fatal("selection-only child was unnecessarily executed")
	}
}
func TestLookupPublicationRejectsIncompleteOwnership(t *testing.T) {
	a := lookupTraceAnalyzer()
	key, changes, err := a.lookupSelectionInputs("value", "missing", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lookupPublication(key, changes); err != nil {
		t.Fatal(err)
	}
	if _, err := lookupPublication(key, changes[:len(changes)-1]); err == nil {
		t.Fatal("missing negative binding admitted")
	}
	extra := append(append([]incremental.Change(nil), changes...), incremental.Change{Key: incremental.Key{Kind: "foreign", Name: "unowned"}, Value: "x"})
	if _, err := lookupPublication(key, extra); err == nil {
		t.Fatal("unowned input admitted")
	}
	wrong := append([]incremental.Change(nil), changes...)
	wrong[0].Value = "null"
	if _, err := lookupPublication(key, wrong); err == nil {
		t.Fatal("malformed recipe admitted")
	}
}
