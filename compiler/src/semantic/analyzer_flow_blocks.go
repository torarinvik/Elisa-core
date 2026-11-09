package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) analyzeBlock(stmts []ast.Stmt) {
	saved := a.currentScope
	a.analyzeBlockInScope(stmts, NewScope(saved))
}

func (a *Analyzer) analyzeBlockInScope(stmts []ast.Stmt, scope *Scope) {
	saved := a.currentScope
	savedAliasAccesses := a.currentAliasAccesses
	savedAliasBindings := a.currentAliasBindings
	savedAliasCarriers := a.currentAliasCarriers
	savedAliasCarrierFieldOverrides := a.currentAliasCarrierFieldOverrides
	a.inferUntypedDArrayBuilderLocals(stmts, scope)
	a.currentScope = scope
	a.currentAliasAccesses = a.cloneAliasAccesses()
	a.currentAliasBindings = a.cloneAliasBindings()
	a.currentAliasCarriers = a.cloneAliasCarriers()
	a.currentAliasCarrierFieldOverrides = a.cloneAliasCarrierFieldOverrides()
	// docs/107 increment 4: an early-return size guard (`if base.size[mem] < N: return`) leaves its
	// negation `size >= N` holding for the rest of THIS block. Facts pushed below are scoped to the
	// block and dropped at its end.
	savedSizeGuards := len(a.overlaySizeGuards)
	for _, stmt := range stmts {
		a.analyzeStmt(stmt)
		a.applyOverlayFallthroughGuard(stmt)
	}
	a.overlaySizeGuards = a.overlaySizeGuards[:savedSizeGuards]
	savedAliasCarriers, savedAliasCarrierFieldOverrides = a.propagateAliasCarrierBlockEffects(saved, scope, savedAliasCarriers, savedAliasCarrierFieldOverrides)
	a.currentAliasAccesses = savedAliasAccesses
	a.currentAliasBindings = savedAliasBindings
	a.currentAliasCarriers = savedAliasCarriers
	a.currentAliasCarrierFieldOverrides = savedAliasCarrierFieldOverrides
	a.currentScope = saved
}

