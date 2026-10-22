package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strings"
	"testing"
)

func BenchmarkStoreScanCallsWithoutTarget(b *testing.B) {
	source := "struct Table:\n    names: mutable darray[sview] = []\ndef observe(names: darray[sview]&):\n    pass\ndef scan(table: mutable Table&, names: darray[sview]&):\n" + strings.Repeat("    observe(names)\n", 128)
	l := lexer.New("untargeted_calls.elisa", []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("untargeted_calls.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal("invalid benchmark fixture")
	}
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
	storeScanUntyped(result)
	a := result.analyzer
	a.currentScope = a.globalScope
	var fn *ast.FuncDecl
	for decl := range a.funcDeclSymbols {
		if decl.Name == "scan" {
			fn = decl
		}
	}
	if fn == nil {
		b.Fatal("missing scan declaration")
	}
	// Warm declaration indexes and taint facts: measure the directed node scan,
	// which is repeated for different source/target queries in recursive graphs.
	if mode := a.calleeStoreModeIntoBody(fn, 1, 0, map[storeScanKey]bool{}); mode != storeNone {
		b.Fatal(mode)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if mode := a.calleeStoreModeIntoBody(fn, 1, 0, map[storeScanKey]bool{}); mode != storeNone {
			b.Fatal(mode)
		}
	}
}
