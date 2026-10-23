package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

func retainedCodegenParse(t *testing.T, src string) *ast.File {
	t.Helper()
	l := lexer.New("retained.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("retained.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return file
}

var retainedGensym = regexp.MustCompile(`__([a-z_]+)_[0-9]+`)

func retainedIR(t *testing.T, r *semantic.Result) string {
	t.Helper()
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("analysis errors: %v", errs)
	}
	ir, err := backend.GenerateLLVMIRWithOptAndPackedLoweringProfile(r, backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	return retainedGensym.ReplaceAllString(ir, "__${1}_N")
}

const retainedCodegenBase = `
module Lib:
    struct Pair:
        a: i64
        b: i64

    def contains(xs: sview, x: u8) -> bool:
        for v in xs:
            if v == x:
                return true
        return false

    def at(b: u8, n: usize, i: usize) -> u8:
        requires i < n
        ensure result == b
        return b

    def width(p: Pair) -> i64:
        return p.a + p.b

    def collect(xs: mutable darray[Pair]&, c: bool) -> i64:
        xs <- xs.push(Pair{a: 1, b: 2}) if c
        seen: bool = any q in xs where q.b == 2
        return 1 if seen else 0

    def record(xs: mutable darray[Pair]&, c: bool) -> void:
        xs <- xs.push(Pair{a: 1, b: 2}) if c

    def tag(x: i64) -> i64 can[Abort.Panic]:
        return x

def main() -> i32:
    xs: sview = "abc"
    p: Lib::Pair = Lib::Pair{a: 1, b: 2}
    if Lib::contains(xs, 98) and Lib::at(98, 3, 1) == 98:
        return i32(Lib::width(p) + Lib::tag(1))
    return 0
`

// Code generated from an updated retained analysis equals code from a fresh one.
func TestRetainedUpdateGeneratesFreshIR(t *testing.T) {
	edits := map[string][2]string{
		"overload":    {"    def width(p: Pair) -> i64:\n", "    def width(p: bool) -> i64:\n        return 1\n\n    def width(p: Pair) -> i64:\n"},
		"sig":         {"def tag(x: i64) -> i64 can[Abort.Panic]:\n        return x", "def tag(x: i64) -> i32 can[Abort.Panic]:\n        return 1"},
		"can_row":     {"can[Abort.Panic]:", "can[Abort.Panic, Console.Write]:"},
		"contract":    {"ensure result == b\n", "ensure result == b and i < n\n"},
		"field":       {"        a: i64\n        b: i64\n", "        a: i64\n        z: i64 = 0\n        b: i64\n"},
		"field_first": {"    struct Pair:\n", "    struct Pair:\n        z: i64 = 0\n"},
		"body":        {"return p.a + p.b", "return p.b + p.a"},
		"fn_remove":   {"    def tag(x: i64) -> i64 can[Abort.Panic]:\n        return x\n", "    def tag(x: i64) -> i64 can[Abort.Panic]:\n        return x\n\n    def unused(x: i64) -> i64:\n        return x\n"},
	}
	for name, e := range edits {
		t.Run(name, func(t *testing.T) {
			edited := strings.Replace(retainedCodegenBase, e[0], e[1], 1)
			if edited == retainedCodegenBase {
				t.Fatal("fixture: edit did not apply")
			}
			_, retained := semantic.AnalyzeRetained(retainedCodegenParse(t, retainedCodegenBase), retainedCodegenParse(t, retainedCodegenBase), []byte(retainedCodegenBase), semantic.AnalyzeOptions{})
			for i, src := range []string{edited, retainedCodegenBase, edited} {
				updated, err := retained.Update(retainedCodegenParse(t, src), retainedCodegenParse(t, src), []byte(src))
				if err != nil {
					t.Logf("step %d fell back: %v", i, err)
					continue // a fallback analyzes fresh, so codegen sees a fresh analysis
				}
				got := retainedIR(t, updated)
				want := retainedIR(t, semantic.AnalyzeWithOptions(retainedCodegenParse(t, src), semantic.AnalyzeOptions{}))
				if got != want {
					t.Fatalf("step %d: IR differs\n%s", i, firstLineDiff(got, want))
				}
			}
		})
	}
}

func firstLineDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return "line " + strconv.Itoa(i+1) + "\n got: " + gl + "\nwant: " + wl
		}
	}
	return ""
}
