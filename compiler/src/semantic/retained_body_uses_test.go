package semantic

import (
	"bytes"
	"elisacore/src/ast"
	"os"
	"strings"
	"testing"
)

func TestRetainedBodyUsesSnapshotQueries(t *testing.T) {
	source := `struct Box:
    value: i64

def make() -> Box:
    return Box{value: 7}

def reads() -> i64:
    return make().value

def unresolved() -> i64:
    return later()

def defaulted(x: i64 = DEFAULT_VALUE) -> i64:
    ensure result == contract_value(x)
    return x
`
	_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	fns := map[string]*ast.FuncDecl{}
	for _, sd := range state.decls {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			fns[fn.Name] = fn
		}
	}
	for _, tc := range []struct {
		fn, name string
		target   Type
		want     bool
	}{
		{"reads", "", state.a.namedTypes["Box"], true},
		{"unresolved", "later", nil, true},
		{"defaulted", "DEFAULT_VALUE", nil, true},
		{"defaulted", "contract_value", nil, true},
		{"make", "absent", nil, false},
	} {
		if _, ok := state.bodyUses[fns[tc.fn]]; !ok {
			t.Fatal("missing snapshot dependency entry", tc.fn)
		}
		targets, names := map[Type]bool{}, map[string]bool{}
		if tc.target != nil {
			targets[tc.target] = true
		}
		if tc.name != "" {
			names[tc.name] = true
		}
		cached := state.a.retainedUsesTypes(fns[tc.fn], targets, names, map[Type]bool{})
		saved := state.bodyUses
		state.bodyUses = nil
		uncached := state.a.retainedUsesTypes(fns[tc.fn], targets, names, map[Type]bool{})
		state.bodyUses = saved
		if cached != tc.want || uncached != tc.want {
			t.Fatalf("%s/%s: cached=%v walk=%v want=%v", tc.fn, tc.name, cached, uncached, tc.want)
		}
	}
}

func TestRetainedBodyUsesInvalidatesAfterBodyAndLayoutEdits(t *testing.T) {
	before := `struct Pair:
    x: i64

def make() -> Pair:
    return Pair{x: 1}

def user() -> i64:
    return 0
`
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	reading := strings.Replace(before, "    return 0\n", "    return make().x\n", 1)
	renamed := strings.Replace(strings.Replace(reading, "    x: i64", "    y: i64", 1), "Pair{x: 1}", "Pair{y: 1}", 1)
	for i, source := range []string{reading, renamed, reading} {
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if state.bodyUses != nil {
			t.Fatal("update retained dependencies of pre-edit bodies")
		}
		want := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("step %d: stale body dependencies\n%s\nwant:\n%s", i, retainedDiagnostics(got), retainedDiagnostics(want))
		}
		if i == 1 {
			if len(want.Errors()) == 0 {
				t.Fatal("removed field must invalidate the new user")
			}
			data, err = state.MarshalState()
			if err != nil {
				t.Fatal(err)
			}
			state, err = RestoreAnalysisState(data)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

// This opt-in oracle reads an admitted compiler snapshot without emitting code.
// The ordinary test run uses small fixtures; qualification supplies the real
// self-build snapshot and checks every function against the original AST walk.
func TestRetainedBodyUsesLargeSnapshotOracle(t *testing.T) {
	path := os.Getenv("ELISACORE_STATE_QUALIFY_BODY_USES")
	if path == "" {
		t.Skip("set ELISACORE_STATE_QUALIFY_BODY_USES to an admitted snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := bytes.SplitN(data, []byte("\n"), 3)
	if len(parts) != 3 || string(parts[0]) != "ELISACSTATE1" || len(parts[1]) != 64 {
		t.Fatal("not an admitted snapshot envelope")
	}
	state, err := RestoreAnalysisState(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(state.bodyUses) == 0 {
		t.Fatal("snapshot has no body dependency index")
	}
	for _, name := range []string{"IncrementalCacheRead", "CliArgumentKind"} {
		target, ok := state.a.namedTypes[name]
		if !ok {
			t.Fatal("qualification target missing", name)
		}
		targets, names := map[Type]bool{target: true}, map[string]bool{name: true}
		state.a.widenLayoutTargets(targets)
		cachedMemo, walkMemo := map[Type]bool{}, map[Type]bool{}
		count := 0
		for _, sd := range state.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if _, ok := state.bodyUses[fn]; !ok {
				t.Fatalf("function %s has no persisted body dependencies", fn.Name)
			}
			cached := state.a.retainedUsesTypes(fn, targets, names, cachedMemo)
			saved := state.bodyUses
			state.bodyUses = nil
			walked := state.a.retainedUsesTypes(fn, targets, names, walkMemo)
			state.bodyUses = saved
			if cached != walked {
				t.Fatalf("%s / %s at %v: cached=%v walk=%v", name, fn.Name, fn.Pos(), cached, walked)
			}
			count++
		}
		t.Logf("%s: checked %d functions against the full AST walk", name, count)
	}
}
