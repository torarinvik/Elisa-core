package semantic

import "elisacore/src/ast"

// RegionPolymorphicImplicitParamName is the synthetic name of the hidden region parameter threaded
// into a region-polymorphic function (docs/75). It carries the caller's region as an Arena& value;
// `new[auto]` in the body allocates into it, so the result lives in the caller's region. Exported so
// the backend recognizes the param when binding it at function entry.
const RegionPolymorphicImplicitParamName = "__region_auto"

// regionPolymorphicImplicitParamName is the package-internal alias kept for brevity at call sites.
const regionPolymorphicImplicitParamName = RegionPolymorphicImplicitParamName

// classifyRegionPolymorphicFunctions is the docs/75 pre-pass. It runs after every top-level FuncType
// exists but BEFORE any body is analyzed, so that when bodies (and their call sites — recursive ones
// included) are analyzed, every region-polymorphic callee already advertises its hidden region
// parameter. A function is region-polymorphic when a value-returning path hands back a value built
// with `new[auto]` (directly, or transitively via a call to an already-region-polymorphic function).
// The "transitive" case needs a fixpoint: a wrapper that `return helper()` only becomes
// region-polymorphic once `helper` is known to be.
func (a *Analyzer) classifyRegionPolymorphicFunctions(decls []scopedDecl) {
	funcs := collectRegionPolyCandidateFuncs(decls)
	if len(funcs) == 0 {
		return
	}
	changed := true
	for changed {
		changed = false
		for _, fn := range funcs {
			fnType := a.funcTypeForRegionPoly(fn)
			if fnType == nil || fnType.RegionPolymorphic {
				continue
			}
			if a.functionReturnsRegionAllocatedValue(fn) {
				fnType.RegionPolymorphic = true
				changed = true
			}
		}
	}
	// docs/74: only enums actually built with `new[auto]` somewhere in the program are region-backed
	// and eligible for implicit store threading; explicit-store enums must be left untouched.
	regionBackedPacked := a.collectRegionBackedPackedEnums(funcs)
	// docs/76: compute each function's TRANSITIVE region-backed store needs (signature ∪ what its
	// callees need) so mutually-recursive enum groups thread every required store across the call
	// graph, not just the one in each function's own signature.
	storeNeeds := a.computeTransitiveStoreNeeds(funcs, regionBackedPacked)
	// An export target must have NO implicit parameters (exports.go), so it can never receive a
	// threaded store — it owns a region and creates stores on demand. The generated test runner turns
	// each @test body into an export target (e.g. ctx_test_case_impl_0), so detect them structurally.
	exportTargets := map[string]bool{}
	for _, scoped := range decls {
		if ed, ok := scoped.Decl.(*ast.ExportFuncDecl); ok && ed != nil && ed.TargetName != "" {
			exportTargets[ed.TargetName] = true
		}
	}
	for _, fn := range funcs {
		fnType := a.funcTypeForRegionPoly(fn)
		if fnType == nil {
			continue
		}
		// Entry points (@test/@bench/main) and export targets own a region and create stores on demand
		// — they must NOT receive threaded store params (the runtime/export ABI calls them with none).
		// Every other function receives its full transitive store set threaded in, so a bare handle can
		// be built/matched without an explicit Store, even across a mutually-recursive enum group.
		if fn.Name != "" && exportTargets[fn.Name] {
			continue
		}
		if fnType.RegionPolymorphic {
			a.injectRegionPolymorphicParam(fnType)
		}
		// A function that owns a region (entry/export, or any `in auto:` block) creates region-backed
		// stores on demand — possibly one per region in a loop — so it gets only its SIGNATURE stores
		// threaded (the data flowing across its boundary), never the transitive callee set. Only
		// region-LESS functions (pure consumers/builders like sumT/sumF/leaf) receive the full
		// transitive set, because they have no region in which to create a callee's store themselves.
		if funcIsRegionStoreEntryPoint(fn) || funcOwnsRegion(fn) {
			a.injectInferredPackedStoreParams(fnType, regionBackedPacked)
		} else {
			a.injectStoreNeeds(fnType, storeNeeds[fnType])
		}
	}
}

// collectRegionPolyCandidateFuncs flattens the top-level and impl-member function declarations into
// a single list for the classification fixpoint.
func collectRegionPolyCandidateFuncs(decls []scopedDecl) []*ast.FuncDecl {
	var out []*ast.FuncDecl
	for _, scoped := range decls {
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			out = append(out, n)
		case *ast.ImplDecl:
			for _, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					out = append(out, fn)
				}
			}
		}
	}
	return out
}

func (a *Analyzer) funcTypeForRegionPoly(fn *ast.FuncDecl) *FuncType {
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		return nil
	}
	fnType, _ := sym.Type.(*FuncType)
	return fnType
}

// injectRegionPolymorphicParam appends the hidden region parameter (an Arena&, named
// `__region_auto`) to a region-polymorphic function's type, mirroring the tree-store implicit-param
// injection. Idempotent: a second call is a no-op.
func (a *Analyzer) injectRegionPolymorphicParam(fnType *FuncType) {
	if fnType == nil || funcTypeHasImplicitParam(fnType, regionPolymorphicImplicitParamName) {
		return
	}
	arenaType := a.namedTypes["Arena"]
	if arenaType == nil {
		return
	}
	fnType.Params = append(fnType.Params, &RefType{Elem: arenaType, State: RefStateNonNull, Storage: RefStorageAny, Mutable: true})
	fnType.ImplicitParamNames = append(fnType.ImplicitParamNames, regionPolymorphicImplicitParamName)
}

