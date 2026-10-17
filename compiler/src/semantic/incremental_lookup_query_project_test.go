//go:build cgo

package semantic

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// The trace is a request inventory, never publication authority. Every query's
// COMPLETE candidates and binding states come from a current fresh Analyzer.
func loadLookupQueryProject(t *testing.T) (*Result, []lookupDependencyContext, []lookupDependencyRecord) {
	t.Helper()
	snapshotPath := os.Getenv("ELISA_LOOKUP_QUERY_PROBE_SNAPSHOT")
	tracePath := os.Getenv("ELISA_LOOKUP_QUERY_PROBE_TRACE")
	if snapshotPath == "" || tracePath == "" {
		t.Skip("set lookup query probe snapshot and trace")
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct{ Filename, Source string }
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Filename == "" || snapshot.Source == "" {
		t.Fatal("missing frozen source")
	}
	data, err = os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	var trace LookupDependencyTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatal(err)
	}
	if trace.Version != 2 || trace.Truncated || trace.Dropped != 0 || len(trace.Contexts)+len(trace.Records) > lookupTraceMaxEntries {
		t.Fatal("incomplete/bounded audit inventory required")
	}
	contexts := make([]lookupDependencyContext, len(trace.Contexts))
	for i, encoded := range trace.Contexts {
		if err := decodeLookupSelection(encoded, &contexts[i]); err != nil {
			t.Fatal(err)
		}
	}
	records := make([]lookupDependencyRecord, len(trace.Records))
	for i, encoded := range trace.Records {
		if err := decodeLookupSelection(encoded, &records[i]); err != nil {
			t.Fatal(err)
		}
		if records[i].Context == 0 || int(records[i].Context) > len(contexts) {
			t.Fatal("invalid inventory context")
		}
	}
	lex := lexer.New(snapshot.Filename, []byte(snapshot.Source))
	tokens := lex.Tokenize()
	parsed := parser.New(tokens)
	file := parsed.ParseFile(snapshot.Filename)
	if len(lex.Errors()) != 0 || len(parsed.Errors()) != 0 {
		t.Fatal("frozen syntax changed")
	}
	result := Analyze(file)
	return result, contexts, records
}

func TestLocalLookupSelectionProjectProbe(t *testing.T) {
	result, contexts, records := loadLookupQueryProject(t)
	a := result.analyzer
	db := newLookupSelectionDatabase()
	checked, skipped, changedFromAudit, ambiguityChecks := 0, 0, 0, 0
	for pass := 0; pass < 2; pass++ {
		for _, record := range records {
			if record.Kind == "enclosing" {
				if pass == 0 {
					skipped++
				}
				continue
			} // audit has no validated lexical site anchor
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
				db.Apply(changes...)
				got, err := readLookupSelection(db, key)
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
					t.Fatal("unsupported inventory domain")
				}
				if got.Name != name || got.Found != found {
					t.Fatalf("stale %s selection of %q", record.Kind, record.Request)
				}
				if pass == 0 {
					previous := lookupSelectionProduct{}
					for _, read := range record.Candidates {
						if read.Visible {
							previous = lookupSelectionProduct{Name: read.Name, Found: true}
							break
						}
					}
					if previous != got {
						changedFromAudit++
					}
				}
				if record.Kind == "value" {
					ak, ac, err := a.lookupAmbiguityInputs(record.Request)
					if err != nil {
						t.Fatal(err)
					}
					db.Apply(ac...)
					encoded, err := db.Evaluate(context.Background(), ak)
					if err != nil {
						t.Fatal(err)
					}
					var matches []string
					if err := decodeLookupSelection(encoded, &matches); err != nil || !slices.Equal(matches, a.usingAmbiguityMatches(record.Request)) {
						t.Fatal("ambiguity adapter differs from current resolver")
					}
					ambiguityChecks++
				}
				checked++
			})
		}
		t.Logf("pass=%d checked=%d queries=%+v", pass, checked, db.Stats())
	}
	a.currentFuncType = nil
	t.Logf("inventory_sites=%d skipped_unanchored=%d frontend_errors=%d changed_from_original_audit=%d ambiguity_checks=%d", len(records), skipped, len(result.Errors()), changedFromAudit, ambiguityChecks)
	if checked == 0 || db.Stats().MemoHits == 0 {
		t.Fatal("no real-world lookup query reuse")
	}
}
