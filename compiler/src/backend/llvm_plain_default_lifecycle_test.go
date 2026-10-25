//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestPlainDefaultLifecycleSharedObserverRemovalHasExactColdO0IR(t *testing.T) {
	const prefix = `global values: i32[2] = [11, 22]
global cursor: usize = 0
`
	const first = `def first(value: i32 = values[cursor]) -> i32:
    return value
`
	const second = `def second(value: i32 = values[cursor]) -> i32:
    return value
`
	before, after := prefix+first+second, prefix+second
	parse := func(source string) *ast.File {
		return parseRetainedDisjointCacheSource(t, "plain-default-lifecycle.elisa", source)
	}
	initial, state := semantic.AnalyzeRetained(parse(before), parse(before), []byte(before), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	warm, err := state.Update(parse(after), parse(after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	cold := semantic.AnalyzeWithOptions(parse(after), semantic.AnalyzeOptions{})
	warmIR, coldIR := retainedDisjointIR(t, warm), retainedDisjointIR(t, cold)
	if warmIR != coldIR {
		t.Fatalf("retained removal IR differs without normalization\nwarm:\n%s\ncold:\n%s", warmIR, coldIR)
	}
}

func TestPlainDefaultLifecycleRetiredBodyThenLastObserverRemovalHasExactColdO0IR(t *testing.T) {
	const prefix = `global values: i32[2] = [11, 22]
global cursor: usize = 0
def keep() -> i32:
    return 1
`
	const first = `def first(value: i32 = values[cursor]) -> i32:
    return value
`
	const replacement = `def first(value: i32 = values[cursor]) -> i32:
    return 7
`
	parse := func(source string) *ast.File {
		return parseRetainedDisjointCacheSource(t, "plain-default-retirement.elisa", source)
	}
	initial, state := semantic.AnalyzeRetained(parse(prefix+first), parse(prefix+first), []byte(prefix+first), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	for _, source := range []string{prefix + replacement, prefix, prefix + first} {
		warm, err := state.Update(parse(source), parse(source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := semantic.AnalyzeWithOptions(parse(source), semantic.AnalyzeOptions{})
		warmIR, coldIR := retainedDisjointIR(t, warm), retainedDisjointIR(t, cold)
		if warmIR != coldIR {
			t.Fatalf("retired-body/last-observer O0 IR differs without normalization\nwarm:\n%s\ncold:\n%s", warmIR, coldIR)
		}
	}
}
