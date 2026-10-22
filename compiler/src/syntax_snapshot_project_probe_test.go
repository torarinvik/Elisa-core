package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"os"
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
