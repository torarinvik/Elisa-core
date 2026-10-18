package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestStoreFlowCalleeHonorsCallableShadows(t *testing.T) {
	global := &ast.FuncDecl{Name: "invoke", Body: []ast.Stmt{}}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "invoke"}}
	for _, tc := range []struct {
		name       string
		locals     map[string]bool
		params     map[string]bool
		wantGlobal bool
	}{
		{name: "global", wantGlobal: true},
		{name: "parameter", params: map[string]bool{"invoke": true}},
		{name: "local", locals: map[string]bool{"invoke": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Analyzer{
				funcDeclSymbols: map[*ast.FuncDecl]*Symbol{global: {Type: &FuncType{}}},
				storeFlowLocals: tc.locals,
				storeFlowParams: tc.params,
			}
			got := a.storeFlowCallee(call)
			if (got == global) != tc.wantGlobal {
				t.Fatalf("global resolved=%v, want %v", got == global, tc.wantGlobal)
			}
		})
	}
}
