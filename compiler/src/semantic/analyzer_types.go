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
	if existing, ok := a.globalScope.Lookup(visibleName); ok && existing != nil {
		existingReceiver, existingHasRecv := receiverOverloadType(existing)
		newReceiver, newHasRecv := receiverOverloadType(sym)
		existingIsFunc := existing.Kind == SymbolFunc || existing.Kind == SymbolExternFunc
		newIsFunc := sym.Kind == SymbolFunc || sym.Kind == SymbolExternFunc
		// Two same-named functions form a legal overload set when they are
		// distinguishable, either by receiver type (both take a receiver whose
		// first-param types differ) or by arity (exactly one takes zero params — a
		// free `f()` coexisting with a method `f(x)` / `x.f()`). A receiver clash
		// (same first-param type), two zero-param functions, or a collision with a
		// non-function symbol remain genuine duplicates.
		sameReceiver := existingHasRecv && newHasRecv && SameType(existingReceiver, newReceiver)
		bothZeroParam := !existingHasRecv && !newHasRecv
		if !existingIsFunc || !newIsFunc || sameReceiver || bothZeroParam {
			a.errorf(pos, "%s", DuplicateDeclarationMessage(existing.Name, existing.Kind))
			return
		}
		if !newHasRecv {
			// The new function takes zero params: it must own the bare global name so a
			// 0-arg call (which has no receiver to probe) resolves to it. Re-mangle the
			// existing receiver overload — still reachable by receiver type through the
			// UFCS dictionary. (existing has a receiver here; bothZeroParam was rejected.)
			a.remangleBareReceiverOverload(visibleName, existing, existingReceiver)
			if _, ok := a.globalScope.Define(sym); !ok {
				a.errorf(pos, "%s", DuplicateDeclarationMessage(visibleName, sym.Kind))
				return
			}
			a.registerUFCSFunction(visibleName, sym)
			return
		}
		// The new function has a receiver: the existing keeps the bare name (a zero-param
		// existing stays bare; a receiver existing keeps it as before) and the new one
		// gets a receiver-mangled name.
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

// remangleBareReceiverOverload re-keys a receiver overload that currently owns the
// bare global name to its receiver-mangled name, freeing the bare name for a
// zero-param overload of the same visible name. The symbol stays reachable by
// receiver type through the UFCS dictionary.
func (a *Analyzer) remangleBareReceiverOverload(visibleName string, existing *Symbol, existingReceiver Type) {
	if a == nil || a.globalScope == nil || existing == nil {
		return
	}
	delete(a.globalScope.Symbols, existing.Name)
	existing.Name = ReceiverOverloadSymbolName(visibleName, existingReceiver, visibleName)
	if fnType, ok := existing.Type.(*FuncType); ok && fnType != nil {
		fnType.Name = existing.Name
	}
	a.globalScope.Symbols[existing.Name] = existing
	a.registerUFCSFunction(visibleName, existing)
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
