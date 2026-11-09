package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) analyzeIdentifierExpr(n *ast.Ident) (result Type) {
	a.checkOneShotClosureUse(n)
	if a.currentScope != nil {
		// A private global reachable through the scope chain must still respect
		// visibility: skip it here so resolution falls through to the global
		// lookup (which also filters it) and reports an undefined name when
		// accessed from outside its owning module. Locals/params are never
		// Private, so this only affects qualified globals.
		if sym, ok := a.lookupIdentifierScope(n); ok && a.globalNameIsVisible(sym, n.Name) {
			a.recordResolvedPrimitiveConstantName(n, sym)
			if len(a.storageViewLoopUseFrames) != 0 || len(a.loopAffineFrames) != 0 {
				if base, named := trackedNamedStateStructBase(sym.Type); named && base != nil && !base.ProtocolStates {
					if a.derivedLoopBindings == nil {
						a.derivedLoopBindings = map[*ast.Ident]*Symbol{}
					}
					a.derivedLoopBindings[n] = sym
				}
			}
			a.recordResolvedGlobalStorage(n, sym)
			result = promoteWritableRefType(sym.Type, symbolPromotesWritableRef(sym))
			if a.suppressGlobalReadCheck == 0 && isGlobalStorageSymbol(sym) {
				a.recordFunctionPermissionRefs(globalReadRefs(n.Position))
				if a.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
					a.recordFunctionPermissionRefs(unsafeMutableGlobalRefs(n.Position))
				}
			}
			if sym.Ghost {
				a.ghostReadSeen = true
			}
			if sym.Ghost && a.ghostReadAllowed == 0 {
				// SOUNDNESS: a ghost var is verification-only and erased from codegen. Real runtime
				// code may never read it (the value does not exist at runtime), and no real value may
				// be assigned from it. Ghost reads are permitted ONLY inside a contract clause
				// (requires/ensure/invariant/assert) or another ghost var's initializer — contexts
				// that raise a.ghostReadAllowed.
				a.errorf(n.Pos(), "ghost variable %q is verification-only and cannot be read by real code: it is erased from codegen, so it may appear only in contracts (requires/ensure/invariant/assert) or another `ghost` declaration", n.Name)
				return
			}
			if sym.Kind == SymbolRegionMark {
				a.errorf(n.Pos(), "checkpoint %q can only be used in restore <region> from %q", n.Name, n.Name)
				return
			}
			if sym.Kind == SymbolRegion && a.programRegionNameUse == 0 {
				if _, program := sym.Node.(*ast.GlobalRegionDecl); program {
					// docs/128 rule 1 holds only because nothing frees or resets the program
					// arena: its Arena value never reaches code that could.
					a.errorf(n.Pos(), "program-lived region %q can only be named as a region (`in %s:`, `new[%s]`, `@%s`), not used as an Arena value", n.Name, n.Name, n.Name, n.Name)
					return
				}
			}
			if sym.Kind == SymbolCheckpoint {
				a.errorf(n.Pos(), "checkpoint %q can only be used in restore %q", n.Name, n.Name)
				return
			}
			if refState, ok := a.currentRegionRefs[sym]; ok {
				if _, dep, invalid := firstInvalidRegionDependency(refState); invalid {
					label := "value"
					if _, isRef := result.(*RefType); isRef {
						label = "reference"
					}
					a.errorf(n.Pos(), invalidatedRegionFactUseMessage(label, n.Name, dep.InvalidatedBy))
					return
				}
			}
			a.reportInvalidStorageViewUse(n)
			if state, ok := a.lookupAffineValueState(n); ok && a.containsAffineHandleValues(result, map[string]bool{}) {
				a.errorf(n.Pos(), consumedFactUseMessage(affineHandleKind(sym.Type), n.Name, state.ConsumedBy))
				return
			}
			if a.suppressUninitReadCheck == 0 && a.isInvalidUninitializedSymbol(sym) {
				a.errorf(n.Pos(), "use of uninitialized variable %q: it has no valid whole-value initialization before this read; assign the variable itself first", n.Name)
				return
			}
			if ownerType, ok := borrowableOwnerRefElemType(result); ok {
				if key, ok := a.lookupBorrowedOwnerRefKey(n); ok {
					if state, ok := a.lookupAffineValueStateForKey(key); ok && state.ConsumedBy != "" {
						a.errorf(n.Pos(), consumedFactUseMessage(affineHandleKind(ownerType), n.Name, state.ConsumedBy))
						return
					}
				}
			}
			if a.reportRawInteriorAffineAliasUse(n, sym) {
				return
			}
			if fnType, ok := a.lookupCurrentFunctionValueType(sym); ok {
				result = fnType
				return
			}
			if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok {
				// Following the binding (not functionValueTypeForExpr directly) marks sym
				// in progress, so a self-referential initializer (`const X: i32 = X + 1`)
				// stops here instead of recursing until the Go stack overflows.
				if fnType, ok := a.functionValueTypeFollowingBinding(sym, valueExpr); ok {
					result = promoteWritableRefType(fnType, symbolPromotesWritableRef(sym))
					return
				}
			}
			if specializedType, ok := a.lookupCurrentSpecializedValueType(sym); ok {
				result = promoteWritableRefType(specializedType, symbolPromotesWritableRef(sym))
			}
			if t, ok := a.lookupRefinedExprType(n); ok {
				if specializedType, ok := a.specializeCallbackCarryingType(t, result); ok {
					result = promoteWritableRefType(specializedType, symbolPromotesWritableRef(sym))
				} else {
					result = promoteWritableRefType(t, symbolPromotesWritableRef(sym))
				}
				return
			}
			return
		}
	}
	if t, ok := a.lookupRefinedExprType(n); ok {
		result = t
		return
	}
	if sym, canonical, ok := a.lookupVisibleGlobal(n.Name); ok {
		if a.reportUsingAmbiguity(n.Name, n.Position) {
			result = invalidType
			return
		}
		// Record the resolved canonical (namespace/using/import-qualified) name so
		// the interpreter can resolve namespaced value/function references instead
		// of re-deriving from the bare name without namespace context.
		if a.resolvedValueNames != nil && canonical != "" && canonical != n.Name && sym.Kind != SymbolConst {
			a.resolvedValueNames[n] = canonical
		}
		a.recordResolvedPrimitiveConstantName(n, sym)
		a.recordResolvedGlobalStorage(n, sym)
		result = promoteWritableRefType(sym.Type, symbolPromotesWritableRef(sym))
		if a.suppressGlobalReadCheck == 0 && isGlobalStorageSymbol(sym) {
			a.recordFunctionPermissionRefs(globalReadRefs(n.Position))
			if a.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
				a.recordFunctionPermissionRefs(unsafeMutableGlobalRefs(n.Position))
			}
		}
		if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok {
			if fnType, ok := a.functionValueTypeForExpr(valueExpr); ok {
				result = promoteWritableRefType(fnType, symbolPromotesWritableRef(sym))
				return
			}
		}
		return
	}
	if valueParam, ok := a.lookupConstParam(n.Name); ok {
		if param, paramOK := valueParam.(*ConstParamType); paramOK && param.ValueType != nil {
			result = param.ValueType
			return
		}
		if value, valueOK := valueParam.(*ConstValueType); valueOK && value.Value.Kind == ConstInt {
			result = a.namedTypes["usize"]
			return
		}
	}
	if a.currentScope != nil {
		if hint, ok := a.currentScope.LookupConditionalBindingHint(n.Name); ok {
			a.errorf(n.Pos(), "%s", hint)
			result = invalidType
			return
		}
	}
	if rewriteDefaultType, ok := a.analyzeRewriteDefaultIdent(n); ok {
		result = rewriteDefaultType
		return
	}
	if n.Name == "variants" || n.Name == "fields" {
		result = &FuncType{Name: n.Name, Return: &ConstValueType{Value: ConstValue{Kind: ConstUnknown}}}
		return
	}
	if qualified, owner, ok := a.inaccessiblePrivateName(n.Name); ok {
		a.errorf(n.Pos(), "%s", PrivateNameMessage(qualified, owner))
	} else {
		a.errorf(n.Pos(), "%s", UndefinedIdentifierMessage(n.Name))
	}
	result = invalidType
	return
}
