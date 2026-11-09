package semantic

import (
	"elisacore/src/ast"
)

// isUserAutoRegion reports whether a synthesized `__auto_*` region is one the user
// wrote as `in auto:`. Unlike an inferred function-body region it is freed at its
// block's exit (no adoption), so a value stored out of it dangles exactly like one
// stored out of `region NAME:`.
func (a *Analyzer) isUserAutoRegion(name string) bool {
	if a == nil || a.currentScope == nil {
		return false
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || sym.Kind != SymbolRegion {
		return false
	}
	region, isRegion := sym.Node.(*ast.RegionStmt)
	return isRegion && region != nil && region.UserAuto
}

// checkRegionlessTargetStoreEscape rejects storing region-allocated data into a
// region-less binding that outlives the region's block. The target outlives the
// region exactly when its defining scope strictly encloses the region's
// declaration scope (it was declared before the region block opened, so it is
// still live after the region is freed). Compiler-inferred auto regions are
// excluded (the region-return/adoption machinery governs them); a user-written
// `in auto:` block is not, since the backend frees it at block exit.
func (a *Analyzer) checkRegionlessTargetStoreEscape(targetExpr ast.Expr, valueRegion string) {
	if a == nil || targetExpr == nil {
		return
	}
	root := rootIdentExpr(targetExpr)
	if root == nil {
		return
	}
	a.checkRegionlessTargetStoreEscapeInScope(targetExpr, valueRegion, a.definingScope(root.Name))
}

func (a *Analyzer) checkRegionlessTargetStoreEscapeInScope(targetExpr ast.Expr, valueRegion string, targetScope *Scope) {
	if a == nil || targetExpr == nil {
		return
	}
	if a.isCompilerGeneratedAutoRegion(valueRegion) {
		return
	}
	regionSym, state := a.lookupRegionState(valueRegion)
	if regionSym == nil || state.Destroyed || state.DeclScope == nil {
		return
	}
	root := rootIdentExpr(targetExpr)
	if root == nil {
		return
	}
	if targetScope == nil || targetScope == state.DeclScope {
		return
	}
	if !scopeStrictlyEncloses(targetScope, state.DeclScope) {
		return
	}
	a.errorf(targetExpr.Pos(), "value in region %q is stored into %q, which outlives the region; region %q is freed first, leaving a dangling reference. Copy the value out of region %q (or declare %q inside the region block) instead", a.regionDisplayName(valueRegion), root.Name, a.regionDisplayName(valueRegion), a.regionDisplayName(valueRegion), root.Name)
}

// isFreshContainerProducer reports whether an expression CONSTRUCTS a fresh value whose interior may
// carry a shorter-lived region than where the value is stored: a list literal, comprehension,
// ternary (a runtime choice between branches), or struct literal. These are the forms whose OWN region
// (the target's) hides their elements'/fields' region, so they need the interior-region escape check.
func isFreshContainerProducer(expr ast.Expr) bool {
	switch stripParenExpr(expr).(type) {
	case *ast.ListLitExpr, *ast.ListComprehensionExpr, *ast.TernaryExpr, *ast.StructLitExpr:
		return true
	}
	return false
}

// valueInteriorRegion returns the innermost (shortest-lived) tracked region reachable through a
// value-producing expression's interior: the (non-spread) elements of a list literal or
// comprehension, BOTH branches of a ternary (either may be chosen at runtime), and the fields of a
// struct literal, recursing through these producer forms. A leaf is the expression's own
// container/entry region or recorded interior-field taint. "" when nothing region-carrying is
// reachable (scalars, region-less data).
func (a *Analyzer) valueInteriorRegion(expr ast.Expr) string {
	if a == nil || expr == nil {
		return ""
	}
	stripped := stripParenExpr(expr)
	switch n := stripped.(type) {
	case *ast.ListLitExpr:
		r := ""
		for i, elem := range n.Elems {
			if i < len(n.Spreads) && n.Spreads[i] {
				continue // spreads are bulk-checked at the store site (with the scalar-elem exemption).
			}
			r = a.innerRegion(r, a.innerRegion(a.nestedFreshStorageRegion(elem), a.valueInteriorRegion(elem)))
		}
		return r
	case *ast.ListComprehensionExpr:
		r := a.valueInteriorRegion(n.Value)
		if n.Key != nil {
			r = a.innerRegion(r, a.valueInteriorRegion(n.Key))
		}
		return r
	case *ast.TernaryExpr:
		return a.innerRegion(a.valueInteriorRegion(n.Value), a.valueInteriorRegion(n.Alt))
	case *ast.StructLitExpr:
		r := ""
		for _, arg := range n.Args {
			r = a.innerRegion(r, a.innerRegion(a.nestedFreshStorageRegion(arg), a.valueInteriorRegion(arg)))
		}
		for _, spread := range n.Spreads {
			r = a.innerRegion(r, a.valueInteriorRegion(spread))
		}
		return r
	default:
		if r := a.valueStoreRegion(stripped, a.exprType(stripped)); r != "" {
			return r
		}
		// An enum-ctor element (`[Item.Row(make_vals(10))]` fed to extend/assign) carries its
		// payload's fresh region even though the enum type is unstamped — same blindness the
		// element-store check had (enumCtorPayloadRegion).
		return a.enumCtorPayloadRegion(stripped)
	}
}

// checkFreshContainerStoreEscape rejects storing a freshly-built container/struct whose INTERIOR
// carries a shorter-lived region than where it is stored. A list literal `[v]`, comprehension
// `[mk() for ...]`, ternary `[v if c else w]`, or struct literal `Holder(xs: v)` copies each element's
// /field's header; an interior typed `@inner` then dangles once the inner region is freed. The
// store-site check (checkNestedRegionStoreEscape) only sees the fresh value's OWN region - which is
// the target's - because element/field types are never region-stamped. This is the
// construction-expression analogue of the push/dict.put element-store check (which only fires on
// explicit container-mutation calls). Spread elements (`[...src]`) copy a whole source container, so
// they route through the bulk-store check (which exempts scalar elements).
func (a *Analyzer) checkFreshContainerStoreEscape(targetExpr ast.Expr, targetType Type, valueExpr ast.Expr) {
	if a == nil || valueExpr == nil {
		return
	}
	if lit, ok := stripParenExpr(valueExpr).(*ast.ListLitExpr); ok {
		elemType := dArrayLikeElemType(targetType)
		for i, elem := range lit.Elems {
			if elem != nil && i < len(lit.Spreads) && lit.Spreads[i] {
				a.checkNestedRegionBulkStoreEscape(elem, targetExpr, targetType, elemType, a.exprType(elem))
			}
		}
	}
	if !isFreshContainerProducer(valueExpr) {
		return
	}
	a.checkInteriorRegionAgainstTarget(targetExpr, containerRegion(targetType), valueExpr, "fresh container")
}

// checkInteriorRegionAgainstTarget rejects a value whose interior region is freed before the target
// storage: a longer-lived target region (or a region-less target that outlives the value's region)
// leaves the stored interior dangling.
func (a *Analyzer) checkInteriorRegionAgainstTarget(targetExpr ast.Expr, targetRegion string, valueExpr ast.Expr, via string) {
	valueRegion := a.valueInteriorRegion(valueExpr)
	if state, ok := a.regionRefStateForExpr(valueExpr); ok {
		if parameterRegion, known := a.currentParamRegionFromRefState(state); known {
			// The side-table is more precise than the nominal enum/container type:
			// a region-polymorphic return may carry caller-owned interior storage
			// even when valueInteriorRegion sees only the callee's auto region.
			valueRegion = parameterRegion
		}
	}
	if valueRegion == "" {
		return // scalar / regionless interior can't dangle.
	}
	// Sanctioned void-grower adoption: when this function's ambient allocation region is bound to
	// the target container's region (single-container grower, AmbientGrownContainerRegion), the
	// value's synthesized `__auto_*` interior actually lives in the caller's arena — the backend's
	// regionPolyAutoAdopts routes it there — so nothing dangles. Mirrors the enum-ctor element
	// check; keeps `out.push(Holder{items: make_vals(10)})` consistent with the enum-ctor form.
	if a.isCompilerGeneratedAutoRegion(valueRegion) && a.autoRegionAdoptsCallerArena(targetRegion) {
		return
	}
	if targetRegion == "" {
		a.checkRegionlessTargetStoreEscape(targetExpr, valueRegion)
		return
	}
	if a.regionStoreEscapes(targetRegion, valueRegion) {
		a.errorf(valueExpr.Pos(), "value in region %q is stored into longer-lived region %q via a %s; region %q is freed first, leaving a dangling element. Copy it into region %q (or a region that outlives %q) before storing", a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), via, a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(targetRegion))
	}
}

