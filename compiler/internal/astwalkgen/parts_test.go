package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

// The split is a file-layout change: every generated declaration must retain
// the exact Go syntax produced by the original walker generator.
func TestGeneratedPartsPreserveDeclarations(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	for _, mode := range modes {
		source, err := generate(mode)
		if err != nil {
			t.Fatal(err)
		}
		parts, err := generatedParts(mode, source)
		if err != nil {
			t.Fatal(err)
		}
		want, err := normalizedGeneratedDecls(map[string][]byte{mode.File: source})
		if err != nil {
			t.Fatal(err)
		}
		got, err := normalizedGeneratedDecls(parts)
		if err != nil {
			t.Fatal(err)
		}
		if len(want) != len(got) {
			t.Fatalf("%s: declarations changed: %d vs %d", mode.File, len(want), len(got))
		}
		for name, expected := range want {
			if got[name] != expected {
				t.Fatalf("%s: declaration %s changed during partition", mode.File, name)
			}
		}
	}
}

func normalizedGeneratedDecls(files map[string][]byte) (map[string]string, error) {
	out := make(map[string]string)
	for name, source := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, source, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
				continue
			}
			key := "var"
			if f, ok := decl.(*ast.FuncDecl); ok {
				key = f.Name.Name
			}
			if _, exists := out[key]; exists {
				return nil, fmt.Errorf("duplicate declaration %s", key)
			}
			var b bytes.Buffer
			if err := format.Node(&b, fset, decl); err != nil {
				return nil, err
			}
			out[key] = b.String()
		}
	}
	return out, nil
}
