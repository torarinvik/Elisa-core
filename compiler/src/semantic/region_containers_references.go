package semantic

import (
	"sort"
	"strconv"

	"elisacore/src/ast"
)

// regionDisplayName is the name a region is allowed to APPEAR UNDER in a user-facing
// diagnostic. A compiler-synthesized auto region has no name the programmer wrote, and
// synthesizedAutoRegionName builds it from the declaration's BUFFER OFFSET -- so printing
// it raw made the message depend on how long the source file's PATH is: the identical
// program compiled from a different directory produced `region "__auto_129"` instead of
// `region "__auto_140"`. A diagnostic that changes with the containing directory cannot be
// asserted by any test and tells the reader nothing, so the inferred region reports under a
// fixed placeholder that no identifier can collide with. Every region the programmer named
// renders exactly as before.
func (a *Analyzer) regionDisplayName(name string) string {
	if a.isCompilerGeneratedAutoRegion(name) {
		return "<inferred>"
	}
	return name
}

// regionLifetimeOrdinal returns a region's position in the outlives-lattice: a
// LOWER ordinal means a longer-lived region. Region params are caller-owned and
// outlive every local region (ordinal 0). Local regions get their declaration
// ordinal (1+). Any other region identity ('heap, a borrowed `in <arena>:`, or
// an unknown name) is treated as outermost (0) — borrowed/process arenas outlive
// local regions. The second return reports whether the region is *comparable*
// (a known region param or tracked local region); incomparable regions must not
// drive a rejection.
func (a *Analyzer) regionLifetimeOrdinal(region string) (int, bool) {
	if a == nil || region == "" {
		return 0, false
	}
	if a.lookupRegionParam(region) {
		return 0, true
	}
	if ord, ok := a.regionLifetimeOrdinals[region]; ok {
		return ord, true
	}
	return 0, false
}

// regionOutlives reports whether region `outer` strictly outlives region `inner`
// — i.e. `inner` is freed first, so a pointer into `inner` stored in `outer`'s
// bucket would dangle. Only decides when the two regions are *comparable* in the
// lattice, which means TRUE LEXICAL NESTING: `outer` must be an enclosing
// ancestor of `inner` (or a caller-owned region param, which outlives every
// local). DISJOINT SIBLING regions (sequential `region a:` then `region b:`,
// neither nested in the other) are INCOMPARABLE — their lifetimes don't overlap,
// so neither outlives the other and this returns false. Declaration order alone
// (the old monotonic-ordinal model) wrongly ordered siblings; ancestry does not.
func (a *Analyzer) regionOutlives(outer, inner string) bool {
	if outer == inner {
		return false
	}
	// docs/128 rules 1-3: a program-lived region outlives every region; only a
	// program-lived region outlives one.
	if a.isProgramRegion(outer) {
		return true
	}
	if a.isProgramRegion(inner) {
		return false
	}
	// A caller-owned region param outlives every local region: it is a true
	// (implicit) ancestor of any region declared inside the function body.
	if a.lookupRegionParam(outer) {
		// Only orders against a comparable (tracked) inner; an unknown/borrowed
		// inner is not something we can prove is shorter-lived.
		_, innerTracked := a.regionLifetimeOrdinal(inner)
		return innerTracked && !a.lookupRegionParam(inner)
	}
	// Both must be tracked local regions to be comparable at all.
	if _, ok := a.regionLifetimeOrdinal(outer); !ok {
		return false
	}
	if _, ok := a.regionLifetimeOrdinal(inner); !ok {
		return false
	}
	// `outer` strictly outlives `inner` iff `outer` lexically encloses `inner`,
	// i.e. `outer` is in `inner`'s recorded ancestor set. Siblings appear in
	// neither's ancestor set → incomparable → false.
	return a.regionIsAncestor(outer, inner)
}

// regionIsAncestor reports whether region `outer` was lexically open (a live
// enclosing region) at the point region `inner` was declared.
func (a *Analyzer) regionIsAncestor(outer, inner string) bool {
	if a == nil {
		return false
	}
	for _, anc := range a.regionLifetimeAncestors[inner] {
		if anc == outer {
			return true
		}
	}
	return false
}

