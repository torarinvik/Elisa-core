package semantic

import (
	"strconv"
	"unicode"

	"llcontext/src/ast"
	"llcontext/src/lexer"
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

func cloneImplicitBindings(src map[string]ast.Expr) map[string]ast.Expr {
	if len(src) == 0 {
		return map[string]ast.Expr{}
	}
	out := make(map[string]ast.Expr, len(src))
	for name, expr := range src {
		out[name] = expr
	}
	return out
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
	merged := map[string]ast.Expr{}
	for _, scope := range a.currentImplicitScopes {
		for name, expr := range scope {
			merged[name] = expr
		}
	}
	return merged
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
	scope := NewScope(a.currentScope)
	savedScope := a.currentScope
	a.currentScope = scope
	for _, tempStmt := range tempStmts {
		a.analyzeStmt(tempStmt)
	}
	a.currentScope = savedScope
	savedImplicitScopes := a.currentImplicitScopes
	a.currentImplicitScopes = append(append([]map[string]ast.Expr(nil), savedImplicitScopes...), cloneImplicitBindings(working))
	a.currentScope = scope
	a.withLocalParamPackFrame(func() {
		for _, bodyStmt := range originalBody {
			a.analyzeStmt(bodyStmt)
		}
	})
	a.currentScope = savedScope
	a.currentImplicitScopes = savedImplicitScopes
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
		argExpr, ok := working[name]
		if !ok || argExpr == nil {
			a.errorf(expr.Pos(), "missing implicit argument %q for call to %q", name, ft.Name)
			continue
		}
		expectedType := a.substituteType(ft.Params[paramIndex], bindings, shapeBindings, regionBindings, permissionBindings)
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
