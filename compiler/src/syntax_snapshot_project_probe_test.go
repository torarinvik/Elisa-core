package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"os"
	"runtime"
	"sort"
	"testing"
)

// Parse-only local probe: diagnose snapshot fallback without running body analysis.
func TestLocalSyntaxSnapshotDeclines(t *testing.T) {
	path := os.Getenv("ELISA_SYNTAX_SNAPSHOT_PROBE")
	if path == "" {
		t.Skip("set ELISA_SYNTAX_SNAPSHOT_PROBE to a local source entrypoint")
	}
	var errors bytes.Buffer
	loaded, ok := loadProgramInput(path, &errors)
	if !ok {
		t.Fatal(errors.String())
	}
	lex := lexer.New(path, loaded.source)
	tokens := lex.Tokenize()
	p := parser.NewWithSyntaxTrace(tokens)
	file := p.ParseFile(path)
	if len(lex.Errors()) != 0 || len(p.Errors()) != 0 {
		t.Fatalf("lex errors %v; parse errors %v", lex.Errors(), p.Errors())
	}
	counts := map[string]int{}
	total := 0
	var scan func([]ast.Decl)
	scan = func(decls []ast.Decl) {
		for _, decl := range decls {
			switch node := decl.(type) {
			case *ast.NamespaceDecl:
				scan(node.Decls)
			case *ast.StaticIfDecl:
				scan(node.Then)
				for _, arm := range node.Elifs {
					scan(arm.Body)
				}
				scan(node.Else)
			case *ast.FuncDecl, *ast.ImplDecl:
				total++
				if _, err := ast.NewSyntaxSnapshot(&ast.File{Decls: []ast.Decl{decl}}, 1<<40); err != nil {
					counts[err.Error()]++
				}
			}
		}
	}
	scan(file.Decls)
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Logf("declined %d: %s", counts[key], key)
	}
	t.Logf("scanned %d function-bearing declarations", total)
}

func TestStage1ParserReleaseMemoryProbe(t *testing.T) {
	path := os.Getenv("ELISA_SYNTAX_SNAPSHOT_PROBE")
	if path == "" {
		t.Skip("set ELISA_SYNTAX_SNAPSHOT_PROBE")
	}
	var errors bytes.Buffer
	loaded, ok := loadProgramInput(path, &errors)
	if !ok {
		t.Fatal(errors.String())
	}
	runtime.GC()
	var before, parsed, collected runtime.MemStats
	runtime.ReadMemStats(&before)
	l := lexer.New(path, loaded.source)
	p := parser.New(l.Tokenize())
	file := p.ParseFile(path)
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		t.Fatal("could not parse benchmark source")
	}
	l, p = nil, nil
	runtime.ReadMemStats(&parsed)
	runtime.GC()
	runtime.ReadMemStats(&collected)
	runtime.KeepAlive(file)
	runtime.KeepAlive(loaded)
	t.Logf("heap before=%d parsed=%d after_gc=%d bytes; reclaimed=%d", before.HeapAlloc, parsed.HeapAlloc, collected.HeapAlloc, int64(parsed.HeapAlloc)-int64(collected.HeapAlloc))
}

func BenchmarkStage1TransientSyntaxSnapshots(b *testing.B) {
	path := os.Getenv("ELISA_SYNTAX_SNAPSHOT_PROBE")
	if path == "" {
		b.Skip("set ELISA_SYNTAX_SNAPSHOT_PROBE")
	}
	var errors bytes.Buffer
	loaded, ok := loadProgramInput(path, &errors)
	if !ok {
		b.Fatal(errors.String())
	}
	l := lexer.New(path, loaded.source)
	p := parser.New(l.Tokenize())
	file := p.ParseFile(path)
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		b.Fatal("could not parse benchmark source")
	}
	var decls []ast.Decl
	var collect func([]ast.Decl)
	collect = func(items []ast.Decl) {
		for _, decl := range items {
			switch n := decl.(type) {
			case *ast.NamespaceDecl:
				collect(n.Decls)
			case *ast.StaticIfDecl:
				collect(n.Then)
				for _, arm := range n.Elifs {
					collect(arm.Body)
				}
				collect(n.Else)
			case *ast.FuncDecl, *ast.ImplDecl:
				decls = append(decls, decl)
			}
		}
	}
	collect(file.Decls)
	if len(decls) == 0 {
		b.Fatal("no function-bearing declarations")
	}
	b.ReportAllocs()
	b.ResetTimer()
	declined := 0
	for i := 0; i < b.N; i++ {
		for _, decl := range decls {
			if _, err := ast.NewTransientSyntaxSnapshot(&ast.File{Decls: []ast.Decl{decl}}, 1<<40); err != nil {
				declined++
			}
		}
	}
	b.ReportMetric(float64(len(decls)), "decls/op")
	b.ReportMetric(float64(declined)/float64(b.N), "declined/op")
}
