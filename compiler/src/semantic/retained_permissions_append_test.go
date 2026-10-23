package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestRetainedPermissionsAppendPreservesRowsAndValidatesNewBodies(t *testing.T) {
	_, state := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	first := retainedBefore + "\ndef extra_plain(x: i64) -> i64:\n    return x + 1\n"
	second := first + "\ndef extra_unsafe(z: i64) -> i64:\n    v: mutable i64 = z\n    address: uintptr = (&v).cast[uintptr] can Unsafe.PointerCast\n    return z\n"
	caller := strings.Replace(second, "def caller(x: i64) -> i64:\n    return helper(x)", "def caller(x: i64) -> i64:\n    return extra_unsafe(x)", 1)
	for i, source := range []string{first, second, caller, retainedBefore} {
		result, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if got, want := retainedDiagnostics(result), retainedDiagnostics(cold); got != want {
			t.Fatalf("step %d differs from cold:\n%s\nwant:\n%s", i, got, want)
		}
		if i < 2 {
			if !state.perms.solvedIn {
				t.Fatal("plain append unnecessarily rebuilt permission graph")
			}
			if len(state.perms.changed) != 0 {
				t.Fatal("a fresh unused row invalidated old validation")
			}
		}
		if i == 2 && !strings.Contains(retainedDiagnostics(result), `call to "extra_unsafe" requires can[Unsafe]`) {
			t.Fatal("newly referenced function was not validated")
		}
		if i == 3 && !state.perms.solvedIn {
			t.Fatal("verified removal unnecessarily rebuilt the equation graph")
		}
	}
}

func TestRetainedPermissionsAppendRepairsUndefinedCallee(t *testing.T) {
	before := "def caller(x: i64) -> i64:\n    return added(x)\n\ndef unrelated(x: i64) -> i64:\n    return x\n"
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	after := before + "\ndef added(z: i64) -> i64:\n    v: mutable i64 = z\n    address: uintptr = (&v).cast[uintptr] can Unsafe.PointerCast\n    return z\n"
	result, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	if got, want := retainedDiagnostics(result), retainedDiagnostics(cold); got != want {
		t.Fatalf("added callee differs from cold:\n%s\nwant:\n%s", got, want)
	}
	if !state.perms.solvedIn {
		t.Fatal("new callee missed append-only solver")
	}
	if len(state.perms.changed) == 0 {
		t.Fatal("changed existing caller row did not invalidate validation")
	}
	if !strings.Contains(retainedDiagnostics(result), `call to "added" requires can[Unsafe]`) {
		t.Fatal("new effect did not reach the rebound caller")
	}
}

func TestRetainedPermissionAppendRejectsIndexShiftAndTypeReuse(t *testing.T) {
	before := "def first(x: i64) -> i64:\n    return x\n\ndef second(x: i64) -> i64:\n    return x\n"
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	old := permissionFunctionDecls(state.decls)
	var oldTypes []*FuncType
	known := map[*FuncType]bool{}
	for _, sd := range old {
		ft := permissionDeclType(state.a, sd)
		oldTypes = append(oldTypes, ft)
		known[ft] = true
	}
	added := &ast.FuncDecl{Name: "fresh"}
	fresh := &FuncType{Name: "fresh"}
	state.a.funcDeclSymbols[added] = &Symbol{Name: "fresh", Type: fresh, Node: added}
	current := append(append([]scopedDecl(nil), old...), scopedDecl{Decl: added})
	changed := current[len(old):]
	verified, ok := state.perms.appendedFunctions(state.a, old, oldTypes, known, current, changed)
	if !ok {
		t.Fatal("fixture did not establish safe append")
	}
	swapped := append([]scopedDecl(nil), current...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if _, ok := state.perms.appendedFunctions(state.a, old, oldTypes, known, swapped, changed); ok {
		t.Fatal("shifted equation indexes admitted")
	}
	// A callable can predate the new declaration without publishing an equation,
	// as with an extern or an alias. It is not a fresh validation dependency.
	external := &FuncType{Name: "external"}
	known[external] = true
	state.a.funcDeclSymbols[added].Type = external
	if _, ok := state.perms.appendedFunctions(state.a, old, oldTypes, known, current, changed); ok {
		t.Fatal("pre-existing callable type treated as fresh")
	}
	state.perms.beginUpdate(changed, true, nil)
	if state.perms.solveIncremental(state.a, current, verified) {
		t.Fatal("body analysis retargeted a verified fresh function type")
	}
}
