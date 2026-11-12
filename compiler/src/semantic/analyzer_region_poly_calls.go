package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/regionframesyntax"
	"fmt"
	"maps"
	"slices"
)

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

// defineRegionParamValueSymbols makes each explicit `[@owner]` parameter referenceable as an
// Arena VALUE inside the function body (not just as an `@owner` annotation). A region param is bound
// as a SymbolRegion exactly like a `region owner(...):` declaration, so `owner` resolves where an
// arena value is expected — e.g. a runtime container constructor that threads an explicit arena
// (`indexmap.new(owner)`, SoA stores). The backend already keys its region environment by name
// (state.regions), so it resolves the reference to the threaded hidden Arena& param. This is the
// no-Arena-field replacement for `owner: mutable Arena&` state fields: the region rides the type,
// and the value form is recovered from the same region param.
func (a *Analyzer) defineRegionParamValueSymbols(fn *ast.FuncDecl) {
	if fn == nil || len(fn.RegionParams) == 0 {
		return
	}
	arenaType, ok := a.namedTypes["Arena"]
	if !ok || arenaType == nil {
		return
	}
	arenaRef := &RefType{Elem: arenaType, State: RefStateNonNull, Storage: RefStorageAny, Mutable: true}
	for _, name := range fn.RegionParams {
		if _, exists := a.currentScope.Lookup(name); exists {
			continue // an explicit value parameter of the same name wins
		}
		sym := &Symbol{Name: name, Kind: SymbolRegion, Type: arenaRef, Node: fn, Mutable: true}
		a.defineLocal(sym, fn.Pos())
		if a.currentRegions != nil {
			a.setCurrentRegionState(sym, regionState{Backing: normalizeBacking(""), DeclScope: a.currentScope})
		}
	}
}

// regionPolymorphicCallerRegionArg produces the expression that threads the caller's ambient inferred
// region into a region-polymorphic call. When the caller is itself region-polymorphic, its hidden
// `__region_auto` parameter carries the region through. An explicit region-polymorphic caller such as
// `def f[@r](out: mutable darray[T]& @r)` carries the same ambient region in its declared region
// parameter; use that value before looking for an `in auto:` scope. Otherwise the active `in
// auto:` region (the same one `new[auto]` allocates into) supplies it. Returns false when no region
// is active.
func (a *Analyzer) regionPolymorphicCallerRegionArg() (ast.Expr, bool) {
	if a.currentFuncType != nil && a.currentFuncType.RegionPolymorphic {
		return &ast.Ident{Name: regionPolymorphicImplicitParamName}, true
	}
	// A function with an explicit region parameter has a caller-owned region even when it is
	// not classified as a value-returning region-polymorphic function. This is the common
	// out-param/grower shape: a helper returning a fresh AST/container value may be called from
	// the @r function, and the result must be allocated in the same @r region before it is
	// stored through the caller-owned out parameter. The parser defines each RegionParam as a
	// value-level Arena symbol, so an identifier is the correct hidden argument expression.
	if a.currentFuncType != nil && len(a.currentFuncType.RegionParams) == 1 {
		return &ast.Ident{Name: a.currentFuncType.RegionParams[0]}, true
	}
	if region := a.activeContainerRegionName(); region != "" {
		return &ast.Ident{Name: region}, true
	}
	return nil, false
}

