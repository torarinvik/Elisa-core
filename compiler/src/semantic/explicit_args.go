package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

type callArgSource int

const (
	callArgSourceNone callArgSource = iota
	callArgSourceForward
	callArgSourcePack
	callArgSourceExplicit
)

func orderedCallArgItems(expr *ast.CallExpr) []ast.CallArgItem {
	if expr == nil {
		return nil
	}
	if len(expr.ArgItemOrder) != 0 {
		return append([]ast.CallArgItem(nil), expr.ArgItemOrder...)
	}
	items := make([]ast.CallArgItem, 0, len(expr.Args))
	for i, arg := range expr.Args {
		pos := lexer.Pos{}
		if arg != nil {
			pos = arg.Pos()
		}
		items = append(items, ast.CallArgItem{Position: pos, ArgIndex: i})
	}
	return items
}

func orderedArgsScopeItems(packs []ast.ParamPackUse, args []ast.WithArg, order []ast.ArgsScopeItem) []ast.ArgsScopeItem {
	if len(order) != 0 {
		return append([]ast.ArgsScopeItem(nil), order...)
	}
	items := make([]ast.ArgsScopeItem, 0, len(packs)+len(args))
	for _, pack := range packs {
		items = append(items, ast.ArgsScopeItem{Position: pack.Position, Pack: pack, IsPack: true})
	}
	for _, arg := range args {
		items = append(items, ast.ArgsScopeItem{Position: arg.Position, Arg: arg})
	}
	return items
}

func cloneExplicitBindings(src map[string]ast.Expr) map[string]ast.Expr {
	if len(src) == 0 {
		return map[string]ast.Expr{}
	}
	out := make(map[string]ast.Expr, len(src))
	for name, expr := range src {
		out[name] = expr
	}
	return out
}

func (a *Analyzer) combinedExplicitBindings() map[string]ast.Expr {
	merged := map[string]ast.Expr{}
	for _, scope := range a.currentExplicitArgScopes {
		for name, expr := range scope {
			merged[name] = expr
		}
	}
	return merged
}

func isExplicitValueSymbolKind(kind SymbolKind) bool {
	switch kind {
	case SymbolConst, SymbolGlobal, SymbolFunc, SymbolExternFunc, SymbolExternVar, SymbolParam, SymbolLocal:
		return true
	default:
		return false
	}
}

func (a *Analyzer) lookupVisibleValueExpr(name string) (ast.Expr, bool) {
	if a == nil || name == "" {
		return nil, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope == a.globalScope {
			break
		}
		sym, ok := scope.Symbols[name]
		if !ok || sym == nil {
			continue
		}
		root := symbolAliasRoot(sym)
		if root == nil {
			root = sym
		}
		if !isExplicitValueSymbolKind(root.Kind) {
			continue
		}
		return &ast.Ident{Position: lexer.Pos{}, Name: name}, true
	}
	if sym, _, ok := a.lookupVisibleGlobal(name); ok && sym != nil {
		root := symbolAliasRoot(sym)
		if root == nil {
			root = sym
		}
		if isExplicitValueSymbolKind(root.Kind) {
			return &ast.Ident{Position: lexer.Pos{}, Name: name}, true
		}
	}
	return nil, false
}

func (a *Analyzer) resolveSameNameExplicitValueExpr(name string, pos lexer.Pos, context string) ast.Expr {
	if expr, ok := a.lookupVisibleValueExpr(name); ok && expr != nil {
		return expr
	}
	a.errorf(pos, "no value named %q for shorthand %s", name, context)
	return &ast.Ident{Position: pos, Name: name}
}

func (a *Analyzer) resolveWithArgValueExpr(arg ast.WithArg, context string) ast.Expr {
	if !arg.Shorthand {
		return arg.Value
	}
	return a.resolveSameNameExplicitValueExpr(arg.Name, arg.Position, context)
}

func (a *Analyzer) resolveCallArgValueExpr(expr *ast.CallExpr, index int, context string) ast.Expr {
	if expr == nil || index < 0 || index >= len(expr.Args) {
		return nil
	}
	argExpr := expr.Args[index]
	if index >= len(expr.ArgShorthand) || !expr.ArgShorthand[index] {
		return argExpr
	}
	name := expr.ArgName(index)
	if name == "" {
		return argExpr
	}
	return a.resolveSameNameExplicitValueExpr(name, argExpr.Pos(), context)
}

