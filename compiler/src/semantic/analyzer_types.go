package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func astAggregateStates(expr *ast.AggregateStateTypeExpr) []RefState {
	if expr == nil {
		return nil
	}
	if len(expr.States) != 0 {
		states := make([]RefState, len(expr.States))
		for i, state := range expr.States {
			states[i] = RefState(state)
		}
		return states
	}
	return []RefState{RefState(expr.State)}
}

func (a *Analyzer) errorLegacyBuiltinReplacement(pos lexer.Pos, oldName, replacement string) {
	a.errorf(pos, "legacy built-in %q has been replaced; use %q instead", oldName, replacement)
}

func (a *Analyzer) defineGlobal(sym *Symbol, pos lexer.Pos) {
	if existing, ok := a.globalScope.Define(sym); !ok {
		a.errorf(pos, "%s", DuplicateDeclarationMessage(existing.Name, existing.Kind))
	}
}

func (a *Analyzer) defineLocal(sym *Symbol, pos lexer.Pos) {
	if a.currentScope == nil {
		return
	}
	if existing, ok := a.currentScope.Define(sym); !ok {
		a.errorf(pos, "%s", DuplicateLocalMessage(existing.Name, existing.Kind))
		return
	}
	a.trackAffineValueSymbol(sym)
	a.recordSpecializedValueTypeBinding(sym, sym.Type)
}

func (a *Analyzer) defineLocalInScope(scope *Scope, sym *Symbol, pos lexer.Pos) {
	if scope == nil {
		return
	}
	if existing, ok := scope.Define(sym); !ok {
		a.errorf(pos, "%s", DuplicateLocalMessage(existing.Name, existing.Kind))
		return
	}
	a.trackAffineValueSymbol(sym)
	a.recordSpecializedValueTypeBinding(sym, sym.Type)
}