// regionStoreEscapes decides whether storing a value living in `valueRegion`
// into a slot living in `targetRegion` leaves a dangling reference — i.e. the
// target can be read after the value's region is freed. A store is UNSAFE unless
// the value is PROVABLY at least as long-lived as the target:
//
//   - same region                        → safe
//   - a caller-owned value region written into a local target → safe
//   - two uses of the same caller-owned region → safe
//   - distinct source-declared caller-owned region parameters → unsafe unless
//     an outlives relation is proven; either region may end before the other
//   - heap / untracked long-lived value region → safe for local targets
//   - both are tracked locals and the target is a true ANCESTOR of the value
//     (genuine nesting: target outlives value)            → UNSAFE
//   - both are tracked locals that are INCOMPARABLE (disjoint sibling regions,
//     neither nested in the other): the value's region may already be freed when
//     the target is later read, so we cannot prove the value outlives the target
//     → UNSAFE (conservative; the old monotonic-ordinal model wrongly accepted)
func (a *Analyzer) regionStoreEscapes(targetRegion, valueRegion string) bool {
	if a == nil || targetRegion == "" || valueRegion == "" || targetRegion == valueRegion {
		return false
	}
	// docs/128: a program-lived value outlives every target; a program-lived target
	// accepts only program-lived values.
	if a.isProgramRegion(valueRegion) {
		return false
	}
	if a.isProgramRegion(targetRegion) {
		return true
	}
	_, targetTracked := a.regionLifetimeOrdinal(targetRegion)
	_, valueTracked := a.regionLifetimeOrdinal(valueRegion)
	// The hidden region-polymorphic arena is caller-owned, but it has no proven
	// identity relation with a separate destination region parameter. A generated
	// auto RegionStmt inside a region-polymorphic function uses that same arena.
	regionPolyCallerArena := a.currentFuncType != nil && a.currentFuncType.RegionPolymorphic &&
		(valueRegion == regionPolymorphicImplicitParamName || a.isCompilerGeneratedAutoRegion(valueRegion))
	valueIsParam := a.lookupRegionParam(valueRegion) || regionPolyCallerArena
	targetIsParam := a.lookupRegionParam(targetRegion)
	if valueIsParam {
		if targetIsParam {
			// The region-polymorphic hidden caller arena is not a proof that a
			// distinct inferred destination binder names the same arena. The
			// equality fast path above is the only identity proof available here.
			if regionPolyCallerArena {
				return true
			}
			if a.isUnrelatedRegionParam(valueRegion) || a.isUnrelatedRegionParam(targetRegion) {
				return true
			}
			// Distinct source-declared region parameters are independently supplied
			// by the caller. Neither is known to outlive the other, so storing a
			// borrowed payload across them is unsafe unless the signature ties them
			// to the same name (handled by the equality fast path above). Generated
			// __rg_* binders use a separate inference contract; call-site validation
			// for relations between those binders is outside this source-declared
			// parameter check.
			if a.isSourceDeclaredRegionParam(valueRegion) && a.isSourceDeclaredRegionParam(targetRegion) {
				return !a.regionOutlives(valueRegion, targetRegion)
			}
			return false
		}
		// Caller-owned storage outlives a region local to this function.
		return false
	}
	// A value in an untracked region (heap, borrowed/process arena, unknown name)
	// outlives every local target — never an escape on the value side.
	if !valueTracked {
		return false
	}
	// Value is a tracked local region. If the target is NOT a tracked local
	// (it's a param/heap/long-lived arena), the value is shorter-lived than the
	// target → the original outlives rule already handled this as unsafe.
	if !targetTracked || targetIsParam {
		return a.regionOutlives(targetRegion, valueRegion)
	}
	// Both are tracked local regions. Safe ONLY when the value's region provably
	// outlives (encloses) the target's region — i.e. value is an ancestor of
	// target. Otherwise (target ancestor of value, OR disjoint siblings) it is
	// not provably safe → reject.
	return !a.regionIsAncestor(valueRegion, targetRegion)
}

