//go:build cgo

package semantic

import (
	"context"
	"elisacore/src/incremental"
	"encoding/json"
	"sort"
	"testing"
)

func TestLocalBoundedLookupSessionProjectProbe(t *testing.T) {
	result, contexts, records := loadLookupQueryProject(t)
	a := result.analyzer
	publications := map[incremental.Key]incremental.Publication{}
	expected := map[incremental.Key]string{}
	add := func(key incremental.Key, changes []incremental.Change, value any) {
		t.Helper()
		p, err := lookupPublication(key, changes)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if old, ok := expected[key]; ok && old != string(encoded) {
			t.Fatal("incoherent request inventory")
		}
		publications[key] = p
		expected[key] = string(encoded)
	}
	skippedSites := 0
	for _, record := range records {
		if record.Kind == "enclosing" {
			skippedSites++
			continue
		}
		ctx := contexts[record.Context-1]
		a.currentFuncType = nil
		if ctx.Phase == "function" {
			a.currentFuncType = &FuncType{Name: ctx.Function}
		}
		a.withResolutionContext(ctx.Namespace, ctx.Usings, func() {
			key, changes, err := a.lookupSelectionInputs(record.Kind, record.Request, "")
			if err != nil {
				t.Fatal(err)
			}
			name := ""
			found := false
			switch record.Kind {
			case "value":
				_, name, found = a.lookupVisibleGlobal(record.Request)
			case "type":
				_, name, found = a.lookupVisibleType(record.Request)
			case "permission":
				_, name, found = a.lookupVisiblePermission(record.Request)
			default:
				t.Fatal("unsupported inventory lookup")
			}
			add(key, changes, lookupSelectionProduct{Name: name, Found: found})
			if record.Kind == "value" {
				key, changes, err = a.lookupAmbiguityInputs(record.Request)
				if err != nil {
					t.Fatal(err)
				}
				add(key, changes, a.usingAmbiguityMatches(record.Request))
			}
		})
	}
	a.currentFuncType = nil
	var batch []incremental.Publication
	for _, p := range publications {
		batch = append(batch, p)
	}
	sort.Slice(batch, func(i, j int) bool {
		if batch[i].Key.Kind != batch[j].Key.Kind {
			return batch[i].Key.Kind < batch[j].Key.Kind
		}
		return batch[i].Key.Name < batch[j].Key.Name
	})
	session, err := incremental.NewSession(lookupQueryEvaluators(), incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var before incremental.SessionStats
	for pass := 0; pass < 2; pass++ {
		published, err := session.PublishPrepared(context.Background(), func() ([]incremental.Publication, error) { return batch, nil })
		if err != nil || len(published.Skipped) != 0 {
			t.Fatalf("real lookup world did not fit: skipped%d err%v", len(published.Skipped), err)
		}
		for _, p := range batch {
			got, err := published.Revision.Evaluate(context.Background(), p.Key)
			if err != nil || got != expected[p.Key] {
				t.Fatalf("bounded query disagreed with current resolver: %v", err)
			}
		}
		stats := session.Stats()
		t.Logf("pass=%d stats=%+v", pass, stats)
		if pass == 1 && stats.Queries.Executed != before.Queries.Executed {
			t.Fatal("unchanged batch reexecuted lookup queries")
		}
		before = stats
	}
	t.Logf("unique_queries=%d skipped_unanchored_sites=%d frontend_errors=%d", len(batch), skippedSites, len(result.Errors()))
}
