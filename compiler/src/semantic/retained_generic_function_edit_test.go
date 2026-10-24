package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"strings"
	"testing"
)

func TestRetainedGenericFunctionBinderDiskRoundTrip(t *testing.T) {
	const base = `module Helpers:
    public:
        def first[T](value: T) -> T:
            return value

def relay[T](value: T) -> T:
    return Helpers::first[T](value)

def invoke() -> i64:
    callback: fn(i64) -> i64 = Helpers::first[i64]
    return relay[i64](41) + callback(1)

def unrelated() -> i64:
    return 9
`
	renamed := strings.Replace(base, "first[T](value: T) -> T:", "first[Payload](value: Payload) -> Payload:", 1)
	invalid := strings.Replace(renamed, "relay[i64](41)", "relay[i64](true)", 1)
	initial, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	for _, source := range []string{renamed, base, invalid, renamed, base} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		old, _ := state.a.globalScope.Lookup("Helpers.first")
		oldBinder := old.Type.(*FuncType).TypeParams[0]
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(cold) {
			t.Fatalf("diagnostics differ\ngot:%s\nwant:%s", retainedDiagnostics(got), retainedDiagnostics(cold))
		}
		if source == invalid && len(cold.Errors()) == 0 {
			t.Fatal("invalid caller accepted")
		}
		if source != invalid && len(got.Errors()) != 0 {
			t.Fatal(got.Errors())
		}
		current, _ := got.GlobalScope.Lookup("Helpers.first")
		if current == nil {
			t.Fatal("generic declaration missing")
		}
		replacement := oldBinder != current.Type.(*FuncType).TypeParams[0]
		if replacement {
			if current == old || state.LastStats.Added != 1 || state.LastStats.Removed != 1 {
				t.Fatalf("replacement stats: %+v", state.LastStats)
			}
		} else if current != old || state.LastStats.Added != 0 || state.LastStats.Removed != 0 {
			t.Fatal("caller repair replaced unchanged generic declaration")
		}

	}
}

func TestRetainedGenericFunctionBinderProtocolGuards(t *testing.T) {
	parse := func(source string) *ast.FuncDecl { return retainedParse(t, source).Decls[0].(*ast.FuncDecl) }
	base := "def first[T](value: T) -> T:\n    return value\n"
	renamed := "def first[Payload](value: Payload) -> Payload:\n    return value\n"
	old := parse(base)
	if !retainedGenericFunctionBinderRename(old, parse(renamed)) {
		t.Fatal("name-only change rejected")
	}
	for _, source := range []string{
		strings.Replace(renamed, "first[Payload]", "other[Payload]", 1),
		strings.Replace(renamed, "value: Payload", "input: Payload", 1),
		strings.Replace(renamed, "-> Payload:", "-> bool:", 1),
		strings.Replace(renamed, "[Payload]", "[Payload, Other]", 1),
		strings.Replace(renamed, "[Payload]", "[Payload: Mark]", 1),
		strings.Replace(renamed, "[Payload]", "[Payload: usize]", 1),
	} {
		if retainedGenericFunctionBinderRename(old, parse(source)) {
			t.Fatalf("changed protocol accepted: %s", source)
		}
	}
	before := parse("def first[T](value: Other::T) -> Other::T:\n    return value\n")
	after := parse("def first[Payload](value: Other::Payload) -> Other::Payload:\n    return value\n")
	if retainedGenericFunctionBinderRename(before, after) {
		t.Fatal("qualified member was mistaken for a binder")
	}
	beforeDefault := parse("def first[T](value: i64 = T) -> i64:\n    return value\n")
	afterDefault := parse("def first[Payload](value: i64 = Payload) -> i64:\n    return value\n")
	if retainedGenericFunctionBinderRename(beforeDefault, afterDefault) {
		t.Fatal("default value was mistaken for type syntax")
	}

	region := parse("def first[T, @r](value: T) -> T:\n    return value\n")
	regionRename := parse("def first[Payload, @s](value: Payload) -> Payload:\n    return value\n")
	if retainedGenericFunctionBinderRename(region, regionRename) {
		t.Fatal("region rename accepted")
	}
	for _, tail := range []string{
		"global callback: fn(i64) -> i64 = first[i64]\n",
		"def caller(callback: fn(i64) -> i64 = first[i64]) -> i64:\n    return callback(7)\n",
	} {
		file := retainedParse(t, base+"\n"+tail)
		if !retainedGenericFunctionHasNonBodyUsers(retainedWalkDecls(file), file.Decls[0].(*ast.FuncDecl)) {
			t.Fatal("non-body consumer escaped guard")
		}
	}
}

func TestRetainedGenericFunctionBinderKeepsBoundsAndEffects(t *testing.T) {
	const base = `protocol Mark:
    type Item
impl Mark for i64:
    type Item = i64

def first[T: Mark](value: T) -> T can[Abort.Panic]:
    return value

def invoke() -> i64 can[Abort.Panic]:
    return first[i64](42)
`
	renamed := strings.Replace(base, "first[T: Mark](value: T) -> T", "first[Payload: Mark](value: Payload) -> Payload", 1)
	initial, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	for _, source := range []string{renamed, base, renamed} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(cold) || len(got.Errors()) != 0 {
			t.Fatalf("bound/effect parity: %v", got.Errors())
		}
		actual, _ := got.GlobalScope.Lookup("first")
		want, _ := cold.GlobalScope.Lookup("first")
		actualType, wantType := actual.Type.(*FuncType), want.Type.(*FuncType)
		if !reflect.DeepEqual(actualType.GenericParams, wantType.GenericParams) || !reflect.DeepEqual(actualType.Permissions, wantType.Permissions) {
			t.Fatal("generic bounds or effect row stale")
		}
	}
}