func (a *Analyzer) isUnrelatedRegionParam(region string) bool {
	if a == nil || region == "" {
		return false
	}
	for i := len(a.regionUnrelatedParamScopes) - 1; i >= 0; i-- {
		if a.regionUnrelatedParamScopes[i][region] {
			return true
		}
	}
	return false
}

func (a *Analyzer) isSourceDeclaredRegionParam(region string) bool {
	if a == nil || a.currentFuncDecl == nil || region == "" {
		return false
	}
	for _, parameter := range a.currentFuncDecl.RegionParams {
		if parameter != region {
			continue
		}
		for _, inferred := range a.currentFuncDecl.InferredRegionParams {
			if inferred == region {
				return false
			}
		}
		return true
	}
	return false
}

// checkNestedRegionStoreEscape rejects storing a value whose region is freed
// before the target slot's region — e.g. an inner-`@r` container/ref written
// into an outer-region object (`darray[T] @outer`'s element typed `@inner`, a
// field `@inner` of an `@outer` struct, etc.). Once the inner region is freed,
// the still-live outer storage holds a dangling pointer. This is the nested-
// region case of the escape property, decided by the region outlives-lattice.
// The existing function-outliving store check (checkStoredRegionContainerEscape)
// covers the global / process-lifetime target; this covers the local outer ->
// inner case the lattice makes precise.
func (a *Analyzer) checkNestedRegionStoreEscape(targetExpr, valueExpr ast.Expr, targetType, valueType Type) {
	a.checkNestedRegionStoreEscapeAtScope(targetExpr, valueExpr, targetType, valueType, nil)
}

// checkNestedRegionStoreEscapeAtScope handles a destination whose declaration
// scope is known before its binding becomes visible to name lookup. Assignment
// sites pass nil and resolve their existing target binding normally.
func (a *Analyzer) checkNestedRegionStoreEscapeAtScope(targetExpr, valueExpr ast.Expr, targetType, valueType Type, declarationScope *Scope) {
	if a == nil || targetExpr == nil {
		return
	}
	valueRegion := containerOrEntryRegion(valueType)
	targetRegion := containerOrEntryRegion(targetType)
	if valueRegion == "" {
		// Only skip the flow query when the type walk proves the value contains no
		// references. Unknown wrappers, function values, pointers encoded as
		// integers, and other unresolved shapes stay on the full escape-check path.
		if a.returnTypeHasNoReferences(valueType, 0) {
			return
		}
		// A borrowed result may have a region-provenance summary without a region in its
		// nominal type (notably an unannotated `sview` returned from a parameter). Check
		// every concrete backing dependency at the store boundary; otherwise a call can
		// erase the source region and place the view into a longer-lived binding.
		if state, ok := a.regionRefStateForExpr(valueExpr); ok {
			a.checkUnannotatedParamViewStore(targetExpr, targetRegion, state)
			regions := liveRegionDependencyNames(state)
			sort.Strings(regions)
			for _, valueRegion := range regions {
				if targetRegion == "" {
					if declarationScope != nil {
						a.checkRegionlessTargetStoreEscapeInScope(targetExpr, valueRegion, declarationScope)
					} else {
						a.checkRegionlessTargetStoreEscape(targetExpr, valueRegion)
					}
					continue
				}
				if a.regionStoreEscapes(targetRegion, valueRegion) {
					a.errorf(targetExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(targetRegion))
				}
			}
		}
		return
	}
	if targetRegion == "" {
		// Region-less target slot (a plain local, or a field of a region-less
		// struct). The slot still dangles if its backing storage outlives the
		// value's region block: a binding declared in a scope that strictly
		// encloses the region's declaration scope survives the region's exit.
		if declarationScope != nil {
			a.checkRegionlessTargetStoreEscapeInScope(targetExpr, valueRegion, declarationScope)
		} else {
			a.checkRegionlessTargetStoreEscape(targetExpr, valueRegion)
		}
		return
	}
	if a.regionStoreEscapes(targetRegion, valueRegion) {
		a.errorf(targetExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(targetRegion))
	}
}

