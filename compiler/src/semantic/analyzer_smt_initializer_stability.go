package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) smtScalarInitializerStableWithBinders(expr ast.Expr, walkedNodes *int, identifiers map[string]bool, depth int, binders map[string]bool) bool {
	if expr == nil || a == nil {
		return false
	}
	if !a.smtBoolInitializerVisit(walkedNodes, depth) {
		return false
	}
	if isDirectRefType(a.exprType(expr)) {
		return false
	}
	switch n := expr.(type) {
	case *ast.IntLit, *ast.CharLit:
		return true
	case *ast.ParenExpr:
		return a.smtScalarInitializerStableWithBinders(n.Inner, walkedNodes, identifiers, depth+1, binders)
	case *ast.Ident:
		// An analyzed module constant keeps its resolved identity even when the
		// current lexical scope has no bare-name binding for it.
		if _, constant := a.resolvedPrimitiveConstant(n); constant {
			return true
		}
		if binders[n.Name] {
			return true
		}
		identifiers[n.Name] = true
		return a.smtImmutableScalarIdent(n)
	case *ast.FieldExpr:
		if root, ok := rootIdentName(n); ok {
			identifiers[root] = true
		}
		if n.Field == "count" && !n.Safe {
			if id, ok := stripOptimizationParens(n.Object).(*ast.Ident); ok && !binders[id.Name] && a.smtImmutableIntegerDArrayParameter(id) {
				return true
			}
		}
		if n.Field == "len" && !n.Safe {
			if id, ok := stripOptimizationParens(n.Object).(*ast.Ident); ok && !binders[id.Name] && a.immutableSviewParameter(id) {
				return true
			}
		}
		return a.smtImmutableScalarProjection(n)
	case *ast.IndexExpr:
		tr := &smtTranslator{a: a}
		view, _, ok := tr.stableSviewByteIndexRoot(n, nil)
		if ok && !binders[view.Name] {
			identifiers[view.Name] = true
			return a.smtScalarInitializerStableWithBinders(n.Index, walkedNodes, identifiers, depth+1, binders)
		}
		// Ordinary integer-element darray reads are stable only for an immutable,
		// by-value parameter in a frame that cannot mutate through an alias. The
		// SMT translator can represent these reads as `(select base index)`; other
		// element types (views, structs, enums, floats) have different theories and
		// must keep the conservative runtime-block path.
		container, ok := stripOptimizationParens(n.Object).(*ast.Ident)
		if !ok || container == nil || binders[container.Name] ||
			!a.smtImmutableIntegerDArrayParameter(container) ||
			n.Index2 != nil || n.Fallback != nil || n.LegacyElseFallback ||
			n.AsSpecialize != nil || n.AsOverlayCall != nil || n.AsBuiltinDictGet != nil {
			return false
		}
		identifiers[container.Name] = true
		return a.smtScalarInitializerStableWithBinders(n.Index, walkedNodes, identifiers, depth+1, binders)
	case *ast.CastExpr:
		return isPureNumericValueCast(n) && a.smtScalarInitializerStableWithBinders(n.Operand, walkedNodes, identifiers, depth+1, binders)
	case *ast.UnaryExpr:
		return n != nil && n.LoweredCall == nil && n.Op == lexer.TOKEN_MINUS && a.smtScalarInitializerStableWithBinders(n.Operand, walkedNodes, identifiers, depth+1, binders)
	case *ast.BinaryExpr:
		if n == nil || n.LoweredCall != nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
			lexer.TOKEN_AMPERSAND, lexer.TOKEN_PIPE, lexer.TOKEN_CARET, lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			return a.smtScalarInitializerStableWithBinders(n.Left, walkedNodes, identifiers, depth+1, binders) && a.smtScalarInitializerStableWithBinders(n.Right, walkedNodes, identifiers, depth+1, binders)
		default:
			return false
		}
	default:
		return false
	}
}

// smtImmutableIntegerDArrayParameter admits only reads from a by-value,
// immutable darray parameter whose element type already has the scalar array
// model. Mutable/reference containers and arrays of aggregates stay opaque.
// The frame check keeps the collection snapshot stable across the runtime
// quantifier's iterations, including writes through aliases or helper calls.
func (a *Analyzer) smtImmutableIntegerDArrayParameter(id *ast.Ident) bool {
	if a == nil || id == nil || a.currentScope == nil || a.currentFuncDecl == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(id.Name)
	if !found || sym == nil || sym.Kind != SymbolParam || sym.Node != a.currentFuncDecl ||
		sym.Mutable || symbolRootMutable(sym) || isDirectRefType(sym.Type) {
		return false
	}
	if _, isRef := StripAggregateStateType(sym.Type).(*RefType); isRef {
		return false
	}
	array, ok := StripAggregateStateType(sym.Type).(*DArrayType)
	if !ok || array == nil || !IsNumericType(array.Elem) || IsFloatType(array.Elem) {
		return false
	}
	for _, param := range a.currentFuncDecl.Params {
		if param.Name == id.Name && !param.Mutable && !a.paramIsMutable(param) {
			return a.sviewFrameWriteFree(a.currentFuncDecl)
		}
	}
	return false
}