// wrapRegionPolyCallerBodies wraps the body of each function that calls a region-polymorphic
// callee outside any region scope in a synthesized lazy auto region, so the call site has a
// region to thread (mirroring the parser's maybeWrapFunctionBodyInAutoRegion, which handles
// direct allocations but cannot know callee polymorphism). Only non-region-polymorphic
// functions without an Arena param are wrapped: a region-polymorphic caller threads its own
// `__region_auto`, and an Arena-threading function manages allocation itself.
func (a *Analyzer) wrapRegionPolyCallerBodies(funcs []*ast.FuncDecl) {
	defer func() { a.regionPolyFn = nil }()
	for _, fn := range funcs {
		a.regionPolyFn = fn
		fnType := a.funcTypeForRegionPoly(fn)
		if fnType == nil || funcHasArenaParam(fn) || len(fn.Body) == 0 {
			continue
		}
		if fnType.RegionPolymorphic {
			continue // threads its caller's region; no scope of its own needed
		}
		if !a.bodyCallsRegionPolyOutsideRegion(fn.Body) {
			continue
		}
		// Tighten loops first (the semantic analogue of the parser's wrapReclaimableLoopBodies):
		// a loop whose region-poly call results are iteration-local gets a per-iteration region,
		// so each iteration's trees are reclaimed instead of accumulating for the whole loop.
		a.tightenRegionPolyLoopBodies(fn.Body)
		if !a.bodyCallsRegionPolyOutsideRegion(fn.Body) {
			continue // every region-poly call now sits inside a loop region
		}
		wrapFuncBodyInLazyAutoRegion(fn)
	}
}

// wrapFuncBodyInLazyAutoRegion wraps a function body in a compiler-synthesized lazy auto
// region (the semantic analogue of the parser's maybeWrapFunctionBodyInAutoRegion).
func wrapFuncBodyInLazyAutoRegion(fn *ast.FuncDecl) {
	regionframesyntax.WrapFuncBodyInLazyAutoRegion(fn)
}

// tightenRegionPolyLoopBodies wraps reclaimable loop bodies containing region-poly calls in a
// per-iteration lazy region. Deliberately does NOT descend into explicit region/in-store scopes:
// code there already compiled with accumulation semantics and must not change behavior. Only
// previously-rejected code (region-poly calls with no ambient region) is affected.
func (a *Analyzer) tightenRegionPolyLoopBodies(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			s.Body = a.tightenRegionPolyLoopBody(s.Body)
		case *ast.WhileStmt:
			s.Body = a.tightenRegionPolyLoopBody(s.Body)
		case *ast.IterForStmt:
			s.Body = a.tightenRegionPolyLoopBody(s.Body)
		case *ast.IfStmt:
			a.tightenRegionPolyLoopBodies(s.Then)
			a.tightenRegionPolyLoopBodies(s.Else)
		case *ast.CanStmt:
			a.tightenRegionPolyLoopBodies(s.Body)
		case *ast.ScopeStmt:
			a.tightenRegionPolyLoopBodies(s.Body)
		case *ast.MatchStmt:
			for i := range s.Arms {
				a.tightenRegionPolyLoopBodies(s.Arms[i].Body)
			}
		}
	}
}

func (a *Analyzer) tightenRegionPolyLoopBody(body []ast.Stmt) []ast.Stmt {
	a.tightenRegionPolyLoopBodies(body) // tighten nested loops first
	if !a.loopBodyRegionPolyReclaimable(body) {
		return body
	}
	pos := body[0].Pos()
	return []ast.Stmt{&ast.RegionStmt{
		Position: pos,
		Name:     fmt.Sprintf("__auto_%d", pos.Offset),
		Lazy:     true,
		Body:     body,
	}}
}

// loopBodyRegionPolyReclaimable reports whether a loop body's region-poly allocations are all
// iteration-local, so a per-iteration region is safe and useful. Conservative: a region-allocated
// RESULT bound to a name must be iteration-local; one assigned to an existing (possibly outer)
// target disqualifies the loop; growing an outer container disqualifies it. A region-poly call
// consumed within an expression (`acc <- acc + check(make(d))` yields a scalar) is always fine.
func (a *Analyzer) loopBodyRegionPolyReclaimable(body []ast.Stmt) bool {
	if len(body) == 0 || !a.bodyCallsRegionPolyOutsideRegion(body) {
		return false
	}
	if ast.LoopBodyGrowsOuterContainer(body) {
		return false
	}
	for _, stmt := range body {
		switch s := stmt.(type) {
		case *ast.VarDeclStmt:
			if a.exprResultIsRegionAllocated(s.Value) && !ast.AllocationIsIterationLocal(s.Name, body) {
				return false
			}
		case *ast.AssignStmt:
			if a.exprResultIsRegionAllocated(s.Value) {
				return false // the target may outlive the iteration
			}
		}
	}
	return true
}

