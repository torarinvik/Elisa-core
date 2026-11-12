package astscan

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"testing"
)

func TestWalkerFacadesVisitSyntax(t *testing.T) {
	id := &ast.Ident{Name: "__auto_42", Position: lexer.Pos{File: "example.elisa", Line: 2}}
	file := &ast.File{Decls: []ast.Decl{&ast.FuncDecl{Body: []ast.Stmt{&ast.ReturnStmt{Value: id}}}}}
	cases := []struct {
		name string
		walk func(func(any))
	}{
		{"statements", func(visit func(any)) { Walk(file, visit, map[uintptr]bool{}) }},
		{"pointers", func(visit func(any)) { WalkPointers(file, visit, map[uintptr]bool{}) }},
		{"pruned", func(visit func(any)) { WalkPruned(file, func(v any) bool { visit(v); return true }) }},
		{"canonical", func(visit func(any)) {
			WalkCanonical(file, func(v any) bool { visit(v); return true }, map[uintptr]bool{})
		}},
	}
	for _, tc := range cases {
		found := false
		tc.walk(func(v any) {
			if v == id {
				found = true
			}
		})
		if !found {
			t.Errorf("%s walker missed identifier", tc.name)
		}
	}
	foundBinding := false
	WalkProofBindings(file, func(node any, _ reflect.Type, _ ast.Decl, kind uint8, _ string, value string, _ bool) {
		if node == id || kind == BindingVisitStringField && value == "__auto_42" {
			foundBinding = true
		}
	}, map[uintptr]bool{})
	if !foundBinding {
		t.Fatal("binding walker missed identifier")
	}
	foundPosition := false
	WalkProofCoordinates(file, func(pos lexer.Pos) {
		if pos.File == "example.elisa" && pos.Line == 2 {
			foundPosition = true
		}
	}, map[uintptr]bool{})
	if !foundPosition {
		t.Fatal("coordinate walker missed identifier position")
	}
}