// checkFreshProducerElementEscape rejects passing a FRESH producer value (list literal, comprehension,
// ternary, struct literal) whose interior carries a shorter-lived region than the container it is
// stored into, at a container-mutation site (push / extend / dict.put / dict-entry insert). The
// element-store checks (checkNestedRegionElementStoreEscape / ...Bulk...) see only the producer's OWN
// region — which is the container's — so they miss its interior; this descends into it. A no-op for a
// non-producer argument, which the element/bulk store checks already cover.
func (a *Analyzer) checkFreshProducerElementEscape(receiverExpr ast.Expr, containerType Type, argExpr ast.Expr) {
	if argExpr == nil || !isFreshContainerProducer(argExpr) {
		return
	}
	a.checkInteriorRegionAgainstTarget(receiverExpr, containerRegion(containerType), argExpr, "fresh container")
}

// dArrayLikeElemType returns the element type of a darray/array/deque-like container, or nil.
func dArrayLikeElemType(t Type) Type {
	switch ct := stripRefForBounds(t).(type) {
	case *DArrayType:
		if ct != nil {
			return ct.Elem
		}
	case *ArrayType:
		if ct != nil {
			return ct.Elem
		}
	}
	return nil
}

// rootIdentExpr peels field accesses and indexing down to the base identifier
// of an lvalue expression, or nil when the root is not a plain identifier.
func rootIdentExpr(expr ast.Expr) *ast.Ident {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e
		case *ast.FieldExpr:
			expr = e.Object
		case *ast.IndexExpr:
			expr = e.Object
		default:
			return nil
		}
	}
}