// A saved predicate call can be expanded only when its value depends on scalar arguments
// and constants. Write-freedom alone would still admit reads through captured pointers.
func (a *Analyzer) smtStableScalarPredicate(decl *ast.FuncDecl, argCount int) bool {
	if !a.sviewFrameCallBodyWriteFree(decl, argCount) {
		return false
	}
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || !IsBoolType(fn.Return) || len(fn.Params) != len(decl.Params) {
		return false
	}
	params := make(map[string]bool, len(decl.Params))
	for i, param := range decl.Params {
		typ := fn.Params[i]
		if isDirectRefType(typ) || (!IsBoolType(typ) && (!IsNumericType(typ) || IsFloatType(typ))) {
			return false
		}
		params[param.Name] = true
	}
	body, ok := a.typedPureReturnExpr(decl)
	if !ok {
		return false
	}
	return !a.walkStaticExpr(body, func(expr ast.Expr) bool {
		switch n := expr.(type) {
		case *ast.BoolLit, *ast.IntLit, *ast.CharLit, *ast.ParenExpr, *ast.TernaryExpr:
			return false
		case *ast.Ident:
			if params[n.Name] {
				return false
			}
			_, constant := a.resolvedPrimitiveConstant(n)
			return !constant
		case *ast.BinaryExpr:
			return n.LoweredCall != nil
		case *ast.UnaryExpr:
			return n.LoweredCall != nil || (n.Op != lexer.TOKEN_NOT && n.Op != lexer.TOKEN_MINUS)
		case *ast.CastExpr:
			return !isPureNumericValueCast(n)
		default:
			return true
		}
	})
}

// smtBoolLocalDefinitionBindings resolves dependencies once after the stable-expression visitor
// has collected every identifier. Later proof points can validate those bindings with one lookup
// per name instead of rescanning the initializer and looking up each name twice.
func (a *Analyzer) smtBoolLocalDefinitionBindings(declScope *Scope, sym *Symbol, names map[string]bool) (map[string]*Symbol, bool) {
	if a == nil || a.currentScope == nil || declScope == nil || sym == nil || sym.Node == nil {
		return nil, false
	}
	if a.smtStaleLocalDefs[sym] {
		return nil, false
	}
	declPos := sym.Node.Pos()
	bindings := make(map[string]*Symbol, len(names))
	for name := range names {
		if name == sym.Name {
			return nil, false
		}
		then, _ := declScope.Lookup(name)
		now, _ := a.currentScope.Lookup(name)
		if then != now {
			return nil, false
		}
		bindings[name] = then
		if then != nil && then.Node != nil && then.Kind == SymbolLocal {
			if p := then.Node.Pos(); p.File == declPos.File && p.Offset > declPos.Offset {
				return nil, false
			}
		}
		if then != nil && then.Kind == SymbolGlobal && then.Mutable {
			return nil, false
		}
	}
	return bindings, true
}

func (a *Analyzer) smtBoolLocalDefinitionBindingsCurrent(sym *Symbol, bindings map[string]*Symbol) bool {
	if a == nil || a.currentScope == nil || sym == nil || a.smtStaleLocalDefs[sym] {
		return false
	}
	for name, binding := range bindings {
		now, _ := a.currentScope.Lookup(name)
		if now != binding {
			return false
		}
	}
	return true
}

func (a *Analyzer) smtBoolInitializerVisit(walkedNodes *int, depth int) bool {
	if walkedNodes == nil || *walkedNodes >= smtBoolInitializerMaxNodes || depth >= smtBoolInitializerMaxDepth {
		return false
	}
	*walkedNodes++
	return true
}

func (a *Analyzer) smtImmutableScalarIdent(id *ast.Ident) bool {
	if a == nil || id == nil || a.currentScope == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(id.Name)
	if !found || sym == nil || sym.Mutable || isDirectRefType(sym.Type) || symbolRootMutable(sym) {
		return false
	}
	if IsBoolType(sym.Type) {
		return true
	}
	return IsNumericType(sym.Type) && !IsFloatType(sym.Type)
}

func (a *Analyzer) smtImmutableScalarProjection(expr *ast.FieldExpr) bool {
	if a == nil || expr == nil || isDirectRefType(a.exprType(expr)) {
		return false
	}
	fieldType := a.exprType(expr)
	if fieldType == nil {
		var ok bool
		fieldType, ok = a.fieldReadResolvedType(expr)
		if !ok {
			return false
		}
	}
	if isDirectRefType(fieldType) || (!IsBoolType(fieldType) && (!IsNumericType(fieldType) || IsFloatType(fieldType))) {
		return false
	}
	rootName, ok := rootIdentName(expr)
	if !ok || a.currentScope == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(rootName)
	if !found || sym == nil || sym.Mutable || symbolRootMutable(sym) || !immutableProjectionRecord(sym.Type, 0) {
		return false
	}
	// Every intermediate projection must also be a by-value part of the record.
	depth := 0
	for cur := ast.Expr(expr); ; {
		field, ok := cur.(*ast.FieldExpr)
		if !ok || field == nil {
			return true
		}
		if depth >= smtBoolInitializerMaxDepth {
			return false
		}
		depth++
		t := a.exprType(field)
		if t == nil {
			t, _ = a.fieldReadResolvedType(field)
		}
		if isDirectRefType(t) {
			return false
		}
		cur = field.Object
	}
}

func isDirectRefType(t Type) bool {
	_, ok := t.(*RefType)
	return ok
}