func (a *Analyzer) inferUntypedDArrayBuilderLocals(stmts []ast.Stmt, scope *Scope) {
	// The prepass only acts on VarDeclStmt entries; a block without one has nothing to do, so skip
	// building the whole-scope-chain type map (a map walk per block).
	hasVarDecl := false
	for _, stmt := range stmts {
		if decl, ok := stmt.(*ast.VarDeclStmt); ok && decl != nil {
			hasVarDecl = true
			break
		}
	}
	if !hasVarDecl {
		return
	}
	known := inferDArrayBuilderKnownTypes(scope)
	// This prepass resolves explicit local types before the ordinary statement walk.
	// Give it a private lexical scope and publish each preceding local there so a
	// region generic such as `Box[arena]` can see an earlier `arena: mutable Arena`
	// in the same nested block (`can:`, `region:`, etc.). Without this shadow scope,
	// region lookup accidentally consulted only the block's parent and rejected a
	// valid Arena owner—or could incorrectly fall through to a same-named outer one.
	savedScope := a.currentScope
	prepassScope := NewScope(scope)
	a.currentScope = prepassScope
	defer func() { a.currentScope = savedScope }()
	definePrepassLocal := func(decl *ast.VarDeclStmt, typ Type) {
		if decl == nil {
			return
		}
		if typ == nil {
			typ = invalidType
		}
		prepassScope.Define(&Symbol{Name: decl.Name, Kind: SymbolLocal, Type: typ, Node: decl, Mutable: decl.Mutable, BindingMutabilityExplicit: decl.BindingExplicit, Ghost: decl.Ghost})
	}
	for i, stmt := range stmts {
		decl, ok := stmt.(*ast.VarDeclStmt)
		if !ok || decl == nil {
			continue
		}
		if decl.Type != nil {
			typ := a.resolveType(decl.Type)
			known.set(decl.Name, typ)
			definePrepassLocal(decl, typ)
			continue
		}
		if !semanticListLiteralExpr(decl.Value) {
			if typ := inferKnownDArrayBuilderExprType(decl.Value, known); typ != nil {
				known.set(decl.Name, typ)
			}
			definePrepassLocal(decl, known.get(decl.Name))
			continue
		}
		elem := inferDArrayBuilderElemTypeExpr(decl.Value)
		sawDArrayBuilderUse := false
		for j := i + 1; elem == nil && j < len(stmts); j++ {
			if shadow, ok := stmts[j].(*ast.VarDeclStmt); ok && shadow.Name == decl.Name {
				break
			}
			if stmtUsesUntypedDArrayBuilderLocal(stmts[j], decl.Name) {
				sawDArrayBuilderUse = true
			}
			elem = inferDArrayElemTypeFromUse(stmts[j], decl.Name, known)
		}
		// Return-type-driven fallback: when no push/extend pins the element type (e.g. the
		// builder pushes computed values like `out.push(i.i64() * 2)` whose type the syntactic
		// scan can't resolve), but the local is the value handed back from a `-> darray[T]`
		// function, take T straight from the return annotation. This makes the canonical
		// loop-builder shape (`def mk() -> darray[i64]: out = []; while ...: out.push(...); return out`)
		// infer with zero ceremony. A mismatched push is still caught later when the push is
		// type-checked against the now-typed darray, so this can only widen acceptance soundly.
		if elem == nil && a.currentFuncType != nil {
			if ret, ok := a.currentFuncType.Return.(*DArrayType); ok && ret != nil && ret.Elem != nil && !IsInvalidType(ret.Elem) {
				for j := i + 1; j < len(stmts); j++ {
					if shadow, ok := stmts[j].(*ast.VarDeclStmt); ok && shadow.Name == decl.Name {
						break
					}
					if stmtReturnsBuilderLocal(stmts[j], decl.Name) {
						elem = astTypeExprForBuiltinMethodRewrite(decl.Position, ret.Elem)
						break
					}
				}
			}
		}
		if elem == nil {
			if sawDArrayBuilderUse {
				if a.ambiguousDArrayBuilders == nil {
					a.ambiguousDArrayBuilders = map[*ast.VarDeclStmt]bool{}
				}
				a.ambiguousDArrayBuilders[decl] = true
			}
			definePrepassLocal(decl, known.get(decl.Name))
			continue
		}
		decl.Mutable = true
		decl.Type = &ast.MutableType{
			Position: decl.Position,
			Elem: &ast.BuiltinTypeExpr{
				Position: decl.Position,
				Name:     "darray",
				TypeArgs: []ast.TypeExpr{elem},
			},
		}
		typ := a.resolveType(decl.Type)
		known.set(decl.Name, typ)
		definePrepassLocal(decl, typ)
	}
}

// darrayBuilderKnownTypes is the builder prepass's view of binding types: the block's own
// declarations (local), else the innermost local or parameter of the scope chain with a valid
// type. It used to be a map snapshot of every binding of the whole chain -- the global scope's
// symbols walked too -- taken for every block that declares a local; lookups are all it needs.
type darrayBuilderKnownTypes struct {
	scope *Scope
	local map[string]Type
}

func inferDArrayBuilderKnownTypes(scope *Scope) *darrayBuilderKnownTypes {
	return &darrayBuilderKnownTypes{scope: scope, local: map[string]Type{}}
}

func (k *darrayBuilderKnownTypes) get(name string) Type {
	if k == nil {
		return nil
	}
	if typ, ok := k.local[name]; ok {
		return typ
	}
	for cur := k.scope; cur != nil; cur = cur.Parent {
		sym := cur.Symbols[name]
		if sym == nil || sym.Type == nil || IsInvalidType(sym.Type) {
			continue
		}
		switch sym.Kind {
		case SymbolLocal, SymbolParam:
			return sym.Type
		}
	}
	return nil
}

func (k *darrayBuilderKnownTypes) set(name string, typ Type) {
	k.local[name] = typ
}