// definingScope returns the scope that defines name as seen from the current
// scope (respecting shadowing), or nil if the name is not in scope.
func (a *Analyzer) definingScope(name string) *Scope {
	for s := a.currentScope; s != nil; s = s.Parent {
		if _, ok := s.Symbols[name]; ok {
			return s
		}
	}
	return nil
}

// scopeStrictlyEncloses reports whether outer is a strict ancestor of inner.
func scopeStrictlyEncloses(outer, inner *Scope) bool {
	if outer == nil || inner == nil {
		return false
	}
	for s := inner.Parent; s != nil; s = s.Parent {
		if s == outer {
			return true
		}
	}
	return false
}

// regionAvailableForContainer reports whether a container's allocation region is
// LIVE at the current point, so a grow/push may source that region's arena. The
// design's rule: "push is legal iff the container's region r is live." A region
// is live when it is (a) an in-scope region parameter (caller supplies the
// arena), (b) the active `region r(...):` scope, or (c) any tracked, non-
// destroyed local region in scope. Codegen mirrors this via regionArenaOwner,
// which sources the arena from exactly that region's binding — so accepting these
// here is sound (the previous check rejected the design's canonical
// `region a: v.push(x)`, which codegen already handled).
func (a *Analyzer) regionAvailableForContainer(t Type) bool {
	if a == nil {
		return false
	}
	region := containerOrEntryRegion(t)
	if region == "" {
		return false
	}
	if a.lookupRegionParam(region) {
		return true
	}
	if sym, state := a.lookupRegionState(region); sym != nil && !state.Destroyed {
		return true
	}
	// An Arena value or Arena& ref in scope also satisfies the region requirement.
	// This covers `darray[T] @alloc` when `alloc: mutable Arena&` is in scope.
	if a.currentScope != nil && a.namedTypes["Arena"] != nil {
		if sym, ok := a.currentScope.Lookup(region); ok && sym != nil {
			arenaType := a.namedTypes["Arena"]
			st := sym.Type
			if SameType(st, arenaType) {
				return true
			}
			if refT, ok2 := st.(*RefType); ok2 && refT != nil && SameType(refT.Elem, arenaType) {
				return true
			}
		}
	}
	return false
}