// checkUnannotatedParamViewStore rejects storing a value borrowed from a region-less view or
// reference PARAMETER into storage of a source-declared region parameter or a program-lived
// region. The parameter's referent is only known to outlive the call, not that region: a caller
// may pass a view of a local string (`def f[@a](n: mutable Node[a]&, s: sview): n.name <- s`).
// Annotating the parameter with the target region (`s: sview @a`) makes the caller prove it.
func (a *Analyzer) checkUnannotatedParamViewStore(targetExpr ast.Expr, targetRegion string, state regionRefState) {
	if a == nil || targetRegion == "" || a.currentFuncType == nil {
		return
	}
	if !a.isProgramRegion(targetRegion) && !a.isSourceDeclaredRegionParam(targetRegion) {
		return
	}
	params := state.ParamDeps.Clone()
	if state.HasDirectParamDep {
		params.Add(state.DirectParamDep)
	}
	reported := false
	params.ForEach(func(index int) {
		if reported || index < 0 || index >= len(a.currentFuncType.Params) {
			return
		}
		paramType := a.currentFuncType.Params[index]
		if containerOrEntryRegion(paramType) != "" || !a.typeCanContainRegionRefs(stripRefForBounds(paramType), map[string]bool{}) {
			return
		}
		if len(a.programRegions) != 0 && a.typeIsProgramLivedCached(paramType) {
			return
		}
		if ref, isRef := paramType.(*RefType); isRef && ref != nil && ref.Region != "" {
			// A region-annotated reference (declared or inferred): its region states the
			// lifetime, and the region escape checks judge the store against it.
			return
		}
		if a.containerViewsLiveIn(paramType, targetRegion) {
			return // `darray[sview @r]&` stored into `r` (or the program region)
		}
		name := ""
		if a.currentFuncDecl != nil && index < len(a.currentFuncDecl.Params) {
			name = a.currentFuncDecl.Params[index].Name
		}
		reported = true
		a.errorf(targetExpr.Pos(), "parameter %q is borrowed from the caller with no region, so it is not known to outlive region %q; storing it there may leave a dangling reference. Annotate the parameter `@%s`", name, a.regionDisplayName(targetRegion), targetRegion)
	})
}

