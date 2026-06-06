package semantic

import (
	"strconv"
	"unicode"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func orderedWithItems(bundles []ast.WithBundleUse, args []ast.WithArg, order []ast.WithItem) []ast.WithItem {
	if len(order) != 0 {
		return append([]ast.WithItem(nil), order...)
	}
	items := make([]ast.WithItem, 0, len(bundles)+len(args))
	for _, bundle := range bundles {
		items = append(items, ast.WithItem{Position: bundle.Position, Bundle: bundle, IsBundle: true})
	}
	for _, arg := range args {
		items = append(items, ast.WithItem{Position: arg.Position, Arg: arg})
	}
	return items
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

func (a *Analyzer) nextImplicitTempName(base string) string {
	a.implicitTempCounter++
	return "__with_" + sanitizeImplicitTempBase(base) + "_" + strconv.Itoa(a.implicitTempCounter)
}

func (a *Analyzer) applyWithBundleBindings(position lexer.Pos, bundleUse ast.WithBundleUse, working map[string]ast.Expr, explicitValues map[string]ast.Expr) {
	bundle, _, ok := a.lookupVisibleContextBundle(bundleUse.Name)
	if !ok || bundle == nil {
		a.errorf(position, "unknown implicit bundle %q", bundleUse.Name)
		return
	}
	for _, field := range bundle.Fields {
		if expr, ok := explicitValues[field.Name]; ok {
			working[field.Name] = expr
			continue
		}
		if bundleUse.Spread {
			if expr, ok := a.lookupSameNameImplicitExpr(field.Name, working); ok {
				working[field.Name] = expr
				continue
			}
			a.errorf(position, "missing same-name ambient value for %q in implicit bundle %q", field.Name, bundleUse.Name)
			continue
		}
		a.errorf(position, "missing explicit value for %q in implicit bundle %q; add `..` to spread ambient bindings", field.Name, bundleUse.Name)
	}
}

func (a *Analyzer) analyzeWithStmt(stmt *ast.WithStmt) {
	if a == nil || stmt == nil {
		return
	}
	items := orderedWithItems(stmt.Bundles, stmt.Args, stmt.WithItemOrder)
	working := a.combinedImplicitBindings()
	tempStmts := make([]ast.Stmt, 0)
	originalBody := append([]ast.Stmt(nil), stmt.Body...)
	for _, item := range items {
		if item.IsBundle {
			explicitValues := make(map[string]ast.Expr, len(item.Bundle.Args))
			for _, arg := range item.Bundle.Args {
				valueExpr := a.resolveWithArgValueExpr(arg, "argument")
				if arg.Shorthand {
					explicitValues[arg.Name] = valueExpr
					continue
				}
				tempName := a.nextImplicitTempName(arg.Name)
				tempStmts = append(tempStmts, &ast.VarDeclStmt{Position: arg.Position, Name: tempName, Value: valueExpr})
				explicitValues[arg.Name] = &ast.Ident{Position: arg.Position, Name: tempName}
			}
			a.applyWithBundleBindings(item.Position, item.Bundle, working, explicitValues)
			continue
		}
		if item.Arg.Shorthand {
			working[item.Arg.Name] = a.resolveWithArgValueExpr(item.Arg, "argument")
			continue
		}
		valueExpr := a.resolveWithArgValueExpr(item.Arg, "argument")
		tempName := a.nextImplicitTempName(item.Arg.Name)
		tempStmts = append(tempStmts, &ast.VarDeclStmt{Position: item.Arg.Position, Name: tempName, Value: valueExpr})
		working[item.Arg.Name] = &ast.Ident{Position: item.Arg.Position, Name: tempName}
	}
	if len(tempStmts) != 0 {
		stmt.Body = append(append(make([]ast.Stmt, 0, len(tempStmts)+len(originalBody)), tempStmts...), originalBody...)
	}
	a.analyzeNestedStmtBodyWithExprBindings(&a.currentImplicitScopes, working, tempStmts, originalBody)
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
	explicitSources := map[string]bool{}
	for _, item := range orderedWithItems(expr.WithBundles, expr.WithArgs, expr.WithItemOrder) {
		if item.IsBundle {
			explicitValues := make(map[string]ast.Expr, len(item.Bundle.Args))
			for _, arg := range item.Bundle.Args {
				explicitValues[arg.Name] = a.resolveWithArgValueExpr(arg, "argument")
			}
			a.applyWithBundleBindings(item.Position, item.Bundle, working, explicitValues)
			continue
		}
		working[item.Arg.Name] = a.resolveWithArgValueExpr(item.Arg, "argument")
		explicitSources[item.Arg.Name] = true
	}
	resolved := make([]ast.Expr, 0, len(ft.ImplicitParamNames))
	usedExplicit := map[string]bool{}
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
				a.errorf(expr.Pos(), "call to region-polymorphic %q must occur inside an inferred region (open one with `in auto:`) or another region-polymorphic function", ft.Name)
				continue
			} else if packedStoreType, isPackedStore := expectedType.(*PackedEnumStoreType); isPackedStore && packedStoreType != nil {
				// docs/74: thread the region-backed packed store. A caller that already has the store
				// (its own implicit param) resolves it via the same-name lookup above; otherwise the
				// backend creates it on demand at this call site (the root call) and reuses it after.
				argExpr = packedStoreImplicitArgExpr(packedStoreType)
				a.exprTypes[argExpr] = expectedType
				resolved = append(resolved, argExpr)
				continue
			} else if storeType, isTreeStore := expectedType.(*TreeStoreType); isTreeStore && storeType != nil {
				if ownerArg, found := a.recoverImplicitTreeStoreOwnerArg(expr, ft, explicitCount); found {
					resolved = append(resolved, ownerArg)
					continue
				}
				a.recordImplicitTreeStoreUse(storeType)
				argExpr = treeStoreImplicitArgExpr(storeType)
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
		if explicitSources[name] {
			usedExplicit[name] = true
		}
	}
	for name, isExplicit := range explicitSources {
		if isExplicit && !usedExplicit[name] {
			a.errorf(expr.Pos(), "unused explicit trailing with binding %q for call to %q", name, ft.Name)
		}
	}
	expr.ResolvedImplicitArgs = resolved
	expr.ResolvedImplicitArgsValid = len(resolved) == len(ft.ImplicitParamNames)
	return expr.ResolvedImplicitArgsValid
}
