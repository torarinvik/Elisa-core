package semantic

import (
	"maps"
	"slices"

	"elisacore/src/ast"
)

// currentParamRegionFromRefState resolves a region-ref summary's parameter
// dependencies against the active function signature. It returns a region only
// when every dependency names the same region-bearing parameter and no local or
// packed-store dependency is mixed in; ambiguity stays conservative and lets the
// existing fresh-value checks reject the store.
func (a *Analyzer) currentParamRegionFromRefState(state regionRefState) (string, bool) {
	if a == nil || a.currentFuncType == nil || len(state.Deps) != 0 || len(state.StoreDeps) != 0 {
		return "", false
	}
	region := ""
	seenParam := false
	acceptParam := func(index int) bool {
		if index < 0 || index >= len(a.currentFuncType.Params) {
			return false
		}
		candidate := regionParamReturnTypeRegion(a.currentFuncType.Params[index])
		if candidate == "" {
			return false
		}
		if region == "" {
			region = candidate
		} else if region != candidate {
			return false
		}
		seenParam = true
		return true
	}
	if state.HasDirectParamDep && !acceptParam(state.DirectParamDep) {
		return "", false
	}
	valid := true
	state.ParamDeps.ForEach(func(index int) {
		if valid && !acceptParam(index) {
			valid = false
		}
	})
	for _, fieldState := range state.Fields {
		if valid {
			fieldRegion, fieldKnown := a.currentParamRegionFromRefState(fieldState)
			if !fieldKnown {
				valid = false
			} else if region == "" {
				region = fieldRegion
			} else if fieldRegion != region {
				valid = false
			}
			// A known field recursively proves at least one parameter dependency.
			// Preserve that evidence when the aggregate has no root dependency.
			if fieldKnown && valid {
				seenParam = true
			}
		}
	}
	return region, valid && seenParam
}

// enumCtorPayloadRegion classifies an enum-constructor expression — bare (`Item.Row(x)`) or
// through `new` (`new Stmt.Block(body: kids)`) — whose payload arguments carry region storage.
// The enum TYPE carries no region stamp, so region checks must inspect the wrapped payloads. An
// existing region-bearing payload keeps its own region; only a region-carrying expression with no
// recoverable provenance falls back to the active allocation region. The shortest comparable
// payload region is returned so a fresh, shorter-lived payload is not hidden by an older one.
func (a *Analyzer) enumCtorPayloadRegion(expr ast.Expr) string {
	if a == nil || expr == nil {
		return ""
	}
	inner := unwrapParen(expr)
	if alloc, isAlloc := inner.(*ast.AllocExpr); isAlloc && alloc != nil {
		inner = unwrapParen(alloc.Value)
	}
	call, ok := inner.(*ast.CallExpr)
	if !ok || call == nil {
		return ""
	}
	// Resolve `Enum.Variant(...)` WITHOUT packedAllocConstructorInfo: that helper reports
	// "enum has no variant" as a side effect, which would fabricate diagnostics here (this is a
	// query, and a non-variant member call like `Expr.Store(..)` is legitimate elsewhere).
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil {
		return ""
	}
	baseName, ok := qualifiedTypePathFromExpr(fieldExpr.Object)
	if !ok {
		return ""
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return ""
	}
	enumType, ok := base.(*EnumType)
	if !ok || enumType == nil {
		return ""
	}
	if _, isVariant := enumType.Variant(fieldExpr.Field); !isVariant {
		return ""
	}
	if enumType.DeclaredStoreRegion() != "" {
		// docs/128 §8: payloads were checked as stores into the program-lived store at the
		// constructor and adopted into its arena; the handle carries no shorter region.
		return ""
	}
	region := ""
	for _, payloadArg := range call.Args {
		payloadType := a.exprType(payloadArg)
		if !typeCarriesRegionStorage(payloadType) {
			continue
		}
		payloadRegion := a.valueStoreRegion(payloadArg, payloadType)
		if payloadRegion == "" {
			payloadRegion = a.activeContainerRegionName()
		}
		region = a.innerRegion(region, payloadRegion)
	}
	return region
}

