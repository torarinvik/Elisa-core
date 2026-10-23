package semantic

import (
	"strings"
	"testing"
)

func TestRetainedPermissionReplacementRetractsInitiallyPresentCycleEffect(t *testing.T) {
	before := `def leaf(n: i64) -> i64 can[Abort.Panic, Console.Write]:
    return n + 1

def ping(n: i64) -> i64:
    return 0 if n <= 0 else pong(n - 1) + leaf(n)

def pong(n: i64) -> i64:
    return 0 if n <= 0 else ping(n - 1)

def top(n: i64) -> i64:
    return pong(n)
`
	after := strings.Replace(before, "can[Abort.Panic, Console.Write]", "can[Abort.Panic]", 1)
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	for step, source := range []string{after, before, after} {
		result, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if got, want := retainedDiagnostics(result), retainedDiagnostics(cold); got != want {
			t.Fatalf("step %d retained a retired cycle effect:\n%s\nwant:\n%s", step, got, want)
		}
		for _, name := range []string{"leaf", "ping", "pong", "top"} {
			got := result.GlobalScope.Symbols[name].Type.(*FuncType)
			want := cold.GlobalScope.Symbols[name].Type.(*FuncType)
			if !snapshotPermissionRow(want).same(got) {
				t.Fatalf("step %d wrong cycle row for %s", step, name)
			}
		}
	}
}

func TestRetainedPermissionsSignatureRemovalRoundTrip(t *testing.T) {
	_, state := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	signature := strings.Replace(retainedBefore, "def helper(x: i64)", "def helper(x: i64, spare: i64 = 0)", 1)
	shrunk := strings.Replace(signature, "    base: uintptr = (&v).cast[uintptr] can Unsafe.PointerCast\n", "", 1)
	removed := retainedBefore[strings.Index(retainedBefore, "def caller"):]
	for step, source := range []string{signature, shrunk, retainedBefore, removed, retainedBefore} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		result, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if got, want := retainedDiagnostics(result), retainedDiagnostics(cold); got != want {
			t.Fatalf("step %d differs from cold:\n%s\nwant:\n%s", step, got, want)
		}
		for name, symbol := range cold.GlobalScope.Symbols {
			want, ok := symbol.Type.(*FuncType)
			if !ok {
				continue
			}
			actual := result.GlobalScope.Symbols[name]
			if actual == nil {
				t.Fatalf("step %d lost callable %s", step, name)
			}
			got, ok := actual.Type.(*FuncType)
			if !ok || !snapshotPermissionRow(want).same(got) {
				t.Fatalf("step %d permission row differs for %s", step, name)
			}
		}
		if !state.perms.solvedIn {
			t.Fatalf("step %d did not reuse verified equations", step)
		}
	}
}

func TestRetainedPermissionReindexRejectsUnrefreshedRemovedReaders(t *testing.T) {
	_, state := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	before := permissionFunctionDecls(state.decls)
	var types []*FuncType
	known := map[*FuncType]bool{}
	for _, sd := range before {
		ft := permissionDeclType(state.a, sd)
		types = append(types, ft)
		known[ft] = true
	}
	// Removing helper without refreshing caller must not mutate the graph or
	// discard its validation cache. Real updates must rebind this call first.
	if _, ok := state.perms.reindexedFunctions(state.a, before, types, known, before[1:], nil); ok {
		t.Fatal("retired callable admitted with an unchanged reader")
	}
	if state.perms.diags == nil || len(state.perms.rows) == 0 {
		t.Fatal("rejected graph change modified cached state")
	}
	// Reordering surviving equations is safe when their identities and scopes
	// agree. Every old edge must then point at its reader's new index.
	swapped := append([]scopedDecl(nil), before...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if _, ok := state.perms.reindexedFunctions(state.a, before, types, known, swapped, nil); !ok {
		t.Fatal("stable surviving equations could not be reindexed")
	}
	if !state.perms.readers[types[0]][0] {
		t.Fatal("caller edge did not follow its new equation index")
	}
	if state.perms.diags != nil {
		t.Fatal("general graph change did not require full validation")
	}
}
