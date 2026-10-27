package semantic

import (
	"reflect"
	"sync"
	"testing"

	"elisacore/src/ast"
)

func TestSpecializationHeaderNameIndexRebuildIsPrivate(t *testing.T) {
	decl := &ast.FuncDecl{Name: "lookup"}
	symbol := &Symbol{Name: "lookup", Node: decl}
	scope := NewScope(nil)
	scope.Symbols["lookup"] = symbol
	root := &Analyzer{globalScope: scope, funcDeclSymbols: map[*ast.FuncDecl]*Symbol{decl: symbol}}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "lookup"}}
	if _, unique := root.uniqueHeaderCallTarget(call, nil); !unique {
		t.Fatal("root target should be unique")
	}

	first := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	second := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	base := reflect.ValueOf(root.headerFunctionNames.counts).Pointer()
	if reflect.ValueOf(first.headerFunctionNames.counts).Pointer() != base || reflect.ValueOf(second.headerFunctionNames.counts).Pointer() != base {
		t.Fatal("specializations did not share the published immutable index")
	}
	duplicate := &ast.FuncDecl{Name: "lookup"}
	first.funcDeclSymbols[duplicate] = &Symbol{Name: "lookup", Node: duplicate}
	var firstUnique, secondUnique bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, firstUnique = first.uniqueHeaderCallTarget(call, nil) }()
	go func() { defer wg.Done(); _, secondUnique = second.uniqueHeaderCallTarget(call, nil) }()
	wg.Wait()
	if firstUnique || !secondUnique {
		t.Fatalf("private rebuild changed target uniqueness: first=%v second=%v", firstUnique, secondUnique)
	}
	if root.headerFunctionNames.counts["lookup"] != 1 || first.headerFunctionNames.counts["lookup"] != 2 {
		t.Fatal("private index rebuild changed the canonical index")
	}
	if _, unique := root.uniqueHeaderCallTarget(call, nil); !unique {
		t.Fatal("root target lost uniqueness after a specialization rebuild")
	}
}