// bodyCallsRegionPolyOutsideRegion reports whether the statements contain a direct call to a
// region-polymorphic function that is not already inside a region scope (`region NAME:` /
// `in owner:` subtrees supply their own ambient region and are skipped). Lambda bodies are
// skipped too: a lambda runs in its eventual caller's context, not this function's.
func (a *Analyzer) bodyCallsRegionPolyOutsideRegion(stmts []ast.Stmt) bool {
	found := false
	walkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		switch n := node.(type) {
		case *ast.RegionStmt:
			if len(n.Body) != 0 {
				return false // scoped region supplies the ambient region for its body
			}
		case *ast.InStoreStmt, *ast.LambdaExpr:
			return false
		case *ast.CallExpr:
			if ft := a.regionPolyCalleeFuncType(n); ft != nil && ft.RegionPolymorphic {
				found = true
			}
		case *ast.StructLitExpr:
			// Before analysis, function calls written in constructor syntax
			// can be represented as struct literals.
			if n.Name != "" {
				if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok && sym != nil {
					if ft, isFn := sym.Type.(*FuncType); isFn && ft != nil && ft.RegionPolymorphic {
						found = true
					}
				}
			}
		}
		return !found
	})
	return found
}

// regionPolyCalleeFuncType resolves a direct call's callee to its FuncType when the callee is a plain
// identifier naming a visible global. Indirect or method calls return nil (the transitive trigger
// simply does not fire — the direct new[auto] trigger still covers them).
func (a *Analyzer) regionPolyCalleeFuncType(call *ast.CallExpr) *FuncType {
	if call == nil {
		return nil
	}
	name := ""
	callee := unwrapParenForRegionPoly(call.Func)
	// Unwrap generic value-form specialization: `fn[A, B](...)` parses as a SpecializeExpr
	// callee, and single-arg `fn[T](...)` as an IndexExpr over the function identifier (the
	// analyzer reinterprets it later via AsSpecialize). For ordinary indexing the Object names
	// a variable, whose symbol type is not a FuncType, so the lookup below returns nil anyway.
	switch wrapped := callee.(type) {
	case *ast.SpecializeExpr:
		callee = unwrapParenForRegionPoly(wrapped.Operand)
	case *ast.IndexExpr:
		callee = unwrapParenForRegionPoly(wrapped.Object)
	}
	switch callee := callee.(type) {
	case *ast.Ident:
		name = callee.Name
	case *ast.FieldExpr:
		// `B.method(...)` where B is a protocol-bounded generic param of the function
		// under classification: the concrete impl is specialization-dependent, so the
		// call is region-polymorphic if ANY impl of the protocol method is.
		if ft := a.regionPolyProtocolMethodFuncType(callee); ft != nil {
			return ft
		}
		// UFCS method call `recv.helper(...)`: methods are plain globals, so resolve by
		// the field name. A top-level helper resolves under its simple name here; a
		// MODULE-scoped method (`Parser.expr`) does NOT — lookupVisibleGlobal only sees
		// its namespace-qualified name — so without a fallback every UFCS call of a
		// module-method region-poly builder is invisible to classification, and its
		// callers fail with "must occur where a region can be inferred". Fall back to the
		// simple-name candidate index (populated for every candidate, module methods
		// included; same *FuncType pointers, live across the fixpoint). Prefer a
		// region-polymorphic candidate; UFCS can't disambiguate same-named methods
		// pre-analysis, and over-classifying the region decision is sound (an unused
		// threaded region). A lone candidate is returned as-is so store-needs threading
		// (keyed on the same pointer) also resolves module methods.
		name = callee.Field
		if sym, _, ok := a.lookupVisibleGlobal(name); ok && sym != nil {
			if ft, isFn := sym.Type.(*FuncType); isFn && ft != nil {
				return ft
			}
		}
		if a.regionPolyCandidateFnTypes != nil {
			cands := a.regionPolyCandidateFnTypes[simpleTypeNameSegment(name)]
			for _, ft := range cands {
				if ft != nil && ft.RegionPolymorphic {
					return ft
				}
			}
			if len(cands) == 1 {
				return cands[0]
			}
		}
		return nil
	default:
		return nil
	}
	if name == "" {
		return nil
	}
	if sym, _, ok := a.lookupVisibleGlobal(name); ok && sym != nil {
		if fnType, isFn := sym.Type.(*FuncType); isFn && fnType != nil {
			return fnType
		}
	}
	// A BARE sibling call inside `module M:` -- `label_text(x)` meaning `M::label_text`.
	// lookupVisibleGlobal only finds it through a.currentNamespace, which is not set
	// during this classification PRE-PASS, so the callee was invisible and its caller
	// never got a region wrapped around it. The call then failed at analysis with
	// "must occur where a region can be inferred", for a program that compiles
	// unchanged when the same two functions sit at top level.
	//
	// Same fallback, and the same soundness argument, as the UFCS case above: the
	// simple-name index covers module methods, and over-classifying the region decision
	// costs at most an unused threaded region.
	if a.regionPolyCandidateFnTypes != nil {
		cands := a.regionPolyCandidateFnTypes[simpleTypeNameSegment(name)]
		for _, ft := range cands {
			if ft != nil && ft.RegionPolymorphic {
				return ft
			}
		}
		if len(cands) == 1 {
			return cands[0]
		}
	}
	return nil
}

