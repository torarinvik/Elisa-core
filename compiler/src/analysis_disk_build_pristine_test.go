package main

import (
	"io"
	"strings"
	"testing"
	"unsafe"

	"elisacore/src/ast"
)

func TestAnalysisBuildPristineReusesTokensWithoutSharingSyntax(t *testing.T) {
	program := &loadedProgram{filename: "pristine.elisa", source: []byte("def value() -> i64:\n    return 21\n"), tokenCache: newRequestSourceTokenCache()}
	primary, ok := parseProgramWithTokenCache(program.filename, program.source, io.Discard, program.tokenCache)
	if !ok || program.tokenCache.requestLexed == nil {
		t.Fatal("primary parse did not populate request tokens")
	}
	pristine, ok := parseAnalysisBuildPristine(program)
	if !ok || pristine == primary {
		t.Fatal("pristine parse must produce an independent syntax tree")
	}
	first, firstOK := primary.Decls[0].(*ast.FuncDecl)
	second, secondOK := pristine.Decls[0].(*ast.FuncDecl)
	if !firstOK || !secondOK || first == second || first.Name != "value" || second.Name != first.Name {
		t.Fatal("unexpected function declarations")
	}
	if unsafe.StringData(first.Name) != unsafe.StringData(second.Name) {
		t.Fatal("pristine parse did not reuse the primary token spelling")
	}
	first.Name = "changed"
	if second.Name != "value" {
		t.Fatal("semantic mutation leaked into pristine syntax")
	}
}

// Benchmark the second parse in a cold bootstrap after its primary parse has
// populated the request token cache. The uncached case is the prior behavior.
func BenchmarkAnalysisBuildPristine(b *testing.B) {
	source := []byte(strings.Repeat("def calculate(x: i64) -> i64:\n    return x * 2 + 1\n\n", 10000))
	program := &loadedProgram{filename: "benchmark.elisa", source: source, tokenCache: newRequestSourceTokenCache()}
	if _, ok := parseProgramWithTokenCache(program.filename, source, io.Discard, program.tokenCache); !ok {
		b.Fatal("primary parse failed")
	}
	for _, mode := range []string{"separate-lex", "reuse-tokens"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			for i := 0; i < b.N; i++ {
				var ok bool
				if mode == "separate-lex" {
					_, ok = parseProgram(program.filename, source, io.Discard)
				} else {
					_, ok = parseAnalysisBuildPristine(program)
				}
				if !ok {
					b.Fatal("pristine parse failed")
				}
			}
		})
	}
}