// containerRegionParamInScope reports whether t is a container whose region is
// an in-scope region parameter (e.g. `def f[@r](out: darray[T] @r&)` or
// `def f[@r](d: dict[K,V] @r&)`). Such a container may be grown/inserted
// without an ambient `in <arena>:` scope: the arena for r is supplied by the
// caller (region-aware codegen sources it from the region environment / hidden
// arena param). Handles darray, dict, dict-entry receivers, peeling refs.
func (a *Analyzer) containerRegionParamInScope(t Type) bool {
	if a == nil {
		return false
	}
	if region := containerOrEntryRegion(t); region != "" {
		return a.lookupRegionParam(region)
	}
	return false
}

// containerOrEntryRegion peels ref wrappers and returns the allocation region of
// a darray, dict, or dict-entry (the entry's underlying dict), or "".
func containerOrEntryRegion(t Type) string {
	for {
		switch tt := t.(type) {
		case *DArrayType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *DictType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *SetType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *DictEntryType:
			if tt == nil || tt.Dict == nil {
				return ""
			}
			return tt.Dict.Region
		case *CStrType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *SViewType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *ViewType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *RefType:
			if tt == nil {
				return ""
			}
			t = tt.Elem
		case *TupleType, *OptionalType, *ErrorUnionType, *ArrayType, *AggregateStateType, *StructType:
			return commonExplicitTypeRegion(tt)
		default:
			return ""
		}
	}
}

// activeContainerRegionName is the name of the region of the innermost active
// `in <arena/region>:` scope, or "" if none.
func (a *Analyzer) activeContainerRegionName() string {
	if a == nil || a.currentAllocExpr == nil {
		return ""
	}
	if ident, ok := stripParenExpr(a.currentAllocExpr).(*ast.Ident); ok && ident != nil {
		return ident.Name
	}
	return ""
}

// liveLocalRegionDependencyName returns the lexically smallest live region a
// provenance state depends on (deterministic across map order), or "".
func liveLocalRegionDependencyName(state regionRefState) string {
	name := ""
	for region, dep := range state.Deps {
		if region == nil || !dep.Valid {
			continue
		}
		if name == "" || region.Name < name {
			name = region.Name
		}
	}
	return name
}

// autoRegionAdoptsCallerArena mirrors the backend's ambient-grown-target path.
// Region-polymorphic functions instead use their distinct hidden caller arena,
// even if an ambient grown target is also recorded on the declaration.
func (a *Analyzer) autoRegionAdoptsCallerArena(targetRegion string) bool {
	if a == nil {
		return false
	}
	if a.currentFuncType != nil && a.currentFuncType.RegionPolymorphic {
		return false
	}
	// docs/128: the caller's arena does not outlive a program-lived region, so adopting into
	// it never sanctions a store there.
	if a.isProgramRegion(targetRegion) {
		return false
	}
	if a.currentFuncDecl != nil && targetRegion != "" && a.currentFuncDecl.AmbientGrownContainerRegion == targetRegion {
		a.noteAmbientFillFresh()
		return true
	}
	return false
}

