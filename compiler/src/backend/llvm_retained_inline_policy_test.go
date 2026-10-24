//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"testing"
)

func TestRetainedInlinePolicyDiskRoundTrip(t *testing.T) {
	const base = `def leaf(value: i64) -> i64:
    return value + 7

def compute(value: i64) -> i64:
    return leaf(value) + 1
export fn compute(value: i64) -> i64 = compute
`
	parse := func(source string) *ast.File {
		l := lexer.New("inline-policy-edit.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("inline-policy-edit.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("parse fixture")
		}
		return f
	}
	initial, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	for _, annotation := range []string{"@inline(never)\n", "@inline(always)\n", "", "@inline(never)\n"} {
		source := annotation + base
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = semantic.RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		got, err := state.Update(parse(source), parse(source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Errors()) != 0 {
			t.Fatal(got.Errors())
		}
		cold := parseAndAnalyzeBackendTest(t, "inline-policy-edit.elisa", source)
		current, ok := got.GlobalScope.Lookup("leaf")
		if !ok {
			t.Fatal("missing leaf")
		}
		expected, ok := cold.GlobalScope.Lookup("leaf")
		if !ok {
			t.Fatal("missing cold leaf")
		}
		a, b := current.Type.(*semantic.FuncType), expected.Type.(*semantic.FuncType)
		if a.HasInlineMode != b.HasInlineMode || a.InlineMode != b.InlineMode {
			t.Fatalf("stale metadata after %q", annotation)
		}
		for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3} {
			for _, debug := range []bool{false, true} {
				replayIR, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(got, level, DefaultPackedLoweringProfile(), "", debug)
				if err != nil {
					t.Fatal(err)
				}
				coldIR, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(cold, level, DefaultPackedLoweringProfile(), "", debug)
				if err != nil {
					t.Fatal(err)
				}
				if replayIR != coldIR {
					t.Fatalf("annotation=%q level=%v debug=%v stale policy IR", annotation, level, debug)
				}
			}
		}
	}
}
