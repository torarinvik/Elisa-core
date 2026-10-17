package semantic

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestAutoReserveKeepsSuppressedLexicalReceiver(t *testing.T) {
	for _, kind := range []SymbolKind{SymbolLocal, SymbolParam, SymbolGlobal} {
		t.Run(string(kind), func(t *testing.T) {
			globals := NewScope(nil)
			global := &Symbol{Name: "flags", Kind: SymbolGlobal, Mutable: true}
			globals.Define(global)
			scope := NewScope(globals)
			if kind != SymbolGlobal {
				scope.Define(&Symbol{Name: "flags", Kind: kind, Mutable: true})
			}
			a := &Analyzer{globalScope: globals, currentScope: scope, suppressDiagnostics: true}
			stmt := a.semanticMakeReserveStmt(lexer.Pos{}, "flags", &ast.IntLit{Value: "4"})
			call := stmt.(*ast.ExprStmt).Expr.(*ast.CallExpr)
			receiver := call.Func.(*ast.FieldExpr).Object
			// Permission validation happens after the lexical scope is gone.
			a.currentScope = globals
			a.suppressDiagnostics = false
			sym, isGlobal := a.globalStorageRoot(receiver)
			if wantGlobal := kind == SymbolGlobal; isGlobal != wantGlobal {
				t.Fatalf("reserve receiver resolved as global=%v, want %v", isGlobal, wantGlobal)
			}
			if isGlobal && sym != global {
				t.Fatal("reserve receiver lost the selected global binding")
			}
		})
	}
}