func stmtUsesUntypedDArrayBuilderLocal(stmt ast.Stmt, name string) bool {
	found := false
	var visitExpr func(ast.Expr)
	visitExpr = func(expr ast.Expr) {
		if found || expr == nil {
			return
		}
		switch e := expr.(type) {
		case *ast.CallExpr:
			if field, ok := stripParenExpr(e.Func).(*ast.FieldExpr); ok && field != nil {
				if ident, ok := stripParenExpr(field.Object).(*ast.Ident); ok && ident != nil && ident.Name == name {
					switch field.Field {
					case "push", "extend", "reserve", "resize", "clear", "truncate", "as_sview", "as_cstr":
						found = true
						return
					}
				}
			}
			visitExpr(e.Func)
			for _, arg := range e.Args {
				visitExpr(arg)
			}
		case *ast.FieldExpr:
			if ident, ok := stripParenExpr(e.Object).(*ast.Ident); ok && ident != nil && ident.Name == name {
				switch e.Field {
				case "count", "capacity", "items":
					found = true
					return
				}
			}
			visitExpr(e.Object)
		case *ast.IndexExpr:
			if ident, ok := stripParenExpr(e.Object).(*ast.Ident); ok && ident != nil && ident.Name == name {
				found = true
				return
			}
			visitExpr(e.Object)
			visitExpr(e.Index)
		case *ast.SliceExpr:
			if ident, ok := stripParenExpr(e.Object).(*ast.Ident); ok && ident != nil && ident.Name == name {
				found = true
				return
			}
			visitExpr(e.Object)
			visitExpr(e.Start)
			visitExpr(e.End)
		case *ast.BinaryExpr:
			visitExpr(e.Left)
			visitExpr(e.Right)
		case *ast.UnaryExpr:
			visitExpr(e.Operand)
		case *ast.ParenExpr:
			visitExpr(e.Inner)
		}
	}
	var visitStmt func(ast.Stmt)
	visitStmts := func(list []ast.Stmt) {
		for _, st := range list {
			if found {
				return
			}
			visitStmt(st)
		}
	}
	// Mirror inferDArrayElemTypeFromUse's recursion: a builder-local use nested inside a
	// loop/conditional must count too, so a loop-filled local is not misclassified as unused.
	visitStmt = func(st ast.Stmt) {
		if found || st == nil {
			return
		}
		switch s := st.(type) {
		case *ast.ExprStmt:
			visitExpr(s.Expr)
		case *ast.VarDeclStmt:
			visitExpr(s.Value)
		case *ast.AssignStmt:
			visitExpr(s.Value)
		case *ast.ReturnStmt:
			visitExpr(s.Value)
		case *ast.IfStmt:
			visitStmts(s.Then)
			for _, elif := range s.Elifs {
				visitStmts(elif.Body)
			}
			visitStmts(s.Else)
		case *ast.WhileStmt:
			visitStmts(s.Body)
		case *ast.ForStmt:
			visitStmts(s.Body)
		case *ast.IterForStmt:
			visitStmts(s.Body)
		case *ast.ScopeStmt:
			visitStmts(s.Body)
		case *ast.CanStmt:
			visitStmts(s.Body)
		case *ast.RegionStmt:
			visitStmts(s.Body)
		case *ast.InStoreStmt:
			visitStmts(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				visitStmts(arm.Body)
			}
		}
	}
	visitStmt(stmt)
	return found
}

// stmtReturnsBuilderLocal reports whether stmt (recursing into nested control-flow bodies)
// contains a `return <name>` handing back the builder local directly. Used by the return-type
// fallback in inferUntypedDArrayBuilderLocals to decide whether the function's `-> darray[T]`
// annotation may pin an untyped empty-literal local's element type.
func stmtReturnsBuilderLocal(stmt ast.Stmt, name string) bool {
	found := false
	var visitStmt func(ast.Stmt)
	visitStmts := func(list []ast.Stmt) {
		for _, st := range list {
			if found {
				return
			}
			visitStmt(st)
		}
	}
	visitStmt = func(st ast.Stmt) {
		if found || st == nil {
			return
		}
		switch s := st.(type) {
		case *ast.ReturnStmt:
			if id, ok := stripParenExpr(s.Value).(*ast.Ident); ok && id != nil && id.Name == name {
				found = true
			}
		case *ast.IfStmt:
			visitStmts(s.Then)
			for _, elif := range s.Elifs {
				visitStmts(elif.Body)
			}
			visitStmts(s.Else)
		case *ast.WhileStmt:
			visitStmts(s.Body)
		case *ast.ForStmt:
			visitStmts(s.Body)
		case *ast.IterForStmt:
			visitStmts(s.Body)
		case *ast.ScopeStmt:
			visitStmts(s.Body)
		case *ast.CanStmt:
			visitStmts(s.Body)
		case *ast.RegionStmt:
			visitStmts(s.Body)
		case *ast.InStoreStmt:
			visitStmts(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				visitStmts(arm.Body)
			}
		}
	}
	visitStmt(stmt)
	return found
}

