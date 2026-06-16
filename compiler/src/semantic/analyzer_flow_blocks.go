package semantic

import (
	"fmt"
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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
	a.inferUntypedDArrayBuilderLocals(stmts, scope)
	a.currentScope = scope
	a.currentAliasAccesses = a.cloneAliasAccesses()
	a.currentAliasBindings = a.cloneAliasBindings()
	a.currentAliasCarriers = a.cloneAliasCarriers()
	for _, stmt := range stmts {
		a.analyzeStmt(stmt)
	}
	a.currentAliasAccesses = savedAliasAccesses
	a.currentAliasBindings = savedAliasBindings
	a.currentAliasCarriers = savedAliasCarriers
	a.currentScope = saved
}

func (a *Analyzer) inferUntypedDArrayBuilderLocals(stmts []ast.Stmt, scope *Scope) {
	known := inferDArrayBuilderKnownTypes(scope)
	for i, stmt := range stmts {
		decl, ok := stmt.(*ast.VarDeclStmt)
		if !ok || decl == nil {
			continue
		}
		if decl.Type != nil {
			known[decl.Name] = a.resolveType(decl.Type)
			continue
		}
		if !semanticListLiteralExpr(decl.Value) {
			if typ := inferKnownDArrayBuilderExprType(decl.Value, known); typ != nil {
				known[decl.Name] = typ
			}
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
		known[decl.Name] = a.resolveType(decl.Type)
	}
}

func inferDArrayBuilderKnownTypes(scope *Scope) map[string]Type {
	known := map[string]Type{}
	var seed func(*Scope)
	seed = func(cur *Scope) {
		if cur == nil {
			return
		}
		seed(cur.Parent)
		for name, sym := range cur.Symbols {
			if sym == nil || sym.Type == nil || IsInvalidType(sym.Type) {
				continue
			}
			switch sym.Kind {
			case SymbolLocal, SymbolParam:
				known[name] = sym.Type
			}
		}
	}
	seed(scope)
	return known
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

func inferDArrayElemTypeFromUse(stmt ast.Stmt, name string, known map[string]Type) ast.TypeExpr {
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

func inferDArrayBuilderElemTypeFromKnownExpr(expr ast.Expr, known map[string]Type) ast.TypeExpr {
	typ := inferKnownDArrayBuilderExprType(expr, known)
	if typ == nil || IsInvalidType(typ) {
		return nil
	}
	return astTypeExprForBuiltinMethodRewrite(expr.Pos(), typ)
}

func inferKnownDArrayBuilderExprType(expr ast.Expr, known map[string]Type) Type {
	expr = stripParenExpr(expr)
	switch e := expr.(type) {
	case *ast.Ident:
		return inferKnownIdentType(e.Name, known)
	case *ast.IndexExpr:
		source := inferKnownDArrayBuilderExprType(e.Object, known)
		if ref, ok := source.(*RefType); ok && ref != nil {
			source = ref.Elem
		}
		switch t := StripAggregateStateType(source).(type) {
		case *DArrayType:
			if t != nil {
				return t.Elem
			}
		case *ViewType:
			if t != nil {
				return t.Elem
			}
		case *ArrayType:
			if t != nil {
				return t.Elem
			}
		}
		return nil
	default:
		return nil
	}
}

func inferKnownIdentType(name string, known map[string]Type) Type {
	typ := known[name]
	if ref, ok := typ.(*RefType); ok && ref != nil {
		typ = ref.Elem
	}
	return typ
}

func inferDArrayExtendElemTypeFromKnownExpr(expr ast.Expr, known map[string]Type) ast.TypeExpr {
	expr = stripParenExpr(expr)
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return nil
	}
	typ := known[ident.Name]
	if ref, ok := typ.(*RefType); ok && ref != nil {
		typ = ref.Elem
	}
	switch t := StripAggregateStateType(typ).(type) {
	case *DArrayType:
		if t != nil {
			return astTypeExprForBuiltinMethodRewrite(expr.Pos(), t.Elem)
		}
	case *ViewType:
		if t != nil {
			return astTypeExprForBuiltinMethodRewrite(expr.Pos(), t.Elem)
		}
	case *ArrayType:
		if t != nil {
			return astTypeExprForBuiltinMethodRewrite(expr.Pos(), t.Elem)
		}
	}
	return nil
}

func inferSimpleLiteralTypeExpr(expr ast.Expr) ast.TypeExpr {
	expr = stripParenExpr(expr)
	pos := expr.Pos()
	switch lit := expr.(type) {
	case *ast.IntLit:
		switch lit.Suffix {
		case "":
			return &ast.NamedType{Position: pos, Name: "int"}
		case "u":
			return &ast.NamedType{Position: pos, Name: "usize"}
		case "i":
			return &ast.NamedType{Position: pos, Name: "int"}
		default:
			return &ast.NamedType{Position: pos, Name: lit.Suffix}
		}
	case *ast.FloatLit:
		if lit.Suffix != "" {
			return &ast.NamedType{Position: pos, Name: lit.Suffix}
		}
		return &ast.NamedType{Position: pos, Name: "f64"}
	case *ast.BoolLit:
		return &ast.NamedType{Position: pos, Name: "bool"}
	case *ast.CharLit:
		return &ast.NamedType{Position: pos, Name: "char"}
	}
	return nil
}

func (a *Analyzer) analyzeBlockWithRegionClone(stmts []ast.Stmt, scope *Scope) {
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedCheckpoints := a.currentCheckpoints
	savedRegionRefs := a.currentRegionRefs
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentCheckpoints = a.cloneCheckpointStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	a.analyzeBlockInScope(stmts, scope)
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentCheckpoints = savedCheckpoints
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
}

type affineFlowSnapshot struct {
	Affine                map[affineValueKey]affineValueState
	BorrowedOwnerRefs     map[*Symbol]borrowedOwnerRefState
	FunctionValues        map[*Symbol]*FuncType
	SpecializedValueTypes map[*Symbol]Type
	ValueBindings         map[*Symbol]ast.Expr
	StorageViewDeps       map[*Symbol]storageViewDependencyState
}

type borrowedOwnerRefSummaryTarget struct {
	ParamIndex int
	Path       []borrowReturnAnnotationStep
}

type borrowedOwnerRefSummary struct {
	HasDirect bool
	Direct    borrowedOwnerRefSummaryTarget
	Fields    map[string]borrowedOwnerRefSummary
}

func (a *Analyzer) analyzeBlockWithAffineClone(stmts []ast.Stmt, scope *Scope) affineFlowSnapshot {
	return a.analyzeBlockWithAffineClonePrepared(stmts, scope, nil)
}

func (a *Analyzer) analyzeBlockWithConditionAffineClone(stmts []ast.Stmt, parent *Scope, cond ast.Expr, truthy bool) affineFlowSnapshot {
	scope := a.refinedScopeForCondition(parent, cond, truthy)
	return a.analyzeBlockWithAffineClonePrepared(stmts, scope, func() {
		a.applyConditionRefinementsInternal(scope, cond, truthy, true)
		a.applyIndexBoundsFactsForCondition(cond, truthy)
		a.applyViewStaticLenForCondition(cond, truthy)
	})
}

// optionalBindBoundType resolves the type bound by an `if VALUE is NAME:`
// condition. A slice operand (`if arr[a:b] is s:`) is a bounds-checked slice:
// it is marked checked (so codegen emits the runtime bounds test) and binds the
// bounded view, taking the else arm when out of range. All other operands fall
// back to the standard optional / nullable-reference unwrap.
func (a *Analyzer) optionalBindBoundType(value ast.Expr, valueType Type) (Type, bool) {
	if slice, ok := stripOptimizationParens(value).(*ast.SliceExpr); ok && slice != nil {
		a.checkedSliceExprs[slice] = true
		if opt, isOpt := valueType.(*OptionalType); isOpt && opt != nil {
			return opt.Value, true
		}
		return valueType, true
	}
	return conditionOptionalBindType(valueType)
}

func conditionOptionalBindType(valueType Type) (Type, bool) {
	switch t := valueType.(type) {
	case *OptionalType:
		if t == nil || t.Value == nil {
			return nil, false
		}
		return t.Value, true
	case *RefType:
		if t == nil || t.State != RefStateNullable {
			return nil, false
		}
		return cloneRefTypeWithState(t, RefStateNonNull), true
	default:
		return nil, false
	}
}

func unwrapDirectConditionPattern(expr ast.Expr) (*ast.BinaryExpr, ast.Expr, ast.MatchPattern, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return unwrapDirectConditionPattern(n.Inner)
	case *ast.BinaryExpr:
		if n.Op != lexer.TOKEN_IS {
			return nil, nil, nil, false
		}
		right := n.Right
		if alias, ok := right.(*ast.IsAliasExpr); ok && alias != nil {
			right = alias.Target
		}
		switch testExpr := right.(type) {
		case *ast.StructTestExpr:
			if testExpr == nil || testExpr.Pattern == nil {
				return nil, nil, nil, false
			}
			return n, n.Left, testExpr.Pattern, true
		case *ast.VariantTestExpr:
			if testExpr == nil || testExpr.Pattern == nil {
				return nil, nil, nil, false
			}
			return n, n.Left, testExpr.Pattern, true
		default:
			return nil, nil, nil, false
		}
	default:
		return nil, nil, nil, false
	}
}

func (a *Analyzer) conditionAliasBindingType(leftExpr ast.Expr, target ast.Expr, leftType Type) (string, Type, bool) {
	alias, ok := target.(*ast.IsAliasExpr)
	if !ok || alias == nil || alias.Alias == "" || alias.Alias == "_" {
		return "", nil, false
	}
	if enumType, variant, ok := a.resolveEnumVariantIsTarget(alias.Target); ok && enumType != nil && variant != nil {
		matchableEnum, _, ok := resolveMatchableEnumType(leftType)
		if ok && matchableEnum != nil && matchableEnum.Name == enumType.Name {
			return alias.Alias, variant.PackedViewType(matchableEnum), true
		}
	}
	if targetBase, targetState, ok := a.resolveNamedStateIsTarget(alias.Target); ok && targetBase != nil && targetState != nil {
		leftBase, ok := namedStateStructBase(leftType)
		if ok && leftBase != nil && leftBase.Name == targetBase.Name {
			currentState, ok := namedStateCurrentArg(leftType)
			if !ok || currentState == nil {
				currentState = fullNamedStateType(leftBase)
			}
			refinedState := intersectNamedStateType(currentState, targetState, leftBase.NamedStateCases)
			if refinedState != nil {
				return alias.Alias, instantiateNamedStateStructLiteralType(leftBase, leftType, refinedState), true
			}
		}
	}
	// docs/77 §2: `e is Statement s` — a bare-category target gives the binder the NARROWED
	// category type, so it can be passed where the sub-category is required.
	if matchableEnum, _, ok := resolveMatchableEnumType(StripAggregateStateType(leftType)); ok && enumIsHierarchical(matchableEnum) {
		if category, ok := a.resolveEnumCategoryIsTarget(alias.Target); ok && enumDescendsFrom(category, matchableEnum) {
			return alias.Alias, category, true
		}
	}
	return alias.Alias, leftType, true
}

func (a *Analyzer) collectConditionStructPatternBindingTypes(pattern ast.MatchPattern, expected Type, out map[string]Type) {
	if a == nil || pattern == nil || expected == nil || out == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchBindPattern:
		if p.Name == "" || p.Name == "_" {
			return
		}
		if a.analyzePredicateMatchPattern(p, expected, nil) {
			return
		}
		if prev, ok := out[p.Name]; ok {
			if !SameType(prev, expected) {
				a.errorf(p.Pos(), "condition binding %q has inconsistent types %s and %s", p.Name, prev, expected)
			}
			return
		}
		out[p.Name] = expected
	case *ast.MatchStructPattern:
		if a.analyzeCountMatchPattern(p, expected) {
			return
		}
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			a.collectConditionStructPatternBindingTypes(arg.Pattern, fields[i].Type, out)
		}
	case *ast.MatchListPattern:
		elemType, ok := SequenceMatchElementType(expected)
		if !ok {
			return
		}
		for _, elem := range p.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				continue
			}
			a.collectConditionStructPatternBindingTypes(elem, elemType, out)
		}
	case *ast.MatchOrPattern:
		bindings, ok := a.collectOrPatternBindingTypes(p, expected)
		if !ok {
			return
		}
		for name, typ := range bindings {
			if prev, exists := out[name]; exists {
				if !SameType(prev, typ) {
					a.errorf(p.Pos(), "condition binding %q has inconsistent types %s and %s", name, prev, typ)
				}
				continue
			}
			out[name] = typ
		}
	case *ast.MatchVariantPattern:
		switch variantBase := expected.(type) {
		case *EnumType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolvePartialMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				a.collectConditionStructPatternBindingTypes(arg.Pattern, variant.Payload[i], out)
			}
		}
	}
}

