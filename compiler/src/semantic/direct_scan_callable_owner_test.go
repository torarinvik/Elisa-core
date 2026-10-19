package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestDirectScanCalleeRejectsCallableParameterOwnerNode(t *testing.T) {
	owner := &ast.FuncDecl{Name: "enclosing", Body: []ast.Stmt{}}
	for _, kind := range []SymbolKind{SymbolParam, SymbolLocal, SymbolFunc} {
		scope := NewScope(nil)
		scope.Define(&Symbol{Name: "callback", Kind: kind, Node: owner})
		a := &Analyzer{currentScope: scope, globalScope: NewScope(nil)}
		call := &ast.CallExpr{Func: &ast.Ident{Name: "callback"}}
		want := kind == SymbolFunc
		if got, ok := a.resolveDirectCallFuncDecl(call); ok != want || (ok && got != owner) {
			t.Fatalf("direct kind=%v resolved=%p ok=%v, want %v", kind, got, ok, want)
		}
		if got, ok := a.resolveReadOnlyScanCallee(call); ok != want || (ok && got != owner) {
			t.Fatalf("scan kind=%v resolved=%p ok=%v, want %v", kind, got, ok, want)
		}
	}
}

func TestDirectScanCallableParameterShadowsGlobalFunction(t *testing.T) {
	decl := &ast.FuncDecl{Name: "callback", Body: []ast.Stmt{}}
	global := NewScope(nil)
	global.Define(&Symbol{Name: "callback", Kind: SymbolFunc, Node: decl})
	scope := NewScope(global)
	scope.Define(&Symbol{Name: "callback", Kind: SymbolParam, Node: decl})
	a := &Analyzer{currentScope: scope, globalScope: global}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "callback"}}
	if _, ok := a.resolveDirectCallFuncDecl(call); ok {
		t.Fatal("callback parameter resolved as shadowed function")
	}
	if _, ok := a.resolveReadOnlyScanCallee(call); ok {
		t.Fatal("callback parameter borrowed shadowed function summary")
	}
	a.currentScope = NewScope(nil)
	if got, ok := a.resolveReadOnlyScanCallee(call); !ok || got != decl {
		t.Fatal("visible global function did not resolve")
	}
	global.Symbols["callback"].Kind = SymbolParam
	if _, ok := a.resolveReadOnlyScanCallee(call); ok {
		t.Fatal("global fallback accepted non-function metadata")
	}
}