// checkNestedRegionBulkStoreEscape is the bulk form of the element-store check
// (darray.extend, bulk darray.push from a darray/view/array source). The values
// copied in are the SOURCE container's elements; when the element type itself
// carries region storage (a nested container/view/ref), those elements live in
// the source container's region, so copying them into a longer-lived target
// container leaves dangling references once the source region is freed. Scalar
// (pointer-free) elements can never dangle, so they are exempt — a per-element
// region stamp is never available for a bulk source, so the source container's
// own region is the proxy for its elements' lifetime.
func (a *Analyzer) checkNestedRegionBulkStoreEscape(sourceExpr, receiverExpr ast.Expr, containerType, elemType, sourceType Type) {
	if a == nil || sourceExpr == nil {
		return
	}
	if !typeCarriesRegionStorage(elemType) {
		return // scalar / pointer-free elements can't dangle.
	}
	// docs/128: elements whose declared type is program-lived (darray[sview @source]) do not
	// live in the source container's region; copying them dangles nowhere.
	if len(a.programRegions) != 0 {
		if da, ok := stripRefForBounds(sourceType).(*DArrayType); ok && da.Elem != nil && a.typeIsProgramLivedCached(da.Elem) {
			return
		}
	}
	valueRegion := containerRegion(sourceType)
	if valueRegion == "" {
		return
	}
	targetRegion := containerOrEntryRegion(elemType)
	if targetRegion == "" {
		targetRegion = containerRegion(containerType)
	}
	if targetRegion == "" {
		// Region-less target container (a plain local declared outside the
		// source's region block): dangles if its scope outlives the region.
		if receiverExpr != nil {
			a.checkRegionlessTargetStoreEscape(receiverExpr, valueRegion)
		}
		return
	}
	if a.regionStoreEscapes(targetRegion, valueRegion) {
		a.errorf(sourceExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(targetRegion))
	}
}

// structInteriorTaintRegion returns the innermost region a region-less aggregate
// local was tainted with (an interior container/view field stored an inner-region
// value), peeling parens/casts/moves down to the root identifier. "" when the
// expression is not a tainted local.
func (a *Analyzer) structInteriorTaintRegion(expr ast.Expr) string {
	if a == nil || a.currentScope == nil || a.currentStructInteriorRegionTaint == nil {
		return ""
	}
	ident, ok := stripParenExpr(expr).(*ast.Ident)
	if !ok || ident == nil {
		return ""
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return ""
	}
	return a.currentStructInteriorRegionTaint[sym]
}

// valueStoreRegion returns the innermost (shortest-lived) tracked region a stored
// value carries: a container/view's own stamped region, or — for a region-less
// aggregate — the interior-field taint recorded for its source local. Only
// lattice-comparable (tracked-local) regions are reported; "" otherwise.
// declaredCallValueRegion recovers only a live, resolved return contract.
func (a *Analyzer) declaredCallValueRegion(expr ast.Expr) string {
	if call, ok := stripParenExpr(expr).(*ast.CallExpr); ok {
		if region := a.declaredCallReturnRegions[call]; region != "" {
			if a.lookupRegionParam(region) {
				return region
			}
			if owner, state := a.lookupRegionState(region); owner != nil && !state.Destroyed {
				return region
			}
		}
	}
	return ""
}

func (a *Analyzer) valueStoreRegion(expr ast.Expr, valueType Type) string {
	if r := containerRegion(valueType); r != "" {
		if _, ok := a.regionLifetimeOrdinal(r); ok {
			return r
		}
		// Program regions have no lexical lifetime ordinal. An aggregate whose
		// complete storage-bearing type names one already owns program-lived
		// storage; do not replace that contract with a producer call's ambient
		// arena. The whole-type check is required: common-region collection alone
		// does not inspect enum payloads or every generic-instance base.
		// Resolve the binding as well as its spelling so a shadow cannot
		// supply this evidence.
		if a.isProgramRegion(r) && a.typeIsProgramLivedCached(valueType) && a.currentScope != nil && a.globalScope != nil {
			if sym, ok := a.currentScope.Lookup(r); ok && sym != nil && sym == a.globalScope.Symbols[r] && sym.Kind == SymbolRegion {
				if _, declared := sym.Node.(*ast.GlobalRegionDecl); declared {
					// This API tracks local storage dependencies, not global owners.
					return ""
				}
			}
		}
	}
	// docs/128 §8: a local declared program-lived holds nothing a local region frees,
	// whatever taint the value it was initialised from carried.
	if id, ok := stripParenExpr(expr).(*ast.Ident); ok && len(a.programRegions) != 0 && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(id.Name); found && sym != nil && sym.Kind == SymbolLocal && a.typeIsProgramLivedCached(sym.Type) {
			return ""
		}
	}
	if r := a.innerRegion(a.structInteriorTaintRegion(expr), a.ownedExtractionRegion(expr)); r != "" {
		return r
	}
	// An explicit return contract names the result's owner even when the
	// aggregate has no nominal container-region stamp.
	if region := a.declaredCallValueRegion(expr); region != "" {
		return region
	}
	// A region-polymorphic call result is region-less by type but lives in the AMBIENT
	// (threaded) region. Storing it into an interior container/view field of a region-less
	// aggregate tains that aggregate with the ambient region — otherwise a later
	// copy/return/store of the struct launders the dangling interior past the region's death
	// unflagged. Mirror the carve-out in checkRegionContainerEscape. Only ADDS taint: the
	// outlives-lattice in the escape check still decides whether it actually escapes, so a
	// result that genuinely outlives its use is not over-rejected.
	if region := a.activeContainerRegionName(); region != "" && a.exprIsRegionPolyResultCarryingRegionData(expr, valueType) {
		if _, ok := a.regionLifetimeOrdinal(region); ok {
			return region
		}
	}
	return ""
}