func (a *Analyzer) collectOrPatternBindingTypes(pattern *ast.MatchOrPattern, expected Type) (map[string]Type, bool) {
	if pattern == nil {
		return nil, true
	}
	var baseline map[string]Type
	for i, option := range pattern.Options {
		current := map[string]Type{}
		a.collectConditionStructPatternBindingTypes(option, expected, current)
		if i == 0 {
			baseline = current
			continue
		}
		if !samePatternBindingTypeMap(baseline, current) {
			a.errorf(option.Pos(), "or-pattern alternatives must bind the same names with compatible types")
			return baseline, false
		}
	}
	if baseline == nil {
		baseline = map[string]Type{}
	}
	return baseline, true
}

func samePatternBindingTypeMap(left, right map[string]Type) bool {
	if len(left) != len(right) {
		return false
	}
	for name, leftType := range left {
		rightType, ok := right[name]
		if !ok || !SameType(leftType, rightType) {
			return false
		}
	}
	return true
}

func (a *Analyzer) collectGuaranteedTruthyConditionBindingTypes(expr ast.Expr) map[string]Type {
	if a == nil || expr == nil {
		return nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectGuaranteedTruthyConditionBindingTypes(n.Inner)
	case *ast.OptionalBindExpr:
		if n.Name == "" || n.Name == "_" {
			return nil
		}
		valueType := a.exprTypes[n.Value]
		if valueType == nil {
			valueType = a.analyzeExpr(n.Value)
		}
		boundType, ok := a.optionalBindBoundType(n.Value, valueType)
		if !ok {
			return nil
		}
		return map[string]Type{n.Name: boundType}
	case *ast.UnaryExpr:
		return nil
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			left := a.collectGuaranteedTruthyConditionBindingTypes(n.Left)
			right := a.collectGuaranteedTruthyConditionBindingTypes(n.Right)
			if len(left) == 0 {
				return right
			}
			if len(right) == 0 {
				return left
			}
			out := make(map[string]Type, len(left)+len(right))
			for name, typ := range left {
				out[name] = typ
			}
			for name, typ := range right {
				if prev, ok := out[name]; ok && !SameType(prev, typ) {
					a.errorf(n.Pos(), "condition binding %q has inconsistent types %s and %s", name, prev, typ)
					continue
				}
				out[name] = typ
			}
			return out
		case lexer.TOKEN_OR:
			left := a.collectGuaranteedTruthyConditionBindingTypes(n.Left)
			right := a.collectGuaranteedTruthyConditionBindingTypes(n.Right)
			if len(left) == 0 || len(right) == 0 {
				return nil
			}
			out := map[string]Type{}
			for name, leftType := range left {
				rightType, ok := right[name]
				if !ok || !SameType(leftType, rightType) {
					continue
				}
				out[name] = leftType
			}
			if len(out) == 0 {
				return nil
			}
			return out
		}
		if n.Op == lexer.TOKEN_IS {
			valueType := a.exprTypes[n.Left]
			if valueType == nil {
				valueType = a.analyzeExpr(n.Left)
			}
			if name, typ, ok := a.conditionAliasBindingType(n.Left, n.Right, valueType); ok && typ != nil {
				return map[string]Type{name: typ}
			}
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectConditionPattern(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return nil
	}
	valueType := a.exprTypes[valueExpr]
	if valueType == nil {
		valueType = a.analyzeExpr(valueExpr)
	}
	out := map[string]Type{}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary != nil {
		if name, typ, ok := a.conditionAliasBindingType(valueExpr, binary.Right, valueType); ok && typ != nil {
			out[name] = typ
		}
	}
	a.collectConditionStructPatternBindingTypes(pattern, valueType, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *Analyzer) collectPossibleTruthyConditionBindingTypes(expr ast.Expr) map[string]Type {
	if a == nil || expr == nil {
		return nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectPossibleTruthyConditionBindingTypes(n.Inner)
	case *ast.OptionalBindExpr:
		if n.Name == "" || n.Name == "_" {
			return nil
		}
		valueType := a.exprTypes[n.Value]
		if valueType == nil {
			valueType = a.analyzeExpr(n.Value)
		}
		boundType, ok := a.optionalBindBoundType(n.Value, valueType)
		if !ok {
			return nil
		}
		return map[string]Type{n.Name: boundType}
	case *ast.UnaryExpr:
		return nil
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			left := a.collectPossibleTruthyConditionBindingTypes(n.Left)
			right := a.collectPossibleTruthyConditionBindingTypes(n.Right)
			if len(left) == 0 {
				return right
			}
			if len(right) == 0 {
				return left
			}
			out := make(map[string]Type, len(left)+len(right))
			for name, typ := range left {
				out[name] = typ
			}
			for name, typ := range right {
				if _, ok := out[name]; !ok {
					out[name] = typ
				}
			}
			return out
		}
		if n.Op == lexer.TOKEN_IS {
			valueType := a.exprTypes[n.Left]
			if valueType == nil {
				valueType = a.analyzeExpr(n.Left)
			}
			if name, typ, ok := a.conditionAliasBindingType(n.Left, n.Right, valueType); ok && typ != nil {
				return map[string]Type{name: typ}
			}
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectConditionPattern(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return nil
	}
	valueType := a.exprTypes[valueExpr]
	if valueType == nil {
		valueType = a.analyzeExpr(valueExpr)
	}
	out := map[string]Type{}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary != nil {
		if name, typ, ok := a.conditionAliasBindingType(valueExpr, binary.Right, valueType); ok && typ != nil {
			out[name] = typ
		}
	}
	a.collectConditionStructPatternBindingTypes(pattern, valueType, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *Analyzer) recordConditionalBindingHints(scope *Scope, expr ast.Expr, truthy bool) {
	if a == nil || scope == nil || !truthy || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.recordConditionalBindingHints(scope, n.Inner, truthy)
		return
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.recordConditionalBindingHints(scope, n.Operand, !truthy)
		}
		return
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.recordConditionalBindingHints(scope, n.Left, true)
			a.recordConditionalBindingHints(scope, n.Right, true)
			return
		case lexer.TOKEN_OR:
			leftPossible := a.collectPossibleTruthyConditionBindingTypes(n.Left)
			rightPossible := a.collectPossibleTruthyConditionBindingTypes(n.Right)
			guaranteed := a.collectGuaranteedTruthyConditionBindingTypes(n)
			allNames := map[string]bool{}
			for name := range leftPossible {
				allNames[name] = true
			}
			for name := range rightPossible {
				allNames[name] = true
			}
			for name := range allNames {
				if _, ok := guaranteed[name]; ok {
					continue
				}
				leftType, leftOK := leftPossible[name]
				rightType, rightOK := rightPossible[name]
				hint := ""
				switch {
				case leftOK && rightOK && !SameType(leftType, rightType):
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` branches do not agree on that binding: left branch binds it as %s, while right branch binds it as %s; use different bind names or restructure the condition", name, leftType.String(), rightType.String())
				case leftOK && !rightOK:
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` branches do not agree on that binding: left branch binds it as %s, while right branch does not bind it", name, leftType.String())
				case !leftOK && rightOK:
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` branches do not agree on that binding: left branch does not bind it, while right branch binds it as %s", name, rightType.String())
				case leftOK || rightOK:
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` condition bindings are only introduced when every successful branch binds that name", name)
				}
				if hint != "" {
					scope.ConditionalBindingHints[name] = hint
				}
			}
			a.recordConditionalBindingHints(scope, n.Left, true)
			a.recordConditionalBindingHints(scope, n.Right, true)
			return
		}
	}
}

func (a *Analyzer) bindConditionPatternLocals(scope *Scope, expr ast.Expr, truthy bool) {
	if a == nil || scope == nil || !truthy {
		return
	}
	a.recordConditionalBindingHints(scope, expr, truthy)
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.bindConditionPatternLocals(scope, n.Inner, truthy)
		return
	case *ast.OptionalBindExpr:
		if n.Name == "" || n.Name == "_" {
			return
		}
		valueType := a.exprTypes[n.Value]
		if valueType == nil {
			valueType = a.analyzeExprInScope(n.Value, scope)
		}
		boundType, ok := a.optionalBindBoundType(n.Value, valueType)
		if !ok {
			return
		}
		sym := &Symbol{Name: n.Name, Kind: SymbolLocal, Type: boundType, Node: n, Mutable: false}
		a.defineLocalInScope(scope, sym, n.Pos())
		return
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.bindConditionPatternLocals(scope, n.Operand, !truthy)
		}
		return
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.bindConditionPatternLocals(scope, n.Left, true)
			a.bindConditionPatternLocals(scope, n.Right, true)
			return
		case lexer.TOKEN_OR:
			for name, typ := range a.collectGuaranteedTruthyConditionBindingTypes(n) {
				sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: n, Mutable: false}
				a.defineLocalInScope(scope, sym, n.Pos())
			}
			return
		}
		if n.Op == lexer.TOKEN_IS {
			valueType := a.exprTypes[n.Left]
			if valueType == nil {
				valueType = a.analyzeExprInScope(n.Left, scope)
			}
			if name, typ, ok := a.conditionAliasBindingType(n.Left, n.Right, valueType); ok && typ != nil {
				sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: n.Right, Mutable: false}
				a.defineLocalInScope(scope, sym, n.Right.Pos())
				a.recordValueBinding(sym, n.Left)
				a.recordBorrowedOwnerRefBinding(sym, n.Left)
				a.recordFunctionValueBinding(sym, n.Left)
				a.recordImmutableSymbolOptimizationFacts(sym, n.Left)
				a.recordRegionRefBinding(sym, n.Left)
			}
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectConditionPattern(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return
	}
	valueType := a.exprTypes[valueExpr]
	if valueType == nil {
		valueType = a.analyzeExprInScope(valueExpr, scope)
	}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary != nil {
		if name, typ, ok := a.conditionAliasBindingType(valueExpr, binary.Right, valueType); ok && typ != nil {
			sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: binary.Right, Mutable: false}
			a.defineLocalInScope(scope, sym, binary.Right.Pos())
			if valueExpr != nil {
				a.recordValueBinding(sym, valueExpr)
				a.recordBorrowedOwnerRefBinding(sym, valueExpr)
				a.recordFunctionValueBinding(sym, valueExpr)
				a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
				a.recordRegionRefBinding(sym, valueExpr)
			}
		}
	}
	a.bindConditionStructPatternLocals(scope, pattern, valueType, valueExpr)
}