// regionPolyProtocolMethodFuncType resolves `B.method(...)` — B a generic param of the
// function under classification (a.regionPolyFn) bounded by a protocol — to a
// region-polymorphic impl method FuncType, if any impl of that protocol provides one.
// The dispatch target is specialization-dependent, so classification must be the union
// over impls: if any impl threads a caller region, the generic caller must too (a
// specialization with a value impl merely carries an unused Arena&).
func (a *Analyzer) regionPolyProtocolMethodFuncType(callee *ast.FieldExpr) *FuncType {
	for _, ft := range a.regionPolyProtocolMethodImplFuncTypes(callee) {
		if ft.RegionPolymorphic {
			return ft
		}
	}
	return nil
}

// regionPolyProtocolMethodImplFuncTypes returns the FuncTypes of every impl of the
// protocol method named by `B.method(...)`, where B is a protocol-bounded generic param
// of the function under classification (a.regionPolyFn). Empty outside the pre-pass or
// when the callee is not such a dispatch.
func (a *Analyzer) regionPolyProtocolMethodImplFuncTypes(callee *ast.FieldExpr) []*FuncType {
	fn := a.regionPolyFn
	if fn == nil || callee == nil || len(fn.GenericParams) == 0 {
		return nil
	}
	obj, ok := unwrapParenForRegionPoly(callee.Object).(*ast.Ident)
	if !ok || obj == nil {
		return nil
	}
	bound := ""
	for _, gp := range fn.GenericParams {
		if gp.Name == obj.Name {
			bound = gp.InterfaceBound
			break
		}
	}
	if bound == "" {
		return nil
	}
	_, ifaceName, ok := a.lookupVisibleStaticInterface(bound)
	if !ok {
		return nil
	}
	var out []*FuncType
	for _, implKey := range slices.Sorted(maps.Keys(a.staticImpls)) { // map: sort so the first region-polymorphic impl is the same every run
		impl := a.staticImpls[implKey]
		if impl == nil || impl.InterfaceName != ifaceName {
			continue
		}
		sym := impl.Methods[callee.Field]
		if sym == nil {
			continue
		}
		if ft, ok := sym.Type.(*FuncType); ok && ft != nil {
			out = append(out, ft)
		}
	}
	return out
}

// returnTypeIsStaticRef reports whether a syntactic return type is a reference with
// `static` storage (peeling mutable/optional wrappers): `static u8&`, `mutable static T&?`.
func returnTypeIsStaticRef(typ ast.TypeExpr) bool {
	return regionframesyntax.ReturnTypeIsStaticRef(typ)
}