// checkRegionParamViewArgs: an argument passed to a parameter typed in one of the callee's
// region parameters (`s: sview @a`) must live at least as long as the caller region bound to
// it -- the callee may store it in that region's storage. A view of a local string passed where
// `a` is bound to the caller's own region parameter would dangle once the local is freed.
func (a *Analyzer) checkRegionParamViewArgs(ft *FuncType, args []ast.Expr, regionBindings map[string]string) {
	if a == nil || ft == nil || (len(ft.RegionParams) == 0 && len(a.programRegions) == 0) || a.suppressDiagnostics || a.staticContextDepth != 0 {
		return
	}
	// One region parameter bound to two different caller regions: the callee may store a
	// value of one into storage of the other, so the shorter one must not be outlived.
	for _, conflict := range a.callRegionBindingConflicts {
		first, second := conflict[1], conflict[2]
		if len(args) > 0 && args[0] != nil && (a.regionStoreEscapes(first, second) || a.regionStoreEscapes(second, first)) {
			a.errorf(args[0].Pos(), "region parameter %q of %q is bound to both %q and %q; the callee may store a value of one into the other, and they do not share a lifetime. Pass values of the same region", conflict[0], ft.Name, a.regionDisplayName(first), a.regionDisplayName(second))
			break
		}
	}
	for index, paramType := range ft.Params {
		if index >= len(args) || args[index] == nil {
			continue
		}
		a.checkProgramViewElementArg(paramType, args[index])
		region := containerOrEntryRegion(paramType)
		if region == "" || stripRefForBounds(paramType) == nil {
			continue
		}
		if _, isView := paramType.(*SViewType); !isView {
			continue
		}
		bound, ok := regionBindings[region]
		// A concrete program region is already bound by its declaration. It
		// carries the same caller obligation as an instantiated region parameter.
		if a.isProgramRegion(region) {
			bound, ok = region, true
		}
		if !ok || bound == "" || bound == region && !a.lookupRegionParam(bound) && !a.isProgramRegion(bound) {
			continue
		}
		// A region parameter or program region outlives this call's frame: a borrow of a
		// function-local value (a local string's view) cannot be passed into it.
		if a.lookupRegionParam(bound) || a.isProgramRegion(bound) {
			a.checkAggregateBorrowEscapesLocal(args[index], "argument for a parameter in region "+strconv.Quote(a.regionDisplayName(bound))+" borrows function-local storage")
		}
		state, ok := a.regionRefStateForExpr(args[index])
		if !ok {
			continue
		}
		a.checkUnannotatedParamViewStore(args[index], bound, state)
		for _, valueRegion := range liveLocalRegionDependencyNames(state) {
			if valueRegion == bound {
				continue
			}
			if a.regionStoreEscapes(bound, valueRegion) {
				a.errorf(args[index].Pos(), "argument in region %q is passed where region %q is required; region %q is freed first, so the callee may keep a dangling reference. Copy it into region %q first", a.regionDisplayName(valueRegion), a.regionDisplayName(bound), a.regionDisplayName(valueRegion), a.regionDisplayName(bound))
				break
			}
		}
	}
}

// checkProgramViewElementArg checks the view elements separately from a darray's
// backing region. A local buffer can carry program-lived views, but a borrowed
// element type cannot acquire that promise merely by passing through a call.
func (a *Analyzer) checkProgramViewElementArg(paramType Type, arg ast.Expr) {
	param, ok := stripRefForBounds(paramType).(*DArrayType)
	if !ok || param == nil {
		return
	}
	view, ok := param.Elem.(*SViewType)
	if !ok || view == nil || !a.isProgramRegion(view.Region) {
		return
	}
	actual, ok := stripRefForBounds(a.exprType(arg)).(*DArrayType)
	if !ok || actual == nil {
		return
	} // ordinary type checking owns incompatible arguments
	actualView, ok := actual.Elem.(*SViewType)
	if !ok || actualView == nil {
		return
	}
	if a.isProgramRegion(actualView.Region) {
		return
	}
	a.errorf(arg.Pos(), "argument elements must be views in program-lived region %q; the element type does not guarantee that lifetime. Pass a darray[sview @%s] whose elements satisfy the declared region", a.regionDisplayName(view.Region), a.regionDisplayName(view.Region))
}

func liveRegionDependencyNames(state regionRefState) []string {
	// Parameter-only provenance, packed-store provenance, and empty states do not
	// name a concrete live region. This is the common local-initializer path; avoid
	// creating traversal maps for it.
	if len(state.Deps) == 0 && len(state.Fields) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	seenFields := make(map[uintptr]struct{})
	var visit func(regionRefState)
	visit = func(current regionRefState) {
		for region, dependency := range current.Deps {
			if region != nil && dependency.Valid {
				seen[region.Name] = struct{}{}
			}
		}
		fieldsID := regionRefFieldsIdentity(current.Fields)
		if fieldsID != 0 {
			if _, alreadyVisited := seenFields[fieldsID]; alreadyVisited {
				return
			}
			seenFields[fieldsID] = struct{}{}
		}
		for _, field := range current.Fields {
			visit(field)
		}
	}
	visit(state)
	regions := make([]string, 0, len(seen))
	for region := range seen {
		regions = append(regions, region)
	}
	return regions
}