func (a *Analyzer) bindConditionStructPatternLocals(scope *Scope, pattern ast.MatchPattern, expected Type, valueExpr ast.Expr) {
	if a == nil || scope == nil || pattern == nil || expected == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchBindPattern:
		if p.Name == "_" {
			return
		}
		if a.analyzePredicateMatchPattern(p, expected, valueExpr) {
			return
		}
		sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: expected, Node: p, Mutable: false}
		a.defineLocalInScope(scope, sym, p.Pos())
		if valueExpr != nil {
			a.recordValueBinding(sym, valueExpr)
			a.recordBorrowedOwnerRefBinding(sym, valueExpr)
			a.recordFunctionValueBinding(sym, valueExpr)
			a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
			a.recordRegionRefBinding(sym, valueExpr)
		}
	case *ast.MatchStructPattern:
		if a.analyzeCountMatchPattern(p, expected) {
			return
		}
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			var fieldExpr ast.Expr
			if valueExpr != nil {
				fieldExpr = &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			}
			a.bindConditionStructPatternLocals(scope, arg.Pattern, fields[i].Type, fieldExpr)
		}
	case *ast.MatchListPattern:
		elemType, ok := SequenceMatchElementType(expected)
		if !ok {
			return
		}
		for i, elem := range p.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				continue
			}
			var indexExpr ast.Expr
			if valueExpr != nil {
				indexExpr = &ast.IndexExpr{
					Position: elem.Pos(),
					Object:   valueExpr,
					Index:    &ast.IntLit{Position: elem.Pos(), Value: strconv.Itoa(i), Suffix: "u"},
				}
			}
			a.bindConditionStructPatternLocals(scope, elem, elemType, indexExpr)
		}
	case *ast.MatchOrPattern:
		if _, ok := a.collectOrPatternBindingTypes(p, expected); !ok || len(p.Options) == 0 {
			return
		}
		a.bindConditionStructPatternLocals(scope, p.Options[0], expected, valueExpr)
	case *ast.MatchVariantPattern:
		switch variantBase := expected.(type) {
		case *EnumType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolvePartialMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				var payloadExpr ast.Expr
				if valueExpr != nil {
					resolvedExpr, ok := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
					if ok {
						payloadExpr = resolvedExpr
					}
				}
				a.bindConditionStructPatternLocals(scope, arg.Pattern, variant.Payload[i], payloadExpr)
			}
		}
	}
}

