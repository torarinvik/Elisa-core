//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func enableRetainedFastPrepareForTest(t *testing.T) {
	t.Helper()
	previous := retainedFastPrepareMode
	retainedFastPrepareMode = "on"
	t.Cleanup(func() { retainedFastPrepareMode = previous })
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	t.Setenv("ELISACORE_RETAINED_FAST_DEBUG", "0")
}

func reloadRetainedFastPrepareForTest(t *testing.T, state *RetainedAnalysis) *RetainedAnalysis {
	t.Helper()
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestRetainedFastPrepareInvalidatesAfterDeclarationEditAcrossDisk(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	const base = `struct Pair:
    value: u32

def calculate(value: i64) -> i64:
    return value + 1
`
	initial, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	if len(initial.Errors()) != 0 || state.classify == nil || state.appendOnly == nil {
		t.Fatalf("fixture has no fast baseline: %v", initial.Errors())
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	if state.tables == nil || len(state.digestOf) == 0 {
		t.Fatal("fixture did not populate baseline caches")
	}
	// Same line count: invalidation must recognize the declaration mutation,
	// rather than relying only on moved source coordinates.
	declarationEdit := strings.Replace(base, "value: u32", "value: u64", 1)
	warm, err := state.Update(retainedParse(t, declarationEdit), retainedParse(t, declarationEdit), []byte(declarationEdit))
	if err != nil {
		t.Fatal(err)
	}
	if state.fastPrepareInvalidated == "" || state.classify != nil || state.appendOnly != nil || state.tables != nil || state.digestOf != nil {
		t.Fatal("declaration update retained stale fast-preparation caches")
	}
	assertRetainedCanonicalLiveResultParity(t, AnalyzeWithOptions(retainedParse(t, declarationEdit), AnalyzeOptions{}), warm)
	state = reloadRetainedFastPrepareForTest(t, state)
	if state.fastPrepareInvalidated == "" || state.classify != nil || state.appendOnly != nil || state.tables != nil || state.digestOf != nil {
		t.Fatal("disk restore silently re-enabled invalidated fast preparation")
	}
	bodyEdit := strings.Replace(declarationEdit, "value + 1", "value + 2", 1)
	warm, err = state.Update(retainedParse(t, bodyEdit), retainedParse(t, bodyEdit), []byte(bodyEdit))
	if err != nil {
		t.Fatal(err)
	}
	if state.LastStats.FastPrepared || !strings.Contains(state.LastStats.FastDeclined, "fresh retained baseline required") {
		t.Fatalf("stale baseline used after declaration edit: %+v", state.LastStats)
	}
	cold := AnalyzeWithOptions(retainedParse(t, bodyEdit), AnalyzeOptions{})
	if retainedDiagnostics(warm) != retainedDiagnostics(cold) {
		t.Fatal("scratch-backed retained update changed diagnostics")
	}
	assertRetainedCanonicalLiveResultParity(t, cold, warm)
	// The conservative intermediate invalidation is not a permanent feature
	// switch: independently analyzing the new program qualifies a fresh baseline.
	_, fresh := AnalyzeRetained(retainedParse(t, bodyEdit), retainedParse(t, bodyEdit), []byte(bodyEdit), AnalyzeOptions{})
	fresh = reloadRetainedFastPrepareForTest(t, fresh)
	next := strings.Replace(bodyEdit, "value + 2", "value + 3", 1)
	if _, err := fresh.Update(retainedParse(t, next), retainedParse(t, next), []byte(next)); err != nil {
		t.Fatal(err)
	}
	if !fresh.LastStats.FastPrepared || fresh.fastPrepareInvalidated != "" {
		t.Fatalf("fresh analysis did not requalify fast preparation: %+v", fresh.LastStats)
	}
}

// An explicit Arena scope uses the same ownership predicate as RegionStmt,
// without introducing temporary region-wrapper Idents outside the source AST.
// This isolates ownership admission while retaining the strict live-fact oracle.
func TestRetainedFastPrepareDigestIncludesRegionOwnership(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	const base = `def calculate(arena: mutable Arena&) -> i64:
    if true:
        _ = 0
    return 1
`
	after := strings.Replace(base, "if true:", "in arena:", 1)
	_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	state = reloadRetainedFastPrepareForTest(t, state)
	live := state.decls[0].Decl.(*ast.FuncDecl)
	old := retainedParse(t, base).Decls[0].(*ast.FuncDecl)
	edit := retainedParse(t, after).Decls[0].(*ast.FuncDecl)
	if state.a.funcOwnsRegion(old) || !state.a.funcOwnsRegion(edit) {
		t.Fatal("fixture does not flip region ownership")
	}
	sd := state.decls[0]
	beforeDigest := state.localFactDigest(state.a, live, old, sd)
	afterDigest := state.localFactDigest(state.a, live, edit, sd)
	if beforeDigest == "" || afterDigest == "" || beforeDigest == afterDigest {
		t.Fatal("digest failed to distinguish region ownership with unchanged scalar returns and no calls/allocations")
	}
	warm, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		if !strings.Contains(err.Error(), "preparation") {
			t.Fatal(err)
		}
		// The complete declaration-phase certificate now rejects a changed
		// ownership decision before mutation; the driver rebuilds this source.
		warm = AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	}
	if state.LastStats.FastPrepared || !strings.Contains(state.LastStats.FastDeclined, "declaration-level facts") {
		t.Fatalf("ownership flip bypassed scratch: %+v", state.LastStats)
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	if retainedDiagnostics(warm) != retainedDiagnostics(cold) {
		t.Fatal("ownership flip diagnostic mismatch")
	}
	assertRetainedCanonicalLiveResultParity(t, cold, warm)
}

func TestRetainedFastPrepareDropsHistoricalDigestASTs(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	const base = `def calculate(value: i64) -> i64:
    return value + 1
`
	_, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	current := base
	for _, next := range []string{
		strings.Replace(current, "value + 1", "value + 2", 1),
		strings.Replace(strings.Replace(current, "value + 1", "value + 2", 1), "value + 2", "value + 3", 1),
	} {
		if next == current {
			t.Fatal("body edit did not apply")
		}
		if _, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next)); err != nil {
			t.Fatal(err)
		}
		if !state.LastStats.FastPrepared {
			t.Fatalf("body edit did not use fast preparation: %+v", state.LastStats)
		}
		current = next

		baseline := map[*ast.FuncDecl]bool{}
		for _, decl := range retainedWalkDecls(state.pristine) {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				baseline[fn] = true
			}
		}
		for fn := range state.digestOf {
			if !baseline[fn] {
				t.Fatal("fast-preparation digest retains a historical declaration")
			}
		}
	}

	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	baseline := map[*ast.FuncDecl]bool{}
	for _, decl := range retainedWalkDecls(restored.pristine) {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			baseline[fn] = true
		}
	}
	for fn := range restored.digestOf {
		if !baseline[fn] {
			t.Fatal("snapshot retained a digest keyed by a historical declaration")
		}
		if len(fn.Body) != 0 {
			t.Fatal("snapshot retained a digest declaration body")
		}
	}
}
