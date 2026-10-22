package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
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

func BenchmarkStoreScanTypedAggregateArguments(b *testing.B) {
	source := "struct Table:\n    names: mutable darray[sview] = []\nenum Payload:\n"
	for i := 0; i < 64; i++ {
		source += fmt.Sprintf("    Names%d(values: darray[sview])\n", i)
	}
	source += "def observe(table: mutable Table&, value: Payload&):\n    pass\ndef scan(table: mutable Table&, value: Payload&):\n" + strings.Repeat("    observe(table, value)\n", 128)
	l := lexer.New("typed_store_calls.elisa", []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("typed_store_calls.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal("invalid benchmark fixture")
	}
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
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