func (a *Analyzer) analyzeBlockWithAffineClonePrepared(stmts []ast.Stmt, scope *Scope, prepare func()) affineFlowSnapshot {
	savedAffine := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedStorageViewDeps := a.currentStorageViewDeps
	savedAliasAccesses := a.currentAliasAccesses
	savedAliasBindings := a.currentAliasBindings
	savedAliasCarriers := a.currentAliasCarriers
	savedPackedVariantViews := a.currentPackedVariantViews
	savedIndexBounds := a.currentIndexBounds
	savedViewStaticLen := a.currentViewStaticLen
	savedViewMutable := a.currentViewMutable
	a.currentAffineValues = a.cloneAffineValueStates()
	a.currentBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
	a.currentFunctionValues = a.cloneFunctionValueBindings()
	a.currentSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
	a.currentValueBindings = a.cloneValueBindings()
	a.currentStorageViewDeps = a.cloneStorageViewDeps()
	a.currentAliasAccesses = a.cloneAliasAccesses()
	a.currentAliasBindings = a.cloneAliasBindings()
	a.currentAliasCarriers = a.cloneAliasCarriers()
	a.currentIndexBounds = cloneIndexBoundFacts(a.currentIndexBounds)
	a.currentViewStaticLen = cloneViewStaticLen(a.currentViewStaticLen)
	a.currentViewMutable = cloneViewMutable(a.currentViewMutable)
	if prepare != nil {
		prepare()
	}
	a.analyzeBlockWithRegionClone(stmts, scope)
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings(), StorageViewDeps: a.cloneStorageViewDeps()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentStorageViewDeps = savedStorageViewDeps
	a.currentAliasAccesses = savedAliasAccesses
	a.currentAliasBindings = savedAliasBindings
	a.currentAliasCarriers = savedAliasCarriers
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentIndexBounds = savedIndexBounds
	a.currentViewStaticLen = savedViewStaticLen
	a.currentViewMutable = savedViewMutable
	return snapshot
}
