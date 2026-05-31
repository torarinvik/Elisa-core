package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
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

func (a *Analyzer) defineReceiverOverloadGlobal(visibleName string, sym *Symbol, pos lexer.Pos) {
	if a == nil || sym == nil {
		return
	}
	if visibleName == "" {
		a.defineGlobal(sym, pos)
		return
	}
	if sym.UFCSOnly {
		newReceiver, newOK := receiverOverloadType(sym)
		if !newOK {
			a.errorf(pos, "@method function %q must take at least one receiver parameter", visibleName)
			return
		}
		for _, existing := range a.ufcsFunctionsByName[visibleName] {
			existingReceiver, existingOK := receiverOverloadType(existing)
			if existingOK && SameType(existingReceiver, newReceiver) {
				a.errorf(pos, "%s", DuplicateDeclarationMessage(visibleName, sym.Kind))
				return
			}
		}
		sym.Name = ReceiverOverloadSymbolName(visibleName, newReceiver, visibleName)
		if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
			fnType.Name = sym.Name
		}
		if _, ok := a.globalScope.Define(sym); !ok {
			a.errorf(pos, "%s", DuplicateDeclarationMessage(visibleName, sym.Kind))
			return
		}
		a.registerUFCSFunction(visibleName, sym)
		return
	}
	if existing, ok := a.globalScope.Lookup(visibleName); ok && existing != nil {
		existingReceiver, existingOK := receiverOverloadType(existing)
		newReceiver, newOK := receiverOverloadType(sym)
		if !existingOK || !newOK || SameType(existingReceiver, newReceiver) {
			a.errorf(pos, "%s", DuplicateDeclarationMessage(existing.Name, existing.Kind))
			return
		}
		a.registerUFCSFunction(visibleName, existing)
		sym.Name = ReceiverOverloadSymbolName(visibleName, newReceiver, visibleName)
		if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
			fnType.Name = sym.Name
		}
		if _, ok := a.globalScope.Define(sym); !ok {
			a.errorf(pos, "%s", DuplicateDeclarationMessage(visibleName, sym.Kind))
			return
		}
		a.registerUFCSFunction(visibleName, sym)
		return
	}
	a.defineGlobal(sym, pos)
	a.registerUFCSFunction(visibleName, sym)
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