// innerRegion returns whichever of two regions is shorter-lived (higher lifetime
// ordinal). Incomparable regions defer to the comparable one.
func (a *Analyzer) innerRegion(r1, r2 string) string {
	if r1 == "" {
		return r2
	}
	if r2 == "" || r1 == r2 {
		return r1
	}
	o1, ok1 := a.regionLifetimeOrdinal(r1)
	o2, ok2 := a.regionLifetimeOrdinal(r2)
	if !ok1 {
		return r2
	}
	if !ok2 {
		return r1
	}
	if o2 > o1 {
		return r2
	}
	return r1
}

// recordStructInteriorRegionTaint maintains the interior-field taint side-table on
// an assignment/binding. A field store `base.field <- v` (or any deeper path with a
// local root) taints the root aggregate with v's region; a whole-aggregate copy
// `dest <- src`/`let dest = src` of a tainted region-less struct propagates the
// taint so the dangling interior reference is tracked through copy chains. This is
// what lets checkStructCopyInteriorRegionEscape reject laundering a region-less
// struct's dangling interior field out past the region's death.
func (a *Analyzer) recordStructInteriorRegionTaint(target, value ast.Expr, valueType Type) {
	if a == nil || a.currentStructInteriorRegionTaint == nil || a.currentScope == nil {
		return
	}
	root := rootIdentExpr(target)
	if root == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(root.Name)
	if !ok || sym == nil {
		return
	}
	if _, isField := stripParenExpr(target).(*ast.FieldExpr); isField {
		// Field store into an aggregate: taint the root with the value's region.
		region := a.valueStoreRegion(value, valueType)
		if region == "" {
			return
		}
		a.currentStructInteriorRegionTaint[sym] = a.innerRegion(a.currentStructInteriorRegionTaint[sym], region)
		return
	}
	// Whole-aggregate assignment to a plain local: propagate source taint and
	// inspect fresh producers too. A struct literal assigned only in one branch
	// is otherwise opaque after it is bound to the local.
	if _, isIdent := stripParenExpr(target).(*ast.Ident); !isIdent {
		return
	}
	// An enum CONSTRUCTOR bound to a local (`node: Item = Item.Row(make_vals(10))`, or the
	// packed `node: Node = new Stmt.Block(body: kids)`) taints the local with the payload's
	// fresh region. Without this the ident launders the payload past every store check — the
	// enum type carries no region stamp and the ctor expression is gone by the time `node` is
	// pushed. Checked BEFORE the container gates below: a PACKED handle deliberately fails
	// typeCarriesRegionStorage, yet its variant payload dangles just the same.
	if region := a.enumCtorPayloadRegion(value); region != "" {
		a.currentStructInteriorRegionTaint[sym] = a.innerRegion(a.currentStructInteriorRegionTaint[sym], region)
		return
	}
	if !typeCarriesRegionStorage(valueType) || containerRegion(valueType) != "" {
		return
	}
	if region := a.valueInteriorRegion(value); region != "" {
		a.currentStructInteriorRegionTaint[sym] = a.innerRegion(a.currentStructInteriorRegionTaint[sym], region)
	}
}

