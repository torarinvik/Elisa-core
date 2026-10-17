//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type decodedLookupRecord struct {
	lookupDependencyContext
	lookupDependencyRecord
}

func decodeLookupTrace(t *testing.T, trace LookupDependencyTrace) []decodedLookupRecord {
	t.Helper()
	var records []decodedLookupRecord
	for _, encoded := range trace.Records {
		var record lookupDependencyRecord
		if err := json.Unmarshal([]byte(encoded), &record); err != nil {
			t.Fatal(err)
		}
		if record.Context == 0 || int(record.Context) > len(trace.Contexts) {
			t.Fatal("invalid context reference")
		}
		var context lookupDependencyContext
		if err := json.Unmarshal([]byte(trace.Contexts[record.Context-1]), &context); err != nil {
			t.Fatal(err)
		}
		records = append(records, decodedLookupRecord{context, record})
	}
	return records
}

func lookupTraceAnalyzer() *Analyzer {
	global := NewScope(nil)
	return &Analyzer{file: &ast.File{Filename: "authority.elisa"}, globalScope: global, currentScope: NewScope(global), currentNamespace: "Local", currentFuncDecl: &ast.FuncDecl{Name: "caller"}, currentFuncType: &FuncType{Name: "Local.caller"}, currentUsings: []string{"Hidden.Inner", "Public"}, privateModules: map[string]bool{}, namedTypes: map[string]Type{}, privateTypeNames: map[string]bool{}, lookupTrace: &lookupDependencyRecorder{}}
}
func TestLookupTraceRecordsMissingAndInvisibleCandidates(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.privateModules["Hidden.Inner"] = true
	a.globalScope.Symbols["Hidden.Inner.target"] = &Symbol{Name: "Hidden.Inner.target"}
	selected := &Symbol{Name: "Public.target"}
	a.globalScope.Symbols["Public.target"] = selected
	if got, name, ok := a.lookupVisibleGlobal("target"); !ok || got != selected || name != "Public.target" {
		t.Fatal("resolution changed")
	}
	records := decodeLookupTrace(t, a.lookupTrace.snapshot())
	want := []lookupCandidateRead{{"Local.target", false, false}, {"Hidden.Inner.target", true, false}, {"Public.target", true, true}}
	if len(records) != 1 || !reflect.DeepEqual(records[0].Candidates, want) || records[0].Function != "Local.caller" || records[0].Origin != "authority.elisa" {
		t.Fatalf("incomplete candidate trace: %+v", records)
	}
	// Adding a higher-priority binding must produce a different read product.
	a.globalScope.Symbols["Local.target"] = &Symbol{Name: "Local.target"}
	a.lookupVisibleGlobal("target")
	if len(a.lookupTrace.snapshot().Records) != 2 {
		t.Fatal("new higher-priority binding was not observed")
	}
	// Repeating an equal lookup deduplicates without losing the read count.
	a.lookupVisibleGlobal("target")
	if trace := a.lookupTrace.snapshot(); len(trace.Records) != 2 || trace.Reads != 3 {
		t.Fatalf("dedup lost read: %+v", trace)
	}
	// Missing every candidate is still a dependency, including qualified paths.
	a.lookupVisibleGlobal("absent")
	last := a.lookupTrace.snapshot()
	found := false
	for _, record := range decodeLookupTrace(t, last) {
		if record.Request == "absent" {
			found = true
			for _, candidate := range record.Candidates {
				if candidate.Present {
					t.Fatal("miss changed")
				}
			}
		}
	}
	if !found {
		t.Fatal("negative lookup lost")
	}
}
func TestLookupTraceTypesAndEnclosingShadow(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.currentUsings = []string{"Hidden", "Public"}
	a.namedTypes["Hidden.Item"] = &StructType{Name: "Hidden.Item"}
	a.privateTypeNames["Hidden.Item"] = true
	a.namedTypes["Public.Item"] = &StructType{Name: "Public.Item"}
	if _, name, ok := a.lookupVisibleType("Item"); !ok || name != "Public.Item" {
		t.Fatal("type lookup changed")
	}
	a.globalScope.Symbols["Local.sibling"] = &Symbol{Name: "Local.sibling"}
	if _, _, ok := a.enclosingNamespaceMember("sibling"); !ok {
		t.Fatal("missing enclosing member")
	}
	a.currentScope.Symbols["sibling"] = &Symbol{Name: "sibling", Kind: SymbolParam}
	if _, _, ok := a.enclosingNamespaceMember("sibling"); ok {
		t.Fatal("local shadow overridden")
	}
	records := decodeLookupTrace(t, a.lookupTrace.snapshot())
	var types, enclosing, shadowed bool
	for _, record := range records {
		switch record.Kind {
		case "type":
			types = reflect.DeepEqual(record.Candidates, []lookupCandidateRead{{"Local.Item", false, false}, {"Hidden.Item", true, false}, {"Public.Item", true, true}})
		case "enclosing":
			if record.LocalShadowed {
				shadowed = true
				if len(record.Candidates) != 0 {
					t.Fatal("shadowed read reached globals")
				}
			} else {
				enclosing = true
			}
		}
	}
	if !types || !enclosing || !shadowed {
		t.Fatalf("missing dependency domain: %+v", records)
	}
}
func TestLookupTraceFullAnalysisParityAndOwnership(t *testing.T) {
	source := `module M:
    def sibling(value: i64) -> i64:
        return value
    def caller(value: i64) -> i64:
        return sibling(value)
`
	cold := Analyze(parseIncrementalSessionFile(t, source))
	result, trace := AnalyzeWithLookupTrace(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	assertIncrementalResultParity(t, cold, result)
	if trace.Truncated || len(trace.Records) == 0 || trace.Reads == 0 {
		t.Fatal("missing actual function dependencies")
	}
	if result.analyzer.lookupTrace != nil {
		t.Fatal("recorder retained by result analyzer")
	}
	original := strings.Join(trace.Records, "\n")
	result.File.Decls = nil
	_, later := AnalyzeWithLookupTrace(parseIncrementalSessionFile(t, strings.ReplaceAll(source, "sibling", "renamed")), AnalyzeOptions{})
	if strings.Join(trace.Records, "\n") != original || reflect.DeepEqual(trace.Records, later.Records) {
		t.Fatal("trace ownership or change detection lost")
	}
}
func TestLookupTraceBoundsAndContext(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.lookupTrace.bytes = lookupTraceMaxBytes
	a.lookupVisibleGlobal("missing")
	if trace := a.lookupTrace.snapshot(); !trace.Truncated || trace.Dropped != 1 || len(trace.Records) != 0 {
		t.Fatal("byte limit did not fail closed")
	}
	a.lookupTrace = &lookupDependencyRecorder{}
	a.currentUsings = []string{strings.Repeat("x", lookupTraceMaxBytes)}
	a.lookupTrace.record(a, "value", "x", nil, false)
	if !a.lookupTrace.snapshot().Truncated {
		t.Fatal("oversized context admitted")
	}
	a.lookupTrace = &lookupDependencyRecorder{}
	a.currentUsings = nil
	a.lookupVisibleGlobal("missing")
	a.currentNamespace = "Other"
	a.lookupVisibleGlobal("missing")
	if len(a.lookupTrace.snapshot().Records) != 2 {
		t.Fatal("namespace context conflated")
	}
}

func TestLookupTraceFinalEffectOwnerDoesNotChangeResolutionContext(t *testing.T) {
	f := newPermissionGraphFixture(1)
	f.a.lookupTrace = &lookupDependencyRecorder{}
	f.a.permissions = map[string]*PermissionSet{"Grant": {Name: "Grant"}}
	f.functions[0].Body = []ast.Stmt{&ast.CanStmt{Permissions: []ast.PermissionRef{{Name: "Grant"}}}}
	if f.a.currentFuncDecl != nil || f.a.currentFuncType != nil {
		t.Fatal("fixture has primary function context")
	}
	f.a.inferFunctionPermissionEffectsWorklist(f.decls)
	found := false
	for _, record := range decodeLookupTrace(t, f.a.lookupTrace.snapshot()) {
		if record.Kind == "permission" && record.Request == "Grant" && record.Function == "f0" && record.Phase == "effects" {
			found = true
		}
	}
	if !found {
		t.Fatal("final inference lookup dropped its function owner")
	}
	if f.a.currentFuncDecl != nil || f.a.currentFuncType != nil || f.a.lookupTraceOwner != "" {
		t.Fatal("trace altered or retained function resolution context")
	}
}

func TestLookupTraceCandidateCollectionIsBounded(t *testing.T) {
	var candidates []lookupCandidateRead
	for i := 0; i < 2*lookupTraceMaxCandidates; i++ {
		candidates = appendLookupCandidate(candidates, lookupCandidateRead{Name: "missing"})
	}
	if len(candidates) != lookupTraceMaxCandidates+1 {
		t.Fatal("candidate prefix escaped bound")
	}
	a := lookupTraceAnalyzer()
	a.lookupTrace.record(a, "value", "missing", candidates, false)
	if got := a.lookupTrace.snapshot(); !got.Truncated || len(got.Records) != 0 {
		t.Fatal("incomplete candidate prefix published")
	}
}

func TestLookupTraceRejectsLossyJSONNames(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.lookupTrace.record(a, "value", string([]byte{255}), nil, false)
	if got := a.lookupTrace.snapshot(); !got.Truncated || len(got.Records) != 0 {
		t.Fatal("invalid UTF-8 silently replaced by JSON")
	}
}

func TestLookupTraceInternsAndCanonicalizesContexts(t *testing.T) {
	traces := make([]LookupDependencyTrace, 2)
	for order := 0; order < 2; order++ {
		a := lookupTraceAnalyzer()
		for step := 0; step < 2; step++ {
			n := step
			if order == 1 {
				n = 1 - step
			}
			a.currentNamespace = []string{"First", "Second"}[n]
			a.lookupVisibleGlobal("one")
			a.lookupVisibleGlobal("two")
		}
		traces[order] = a.lookupTrace.snapshot()
		if len(traces[order].Contexts) != 2 || len(traces[order].Records) != 4 {
			t.Fatal("context repeated in lookup payload")
		}
		decodeLookupTrace(t, traces[order])
		bytes := 0
		for _, text := range append(append([]string(nil), traces[order].Contexts...), traces[order].Records...) {
			bytes += len(text)
		}
		if bytes > a.lookupTrace.bytes || bytes > lookupTraceMaxBytes {
			t.Fatal("canonical snapshot escaped accounted storage")
		}
	}
	if !reflect.DeepEqual(traces[0], traces[1]) {
		t.Fatal("observation order changed context references")
	}
}