// checkNestedRegionElementStoreEscape is the container-mutation form of the
// nested-region store check (push / dict.put / set.add). Element types are
// never region-stamped, so when the slot's own type carries no region the
// element's true lifetime is the CONTAINER's region — fall back to it.
func (a *Analyzer) checkNestedRegionElementStoreEscape(argExpr ast.Expr, containerType, elemType, valueType Type) {
	if a == nil || argExpr == nil {
		return
	}
	targetRegion := containerOrEntryRegion(elemType)
	if targetRegion == "" {
		targetRegion = containerRegion(containerType)
	}
	if targetRegion == "" {
		return
	}
	// An element pushed into program-region or source-declared region storage must not be a
	// view borrowed from a region-less parameter (the assignment form's rule, applied to push).
	if state, ok := a.regionRefStateForExpr(argExpr); ok {
		a.checkUnannotatedParamViewStore(argExpr, targetRegion, state)
	}
	valueRegion := containerOrEntryRegion(valueType)
	if valueRegion == "" {
		// Region-polymorphic enum results keep their lifetime in the region-ref
		// provenance side table rather than in the nominal enum type. Prefer that
		// proven caller-parameter region before classifying the value as a fresh
		// enum allocation; otherwise a returned AST node can be mistaken for a
		// short-lived auto-region value when it is pushed back into the same
		// caller-owned container.
		if state, ok := a.regionRefStateForExpr(argExpr); ok {
			if parameterRegion, known := a.currentParamRegionFromRefState(state); known {
				valueRegion = parameterRegion
			} else if region := liveLocalRegionDependencyName(state); region != "" {
				// A call result that borrows a local container's buffer (`out.push(bytes_view(xs))`)
				// has a region-free nominal type; its provenance still names the container's region.
				// A synthesized auto region that ADOPTS the caller's arena (regionPolyAutoAdopts)
				// frees nothing, so a borrow of its storage is caller-owned: the same sanction the
				// fresh-enum-payload path below grants.
				if a.isCompilerGeneratedAutoRegion(region) && a.autoRegionAdoptsCallerArena(targetRegion) {
					return
				}
				if a.regionStoreEscapes(targetRegion, region) {
					a.errorf(argExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", a.regionDisplayName(region), a.regionDisplayName(targetRegion), a.regionDisplayName(region), a.regionDisplayName(targetRegion), a.regionDisplayName(targetRegion))
				}
				return
			}
		}
	}
	if valueRegion == "" {
		// An INLINE value-enum constructor (`out.push(Item.Row(make_vals(10)))`) wraps its
		// payload directly, but the enum TYPE carries no region stamp, so the store looked
		// region-free and slipped past this check (silent use-after-free). When a payload
		// argument carries region storage, the wrapped value lives in the INNERMOST active
		// allocation region (the same region stampContainerRegion would have stamped on a bare
		// container here) — use that as the value's region. The one sanctioned exception: a
		// void grower whose ambient region was bound to this very target (adoption routes the
		// payload into the target's arena, so nothing dangles).
		valueRegion = a.enumCtorPayloadRegion(argExpr)
		if valueRegion == "" {
			// The ctor may have been bound to a local first (`node = Item.Row(mk()); out.push(node)`)
			// — the taint side-table carries the payload's region across that binding.
			valueRegion = a.structInteriorTaintRegion(argExpr)
		}
		if valueRegion == "" {
			// An owned element/field read (`xs[0].items`, `b.items`, `xs.pop()`, a loop binder)
			// copies only a header; its backing lives in the root's region.
			valueRegion = a.ownedExtractionRegion(argExpr)
		}
		if valueRegion == "" {
			return
		}
		// Sanctioned adopted cases (mirrors the backend's regionPolyAutoAdopts): the function's
		// synthesized auto region ADOPTS a caller-provided arena, so the wrapped payload actually
		// lands in caller-owned storage and nothing dangles.
		if a.isCompilerGeneratedAutoRegion(valueRegion) && a.autoRegionAdoptsCallerArena(targetRegion) {
			return
		}
	}
	if a.regionStoreEscapes(targetRegion, valueRegion) {
		a.errorf(argExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(targetRegion))
	}
}