// argInteriorLocalRegion returns the interior-taint region of a by-value aggregate argument when that
// region is a live scope-owned LOCAL region (the dangerous case: it frees at block exit). "" for a
// region param the caller owns, a destroyed/unknown region, or an untainted argument.
func (a *Analyzer) argInteriorLocalRegion(arg ast.Expr) string {
	taint := a.structInteriorTaintRegion(arg)
	if taint == "" || a.isCompilerGeneratedAutoRegion(taint) {
		return ""
	}
	if sym, state := a.lookupRegionState(taint); sym == nil || state.Destroyed || state.DeclScope == nil {
		return ""
	}
	return taint
}

// checkInterprocStoreEscape rejects passing a by-value struct, whose interior field was grown in a
// scope-owned local region, as an argument that the callee STORES into another argument that outlives
// that region (docs/91 S4 W5: `def stash(dst: darray[Mod]&, v: Mod): dst.push(v)` then `stash(mods, m)`
// with m grown in an inner region and mods in an outer one — m's field dangles when inner frees but
// mods keeps the reference). The callee body is locally correct; the escape is a CALL-SITE property,
// resolved here via the structural store-target summary (paramStoreTargets) + the live interior-taint
// table. Precise: fires only when the target argument's region provably outlives the source's taint
// region, so a same-region cohort pass (`stash(mods, m)` both in one region) is accepted.
func (a *Analyzer) lookupParamStoreTargets(callee *ast.FuncDecl) ([]map[int]bool, bool) {
	if a == nil {
		return nil, false
	}
	targets, ok := a.paramStoreTargets[callee]
	if !ok && a.parallel != nil && a.parallel.specialization != nil {
		original := a.parallel.original[callee]
		if original == nil {
			original = callee
		}
		targets, ok = a.parallel.specialization.storeTargets[original]
	}
	return targets, ok
}

