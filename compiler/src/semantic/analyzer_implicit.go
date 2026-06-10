package semantic

import (
	"strconv"
	"unicode"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func funcTypeHasImplicitParam(ft *FuncType, name string) bool {
	if ft == nil {
		return false
	}
	for _, n := range ft.ImplicitParamNames {
		if n == name {
			return true
		}
	}
	return false
}

func (a *Analyzer) implicitBindingsForCurrentFunction(ft *FuncType) map[string]ast.Expr {
	if a == nil || ft == nil || len(ft.ImplicitParamNames) == 0 || a.currentScope == nil {
		return nil
	}
	bindings := make(map[string]ast.Expr, len(ft.ImplicitParamNames))
	for _, name := range ft.ImplicitParamNames {
		if name == "" {
			continue
		}
		sym, ok := a.currentScope.Lookup(name)
		if !ok || sym == nil || sym.Kind != SymbolParam {
			continue
		}
		bindings[name] = &ast.Ident{Position: lexer.Pos{}, Name: name}
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}

func (a *Analyzer) combinedImplicitBindings() map[string]ast.Expr {
	return combineExprBindingScopes(a.currentImplicitScopes)
}

func (a *Analyzer) lookupSameNameImplicitExpr(name string, working map[string]ast.Expr) (ast.Expr, bool) {
	if working != nil {
		if expr, ok := working[name]; ok && expr != nil {
			return expr, true
		}
	}
	if a.currentScope != nil {
		if _, ok := a.currentScope.Lookup(name); ok {
			return &ast.Ident{Position: lexer.Pos{}, Name: name}, true
		}
	}
	if _, _, ok := a.lookupVisibleGlobal(name); ok {
		return &ast.Ident{Position: lexer.Pos{}, Name: name}, true
	}
	return nil, false
}

func (a *Analyzer) resolveImplicitCallArgs(expr *ast.CallExpr, ft *FuncType, bindings map[string]Type, shapeBindings map[string]Shape, regionBindings map[string]string, permissionBindings map[string][]ast.PermissionRef) bool {
	if expr == nil || ft == nil {
		return false
	}
	explicitCount := funcTypeExplicitParamCount(ft)
	if len(ft.ImplicitParamNames) == 0 {
		expr.ResolvedImplicitArgs = nil
		expr.ResolvedImplicitArgsValid = true
		return true
	}
	working := a.combinedImplicitBindings()
	resolved := make([]ast.Expr, 0, len(ft.ImplicitParamNames))
	for i, name := range ft.ImplicitParamNames {
		paramIndex := explicitCount + i
		if paramIndex >= len(ft.Params) {
			a.errorf(expr.Pos(), "internal error: implicit parameter %q index out of bounds for call to %q", name, ft.Name)
			continue
		}
		expectedType := a.substituteType(ft.Params[paramIndex], bindings, shapeBindings, regionBindings, permissionBindings)
		argExpr, ok := working[name]
		if !ok || argExpr == nil {
			if fallback, found := a.lookupSameNameImplicitExpr(name, working); found {
				argExpr = fallback
			} else if name == regionPolymorphicImplicitParamName {
				// docs/75: thread the caller's ambient inferred region into a region-polymorphic
				// callee. A region-polymorphic caller threads its own `__region_auto` (resolved above
				// via the same-name lookup); otherwise the active `in auto:` region supplies it.
				if regionArg, found := a.regionPolymorphicCallerRegionArg(); found {
					a.exprTypes[regionArg] = expectedType
					resolved = append(resolved, regionArg)
					continue
				}
				a.errorf(expr.Pos(), "call to region-polymorphic %q must occur where a region can be inferred: inside another region-polymorphic function (one that returns an inferred-region value) or an explicit `region NAME(size):` scope", ft.Name)
				continue
			} else if packedStoreType, isPackedStore := expectedType.(*PackedEnumStoreType); isPackedStore && packedStoreType != nil {
				// docs/74: thread the region-backed packed store. A caller that already has the store
				// (its own implicit param) resolves it via the same-name lookup above; otherwise the
				// backend creates it on demand at this call site (the root call) and reuses it after.
				argExpr = packedStoreImplicitArgExpr(packedStoreType)
				a.exprTypes[argExpr] = expectedType
				resolved = append(resolved, argExpr)
				continue
			} else {
				a.errorf(expr.Pos(), "missing implicit argument %q for call to %q", name, ft.Name)
				continue
			}
		}
		var actualType Type
		argExpr, actualType = a.analyzeCallLikeValueExpr(argExpr, expectedType)
		if !AssignableTo(expectedType, actualType) {
			a.errorf(argExpr.Pos(), "implicit argument %q to %q expects %s, got %s", name, ft.Name, expectedType, actualType)
			a.reportMutableRefArgumentNote(argExpr.Pos(), expectedType, actualType)
			a.reportShapeMismatchNotes(argExpr.Pos(), expectedType, actualType)
		}
		if !a.tryConsumeSinkCallArg(expr.Func, ft, paramIndex, argExpr, expectedType) {
			a.consumeAffineValueExpr(argExpr, expectedType, "implicit argument to call "+strconv.Quote(ft.Name))
		}
		resolved = append(resolved, argExpr)
	}
	expr.ResolvedImplicitArgs = resolved
	expr.ResolvedImplicitArgsValid = len(resolved) == len(ft.ImplicitParamNames)
	return expr.ResolvedImplicitArgsValid
}

// sanitizeImplicitTempBase normalizes a type/value name into an identifier-safe
// fragment for synthesized implicit parameter names (e.g. the hidden
// `__packed_store_E` / `__tree_store_E` params threaded by docs/74/75).
func sanitizeImplicitTempBase(name string) string {
	if name == "" {
		return "value"
	}
	runes := make([]rune, 0, len(name))
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			runes = append(runes, r)
			continue
		}
		runes = append(runes, '_')
	}
	if len(runes) == 0 || unicode.IsDigit(runes[0]) {
		runes = append([]rune{'v', '_'}, runes...)
	}
	return string(runes)
}