// functionReturnsRegionAllocatedValue reports whether any value-returning path of fn hands back a
// value whose result is a `new[auto]` allocation, or a call to an already-region-polymorphic
// function. Walks only statement bodies (not lambda expression bodies), so a `return` inside a
// nested closure is correctly excluded.
func (a *Analyzer) functionReturnsRegionAllocatedValue(fn *ast.FuncDecl) bool {
	found := false
	var walk func(stmts []ast.Stmt)
	check := func(value ast.Expr) {
		if value != nil && a.exprResultIsRegionAllocated(value) {
			found = true
		}
	}
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			if found {
				return
			}
			switch s := stmt.(type) {
			case *ast.ReturnStmt:
				check(s.Value)
			case *ast.IfStmt:
				walk(s.Then)
				for _, elif := range s.Elifs {
					walk(elif.Body)
				}
				walk(s.Else)
			case *ast.WhileStmt:
				walk(s.Body)
			case *ast.ForStmt:
				walk(s.Body)
			case *ast.IterForStmt:
				walk(s.Body)
			case *ast.ParallelForStmt:
				walk(s.Body)
			case *ast.ScopeStmt:
				walk(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					walk(arm.Body)
				}
			case *ast.CanStmt:
				walk(s.Body)
			case *ast.WithStmt:
				walk(s.Body)
			case *ast.RegionStmt:
				walk(s.Body)
			case *ast.InStoreStmt:
				walk(s.Body)
			}
		}
	}
	walk(fn.Body)
	return found
}

// exprResultIsRegionAllocated reports whether the RESULT of an expression is a fresh region
// allocation (a `new[auto]` modulo parentheses) or the result of an already-region-polymorphic call.
// It deliberately does NOT descend into arguments: `f(new[auto] X())` yields f's result, not the
// new[auto], so it is not region-allocated unless f itself is region-polymorphic.
func (a *Analyzer) exprResultIsRegionAllocated(value ast.Expr) bool {
	value = unwrapParenForRegionPoly(value)
	switch e := value.(type) {
	case *ast.AllocExpr:
		return e != nil && e.AutoRegion
	case *ast.CallExpr:
		if ft := a.regionPolyCalleeFuncType(e); ft != nil && ft.RegionPolymorphic {
			return true
		}
		// A bare constructor of a region-backed (recursive-plain) enum is an implicit new[auto]
		// allocation (docs/76 Slice 0b), so a function returning one is region-polymorphic.
		if et, ok := a.regionBackedEnumConstructor(e); ok && et != nil {
			return true
		}
	}
	return false
}

// regionBackedEnumConstructor reports the enum if a call is a bare constructor of a region-backed
// (recursive-plain) enum — `Expr.Add(...)` where Expr is a promoted plain enum. Explicit `packed
// enum`s (e.g. an explicit-store JSON DOM) are NOT region-backed-by-default and are excluded, so
// their bare constructors keep the explicit-store path.
func (a *Analyzer) regionBackedEnumConstructor(call *ast.CallExpr) (*EnumType, bool) {
	if call == nil {
		return nil, false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(fieldExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed || !enumType.RecursivePlain {
		return nil, false
	}
	return enumType, true
}

func unwrapParenForRegionPoly(value ast.Expr) ast.Expr {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok || paren == nil {
			return value
		}
		value = paren.Inner
	}
}

// defineRegionPolymorphicParamSymbol binds the hidden `__region_auto` Arena& parameter as a local
// symbol in a region-polymorphic function's body scope, so the threaded recursive-call argument
// (`Ident{__region_auto}`) resolves and implicitBindingsForCurrentFunction surfaces it.
func (a *Analyzer) defineRegionPolymorphicParamSymbol(fn *ast.FuncDecl, fnType *FuncType) {
	if fnType == nil || a.namedTypes["Arena"] == nil {
		return
	}
	idx := -1
	for i, name := range fnType.ImplicitParamNames {
		if name == regionPolymorphicImplicitParamName {
			idx = funcTypeExplicitParamCount(fnType) + i
			break
		}
	}
	if idx < 0 {
		return
	}
	arenaRef := &RefType{Elem: a.namedTypes["Arena"], State: RefStateNonNull, Storage: RefStorageAny, Mutable: true}
	sym := &Symbol{Name: regionPolymorphicImplicitParamName, Kind: SymbolParam, Type: arenaRef, Node: fn, ParamIndex: idx}
	a.defineLocal(sym, fn.Pos())
}

// regionPolymorphicCallerRegionArg produces the expression that threads the caller's ambient inferred
// region into a region-polymorphic call. When the caller is itself region-polymorphic, its hidden
// `__region_auto` parameter carries the region through. Otherwise the active `in auto:` region (the
// same one `new[auto]` allocates into) supplies it. Returns false when no region is active.
func (a *Analyzer) regionPolymorphicCallerRegionArg() (ast.Expr, bool) {
	if a.currentFuncType != nil && a.currentFuncType.RegionPolymorphic {
		return &ast.Ident{Name: regionPolymorphicImplicitParamName}, true
	}
	if region := a.activeContainerRegionName(); region != "" {
		return &ast.Ident{Name: region}, true
	}
	return nil, false
}

// regionPolyCalleeFuncType resolves a direct call's callee to its FuncType when the callee is a plain
// identifier naming a visible global. Indirect or method calls return nil (the transitive trigger
// simply does not fire — the direct new[auto] trigger still covers them).
func (a *Analyzer) regionPolyCalleeFuncType(call *ast.CallExpr) *FuncType {
	if call == nil {
		return nil
	}
	ident, ok := unwrapParenForRegionPoly(call.Func).(*ast.Ident)
	if !ok || ident == nil {
		return nil
	}
	sym, _, ok := a.lookupVisibleGlobal(ident.Name)
	if !ok || sym == nil {
		return nil
	}
	fnType, _ := sym.Type.(*FuncType)
	return fnType
}