// checkCallArgumentRegionStoreEscape: a call whose `mutable C&` parameter receives a container
// that outlives a local region may store another argument into it. `put(out, E{name:
// xs.as_sview()})` with put pushing its second argument into out leaves the caller's out holding
// a view of xs's bytes, which live in a function-local region freed on return. The direct form
// `out.push(...)` is checked by checkNestedRegionElementStoreEscape; the callee's own checks
// accept the store (both are its parameters), so the caller checks every argument the call may
// store, with the same adoption sanction.
func (a *Analyzer) checkCallArgumentRegionStoreEscape(call *ast.CallExpr) {
	if a == nil || call == nil || a.suppressDiagnostics || a.staticContextDepth != 0 || a.currentFuncDecl == nil {
		return
	}
	fnType := a.returnBorrowCalleeSignature(call)
	if fnType == nil {
		return
	}
	args := returnBorrowCallArgs(call)
	// What each argument may point at depends only on its type; compute it once per argument,
	// not once per (writable param, argument) pair.
	argPointees := make([]*regionPointees, len(args))
	storePointeesOf := func(other int) regionPointees {
		if argPointees[other] == nil {
			pointees := a.argumentStorePointeesOf(a.exprType(args[other]))
			argPointees[other] = &pointees
		}
		return *argPointees[other]
	}
	for index, paramType := range fnType.Params {
		if index >= len(args) {
			continue
		}
		// A literal null passed to an optional writable reference has no destination.
		// Nullable variables can still hold a real destination and must be analyzed.
		if ref, isNullableRef := paramType.(*RefType); isNullableRef && ref != nil && ref.State == RefStateNullable {
			if _, isNull := stripOptimizationParens(args[index]).(*ast.NullLit); isNull {
				continue
			}
		}
		// A mutable reference to a struct holding region storage (owned containers included,
		// which returnBorrowWritableParam does not count): the callee may replace a field with
		// another argument's storage.
		if ref, isRef := paramType.(*RefType); isRef && ref != nil && ref.Mutable {
			if st, isStruct := peelToStructType(ref.Elem); isStruct && typeCarriesRegionStorage(st) {
				a.checkCallArgumentStructTargetStoreEscape(call, args, index)
				continue
			}
		}
		if !a.returnBorrowWritableParam(paramType) {
			continue
		}
		containerType := stripRefForBounds(a.exprType(args[index]))
		targetRegion := a.containerRegionOf(containerType)
		if targetRegion == "" {
			continue
		}
		if !a.containerMayHoldRegionRefs(containerType) {
			continue
		}
		// docs/128: when the CALLEE's parameter declares program-lived elements
		// (`darray[sview @source]&`), the callee can only push program-lived values into it --
		// its own stores are checked against that type. (The argument's type is not enough: a
		// `darray[sview @source]` passed to a plain `darray[sview]&` parameter is not enforced.)
		if len(a.programRegions) != 0 && a.darrayElemsProgramLived(paramType) {
			continue
		}
		stored := a.containerElementPointeesOf(containerType)
		for other, arg := range args {
			if other == index {
				continue
			}
			// The callee can only store region data reachable from this argument if some value it
			// reaches has a type the container's elements may point at: a `darray[Pattern]` of
			// handles cannot become the bytes an `sview` element points into.
			if !stored.intersects(storePointeesOf(other)) {
				continue
			}
			// Store-flow is directed: the container in `other` is dangerous only
			// when its contents can reach the writable container at `index`.
			// Symmetric connectivity alone also includes the reverse-only case
			// (the target feeds the source), which cannot create this escape.
			if a.callArgNeverReaches(call, other, index) {
				continue
			}
			state, ok := a.regionRefStateForExpr(arg)
			if !ok {
				continue
			}
			if elemState, onlyElements := a.callArgumentElementStateWithoutHeaderEscape(arg, containerType); onlyElements {
				// The destination cannot retain the source darray header, so only the
				// source elements' tracked provenance can escape through this argument.
				state = elemState
			}
			for _, region := range liveLocalRegionDependencyNames(state) {
				if a.isCompilerGeneratedAutoRegion(region) && a.autoRegionAdoptsCallerArena(targetRegion) {
					continue
				}
				if a.regionStoreEscapes(targetRegion, region) {
					a.errorf(arg.Pos(), "call may store a value in region %q into longer-lived region %q through argument %d; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before the call", a.regionDisplayName(region), a.regionDisplayName(targetRegion), index+1, a.regionDisplayName(region), a.regionDisplayName(targetRegion), a.regionDisplayName(targetRegion))
					break
				}
			}
		}
	}
}