func semanticListLiteralExpr(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	_, ok := value.(*ast.ListLitExpr)
	return ok
}

func inferDArrayBuilderElemTypeExpr(value ast.Expr) ast.TypeExpr {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	list, ok := value.(*ast.ListLitExpr)
	if !ok || list == nil {
		return nil
	}
	for _, elem := range list.Elems {
		if inferred := inferSimpleLiteralTypeExpr(elem); inferred != nil {
			return inferred
		}
	}
	return nil
}

func inferDArrayElemTypeFromUse(stmt ast.Stmt, name string, known *darrayBuilderKnownTypes) ast.TypeExpr {
	var found ast.TypeExpr
	var visitExpr func(ast.Expr)
	visitExpr = func(expr ast.Expr) {
		if found != nil || expr == nil {
			return
		}
		switch e := expr.(type) {
		case *ast.CallExpr:
			if field, ok := e.Func.(*ast.FieldExpr); ok && field != nil {
				if ident, ok := field.Object.(*ast.Ident); ok && ident.Name == name {
					switch field.Field {
					case "push":
						if len(e.Args) == 1 {
							if list, ok := stripParenExpr(e.Args[0]).(*ast.ListLitExpr); ok && list != nil {
								found = inferDArrayBuilderElemTypeExpr(list)
							} else {
								found = inferSimpleLiteralTypeExpr(e.Args[0])
								if found == nil {
									found = inferDArrayBuilderElemTypeFromKnownExpr(e.Args[0], known)
								}
							}
						}
						return
					case "extend":
						if len(e.Args) == 1 {
							if list, ok := stripParenExpr(e.Args[0]).(*ast.ListLitExpr); ok && list != nil {
								found = inferDArrayBuilderElemTypeExpr(list)
							} else {
								found = inferDArrayExtendElemTypeFromKnownExpr(e.Args[0], known)
							}
						}
						return
					}
				}
			}
			visitExpr(e.Func)
			for _, arg := range e.Args {
				visitExpr(arg)
			}
		case *ast.BinaryExpr:
			visitExpr(e.Left)
			visitExpr(e.Right)
		case *ast.UnaryExpr:
			visitExpr(e.Operand)
		case *ast.ParenExpr:
			visitExpr(e.Inner)
		}
	}
	var visitStmt func(ast.Stmt)
	visitStmts := func(list []ast.Stmt) {
		for _, st := range list {
			if found != nil {
				return
			}
			visitStmt(st)
		}
	}
	// Descend into nested control-flow bodies: a builder local is very often filled by a
	// `.push(...)` inside a `while`/`for`/`if`/`match`/`can` body, not just straight-line at
	// the declaration's level. Without this recursion the empty-literal element type could not
	// be inferred for the canonical loop-builder shape (`out = []; while ...: out.push(x)`).
	visitStmt = func(st ast.Stmt) {
		if found != nil || st == nil {
			return
		}
		switch s := st.(type) {
		case *ast.ExprStmt:
			visitExpr(s.Expr)
		case *ast.VarDeclStmt:
			visitExpr(s.Value)
		case *ast.AssignStmt:
			visitExpr(s.Value)
		case *ast.ReturnStmt:
			visitExpr(s.Value)
		case *ast.IfStmt:
			visitStmts(s.Then)
			for _, elif := range s.Elifs {
				visitStmts(elif.Body)
			}
			visitStmts(s.Else)
		case *ast.WhileStmt:
			visitStmts(s.Body)
		case *ast.ForStmt:
			visitStmts(s.Body)
		case *ast.IterForStmt:
			visitStmts(s.Body)
		case *ast.ScopeStmt:
			visitStmts(s.Body)
		case *ast.CanStmt:
			visitStmts(s.Body)
		case *ast.RegionStmt:
			visitStmts(s.Body)
		case *ast.InStoreStmt:
			visitStmts(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				visitStmts(arm.Body)
			}
		}
	}
	visitStmt(stmt)
	return found
}