func (a *Analyzer) expandParamPackUseValues(packUse ast.ParamPackUse, context string) (*ParamPack, map[string]ast.Expr) {
	pack, _, ok := a.lookupVisibleParamPack(packUse.Name)
	if !ok || pack == nil {
		a.errorf(packUse.Position, "unknown parameter pack %q", packUse.Name)
		return nil, nil
	}
	fieldByName := make(map[string]ParamPackField, len(pack.Fields))
	for _, field := range pack.Fields {
		fieldByName[field.Name] = field
	}
	values := make(map[string]ast.Expr, len(pack.Fields))
	seen := map[string]bool{}
	for _, arg := range packUse.Args {
		field, ok := fieldByName[arg.Name]
		if !ok {
			a.errorf(arg.Position, "parameter pack %q has no parameter %q", pack.Name, arg.Name)
			continue
		}
		_ = field
		if seen[arg.Name] {
			a.errorf(arg.Position, "parameter pack %q parameter %q is specified more than once", pack.Name, arg.Name)
			continue
		}
		seen[arg.Name] = true
		values[arg.Name] = a.resolveWithArgValueExpr(arg, context)
	}
	for _, field := range pack.Fields {
		if _, ok := values[field.Name]; ok {
			continue
		}
		if field.Decl.DefaultValue == nil {
			continue
		}
		defaultExpr := cloneDefaultArgExpr(field.Decl.DefaultValue)
		if defaultExpr == nil {
			a.errorf(field.Decl.Position, "default value for parameter %q on parameter pack %q uses unsupported syntax in v1", field.Name, pack.Name)
			continue
		}
		values[field.Name] = defaultExpr
	}
	return pack, values
}

func (a *Analyzer) fillMissingAmbientExplicitCallArgs(ft *FuncType, ordered []ast.Expr, filled []bool) {
	if a == nil || ft == nil {
		return
	}
	bindings := a.combinedExplicitBindings()
	if len(bindings) == 0 {
		return
	}
	explicitCount := funcTypeExplicitParamCount(ft)
	for i := 0; i < explicitCount; i++ {
		if i < len(filled) && filled[i] {
			continue
		}
		if i >= len(ft.ExplicitParamNames) || ft.ExplicitParamNames[i] == "" {
			continue
		}
		if expr, ok := bindings[ft.ExplicitParamNames[i]]; ok && expr != nil {
			ordered[i] = expr
			if i < len(filled) {
				filled[i] = true
			}
		}
	}
}

func (a *Analyzer) analyzeArgsScopeStmt(stmt *ast.ArgsScopeStmt) {
	if a == nil || stmt == nil {
		return
	}
	items := orderedArgsScopeItems(stmt.ParamPacks, stmt.Args, stmt.ItemOrder)
	working := a.combinedExplicitBindings()
	tempStmts := make([]ast.Stmt, 0)
	originalBody := append([]ast.Stmt(nil), stmt.Body...)
	materialize := func(name string, expr ast.Expr, shorthand bool, pos lexer.Pos) ast.Expr {
		if shorthand {
			return expr
		}
		tempName := a.nextImplicitTempName(name)
		tempStmts = append(tempStmts, &ast.VarDeclStmt{Position: pos, Name: tempName, Value: expr})
		return &ast.Ident{Position: pos, Name: tempName}
	}
	for _, item := range items {
		if item.IsPack {
			pack, values := a.expandParamPackUseValues(item.Pack, "argument")
			if pack == nil {
				continue
			}
			for _, field := range pack.Fields {
				valueExpr, ok := values[field.Name]
				if !ok || valueExpr == nil {
					continue
				}
				shorthand := false
				for _, arg := range item.Pack.Args {
					if arg.Name == field.Name {
						shorthand = arg.Shorthand
						break
					}
				}
				working[field.Name] = materialize(field.Name, valueExpr, shorthand, item.Pack.Position)
			}
			continue
		}
		valueExpr := a.resolveWithArgValueExpr(item.Arg, "argument")
		working[item.Arg.Name] = materialize(item.Arg.Name, valueExpr, item.Arg.Shorthand, item.Arg.Position)
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
	savedExplicitScopes := a.currentExplicitArgScopes
	a.currentExplicitArgScopes = append(append([]map[string]ast.Expr(nil), savedExplicitScopes...), cloneExplicitBindings(working))
	a.currentScope = scope
	a.withLocalParamPackFrame(func() {
		for _, bodyStmt := range originalBody {
			a.analyzeStmt(bodyStmt)
		}
	})
	a.currentScope = savedScope
	a.currentExplicitArgScopes = savedExplicitScopes
}
