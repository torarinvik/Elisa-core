package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strconv"
	"testing"
)

func BenchmarkStorageViewRecursiveSummary(b *testing.B) {
	source := []byte("struct Pair:\n    left: mutable darray[u8] = []\ndef first(pair: mutable Pair&) -> void:\n    second(pair)\ndef second(pair: mutable Pair&) -> void:\n    if pair.left.count > 0:\n        first(pair)\n    pair.left.clear()\n")
	l := lexer.New("recursive.elisa", source)
	p := parser.New(l.Tokenize())
	file := p.ParseFile("recursive.elisa")
	result := AnalyzeWithOptions(file, AnalyzeOptions{})
	if len(l.Errors())+len(p.Errors()) != 0 {
		b.Fatal("invalid fixture")
	}
	a := result.analyzer
	a.currentScope = a.globalScope
	var decl *ast.FuncDecl
	for candidate := range a.funcDeclSymbols {
		if candidate.Name == "first" {
			decl = candidate
		}
	}
	if decl == nil {
		b.Fatal("missing declaration")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.storageViewCalleeWriteCache = nil
		if got := a.storageViewCalleeWritesFor([]*ast.FuncDecl{decl}, 0); got.All || !got.Fields["left"] {
			b.Fatal("wrong summary")
		}
	}
}

func BenchmarkStorageViewWriteCacheHit(b *testing.B) {
	decl := &ast.FuncDecl{Name: "parser_step", Params: []ast.ParamDecl{{Name: "parser", Mutable: true}}}
	fields := map[string]bool{}
	for i := 0; i < 32; i++ {
		fields["field_"+strconv.Itoa(i)] = true
	}
	a := &Analyzer{storageViewCalleeWriteCache: map[storageViewCalleeWriteKey]storageViewCalleeWrites{
		{decl: decl, param: 0}: {Fields: fields},
	}}
	decls := []*ast.FuncDecl{decl}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := a.storageViewCalleeWritesFor(decls, 0); !got.Fields["field_0"] || len(got.Fields) != 32 {
			b.Fatal("cached write summary changed")
		}
	}
}
