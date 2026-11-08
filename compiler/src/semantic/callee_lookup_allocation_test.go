package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestResolveCalleeCanonicalThenBare(t *testing.T) {
	scope := NewScope(nil)
	canonical := &ast.FuncDecl{Name: "A.f"}
	bare := &ast.FuncDecl{Name: "f"}
	scope.Define(&Symbol{Name: "A.f", Node: canonical})
	scope.Define(&Symbol{Name: "f", Node: bare})
	id := &ast.Ident{Name: "f"}
	call := &ast.CallExpr{Func: id}
	a := &Analyzer{globalScope: scope, resolvedValueNames: map[*ast.Ident]string{id: "A.f"}}
	if got, ok := a.resolveCalleeFuncDecl(call); !ok || got != canonical {
		t.Fatal("canonical lookup failed")
	}
	a.resolvedValueNames[id] = "Missing.f"
	if got, ok := a.resolveCalleeFuncDecl(call); !ok || got != bare {
		t.Fatal("bare fallback failed")
	}
	if allocs := testing.AllocsPerRun(100, func() { a.resolveCalleeFuncDecl(call) }); allocs != 0 {
		t.Fatalf("callee lookup allocated %v times", allocs)
	}
}