func (a *Analyzer) checkInterprocStoreEscape(call *ast.CallExpr, orderedArgs []ast.Expr) {
	if a == nil || call == nil {
		return
	}
	if a.paramStoreTargets == nil && (a.parallel == nil || a.parallel.specialization == nil || a.parallel.specialization.storeTargets == nil) {
		return
	}
	callee, ok := a.resolveCalleeFuncDecl(call)
	if !ok || callee == nil {
		return
	}
	targets, ok := a.lookupParamStoreTargets(callee)
	if !ok {
		return
	}
	for i, arg := range orderedArgs {
		if i >= len(targets) || len(targets[i]) == 0 {
			continue
		}
		srcRegion := a.argInteriorLocalRegion(arg)
		if srcRegion == "" {
			continue
		}
		for _, tj := range slices.Sorted(maps.Keys(targets[i])) { // map: sort for a stable report order
			if tj == storeTargetGlobal {
				// The callee stores the argument into program-lifetime storage (a global/perm
				// container, or relayed there) — that outlives every local region unconditionally.
				a.errorf(arg.Pos(), "value in region %q is stored by the callee into program-lifetime storage, which outlives the region; region %q is freed first, leaving a dangling reference. Build the value into a program-lifetime region (e.g. `perm`) before passing it", a.regionDisplayName(srcRegion), a.regionDisplayName(srcRegion))
				continue
			}
			if tj < 0 || tj >= len(orderedArgs) {
				continue
			}
			targetArg := orderedArgs[tj]
			targetRegion := containerRegion(a.exprType(targetArg))
			if targetRegion == "" {
				a.checkRegionlessTargetStoreEscape(targetArg, srcRegion)
				continue
			}
			if a.regionOutlives(targetRegion, srcRegion) {
				a.errorf(arg.Pos(), "value in region %q is stored by the callee into a longer-lived argument in region %q; region %q is freed first, leaving a dangling reference. Build the value into region %q (or a region that outlives it) before passing it", a.regionDisplayName(srcRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(srcRegion), a.regionDisplayName(targetRegion))
			}
		}
	}
}

// returnedAggregateTaintRegion returns the interior-TAINT region (a grown field's region, recorded in
// the taint side-table — never a value's OWN stamped container/view region) reachable through a
// by-value aggregate return: a tainted struct local, or the elements/fields of a fresh producer
// (list/struct literal, comprehension, ternary), recursing. The value's own container/view region is
// deliberately EXCLUDED — that is what checkReturnRegionContainerEscape / checkRegionParamReturnEscape
// handle (incl. the accepted explicit-`@r` and within-lifetime forms); folding it in here over-rejects.
func (a *Analyzer) returnedAggregateTaintRegion(expr ast.Expr) string {
	if a == nil || expr == nil {
		return ""
	}
	switch n := stripParenExpr(expr).(type) {
	case *ast.MoveExpr:
		if n == nil {
			return ""
		}
		// Moving ownership does not extend the lifetime of interior buffers.
		return a.returnedAggregateTaintRegion(n.Operand)
	case *ast.ListLitExpr:
		r := ""
		for i, e := range n.Elems {
			if i < len(n.Spreads) && n.Spreads[i] {
				continue
			}
			r = a.innerRegion(r, a.returnedAggregateTaintRegion(e))
		}
		return r
	case *ast.ListComprehensionExpr:
		r := a.returnedAggregateTaintRegion(n.Value)
		if n.Key != nil {
			r = a.innerRegion(r, a.returnedAggregateTaintRegion(n.Key))
		}
		return r
	case *ast.TernaryExpr:
		return a.innerRegion(a.returnedAggregateTaintRegion(n.Value), a.returnedAggregateTaintRegion(n.Alt))
	case *ast.StructLitExpr:
		r := ""
		for _, arg := range n.Args {
			r = a.innerRegion(r, a.returnedAggregateTaintRegion(arg))
		}
		for _, sp := range n.Spreads {
			r = a.innerRegion(r, a.returnedAggregateTaintRegion(sp))
		}
		return r
	default:
		return a.structInteriorTaintRegion(stripParenExpr(expr))
	}
}

// recordGrownFieldInteriorTaint taints a region-less aggregate local when one of its CONTAINER FIELDS
// is grown (`m.bits.push(..)`) into the ambient region. The growth allocates the field's backing in
// the ambient region (the lexical ambient-region-at-push rule), so a later by-VALUE copy of the whole
// struct (`mods.push(m)`) carries an interior reference into that region — exactly the interior taint
// the store-side checks consult via valueStoreRegion. Mirrors the field-store taint path
// (recordStructInteriorRegionTaint), but the region source is the ambient alloc context, not an
// assigned value. Only a field-path receiver taints (a bare container local carries its own region).
func (a *Analyzer) recordGrownFieldInteriorTaint(receiverExpr ast.Expr) {
	if a == nil || a.currentStructInteriorRegionTaint == nil || a.currentScope == nil {
		return
	}
	field, ok := stripParenExpr(receiverExpr).(*ast.FieldExpr)
	if !ok || field == nil {
		return
	}
	root := rootIdentExpr(field)
	if root == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(root.Name)
	if !ok || sym == nil {
		return
	}
	region := a.activeContainerRegionName()
	if region == "" {
		return
	}
	if _, tracked := a.regionLifetimeOrdinal(region); !tracked {
		return
	}
	a.currentStructInteriorRegionTaint[sym] = a.innerRegion(a.currentStructInteriorRegionTaint[sym], region)
}

// checkStructCopyInteriorRegionEscape rejects copying a region-less aggregate that
// holds an interior reference into an inner region (recorded via interior taint)
// into storage that outlives that region. A struct's TYPE never carries its
// fields' regions, so checkNestedRegionStoreEscape (which keys off the value type's
// region) is blind to this laundering; the taint side-table supplies the region.
func (a *Analyzer) checkStructCopyInteriorRegionEscape(targetExpr ast.Expr, targetType Type, valueExpr ast.Expr, valueType Type) {
	if a == nil || targetExpr == nil {
		return
	}
	// Only aggregates: a stamped container value's region is on its type and is
	// already handled by checkNestedRegionStoreEscape.
	if !typeCarriesRegionStorage(valueType) || containerRegion(valueType) != "" {
		return
	}
	valueRegion := a.structInteriorTaintRegion(valueExpr)
	if valueRegion == "" {
		return
	}
	targetRegion := containerOrEntryRegion(targetType)
	if targetRegion == "" {
		a.checkRegionlessTargetStoreEscape(targetExpr, valueRegion)
		return
	}
	if a.regionStoreEscapes(targetRegion, valueRegion) {
		a.errorf(targetExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(valueRegion), a.regionDisplayName(targetRegion), a.regionDisplayName(targetRegion))
	}
}
