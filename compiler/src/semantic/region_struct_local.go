package semantic

import "elisacore/src/ast"

// Locally-constructed structs that own region-allocated container fields need a region
// identity so a call site can thread that region into a callee's struct-ref region param
// (`def f(b: mutable Bag& @r)`). The region rides the ARGUMENT's RefType wrapper — never the
// StructType (the "a struct's TYPE never carries its fields' regions" invariant, region_containers.go)
// — exactly as a struct PARAM threads its region. This is the locally-constructed analogue of the
// already-supported param-threading path: the field-growth in the callee is allocated into the
// recorded region, and the same interior-taint / region-poly-adopt escape checks that guard the
// param-threaded form cover every borrow-out vector here too (no new lifetime power).

// peelToStructType strips Ref wrappers and returns the underlying StructType.
func peelToStructType(t Type) (*StructType, bool) {
	for {
		if rt, ok := t.(*RefType); ok {
			t = rt.Elem
			continue
		}
		st, ok := t.(*StructType)
		return st, ok
	}
}

// structHasRegionlessContainerField reports whether a struct has at least one region-less
// container/view field — the fields that an ambient region would back at construction and that
// a callee could grow through a struct-ref region param.
func structHasRegionlessContainerField(st *StructType) bool {
	if st == nil {
		return false
	}
	for _, f := range st.Fields {
		switch ft := f.Type.(type) {
		case *DArrayType:
			if ft != nil && ft.Region == "" {
				return true
			}
		case *DictType:
			if ft != nil && ft.Region == "" {
				return true
			}
		case *SetType:
			if ft != nil && ft.Region == "" {
				return true
			}
		}
	}
	return false
}

// recordStructLocalAllocRegion records the ambient region that backs a freshly-constructed
// struct local's container fields, so call sites can thread it into a callee's region param.
// No-ops unless there is an ambient region, the binding is a container-bearing struct, and the
// initializer genuinely produces a struct whose container fields live in that ambient region.
//
// Two admissible initializer forms, both of which allocate the struct's container fields into the
// caller's ambient region:
//
//   (1) a fresh struct literal — `bag: Bag = Bag([], [])` — the inline-construction case; or
//   (2) a call to a region-POLYMORPHIC function returning container-bearing data —
//       `bag: Bag = make_bag()`. A region-poly result is allocated in the ambient (threaded)
//       region even though its type is region-less (the region rides the threaded arena, not the
//       type), so it lives in EXACTLY the same region a struct-literal local would (see
//       region_containers.go's region-poly carve-out / exprIsRegionPolyResultCarryingRegionData).
//
// SOUNDNESS: form (2) is gated on exprIsRegionPolyResultCarryingRegionData, which requires the
// callee be RegionPolymorphic AND the result transitively carry region storage. A function that
// returns a struct holding data from some OTHER region (e.g. a region-param it was handed) is NOT
// region-polymorphic on that data, so it fails the gate and is not recorded — the local then falls
// back to the conservative "cannot infer region parameter" rejection rather than being threaded
// with a wrong region. Recording the AMBIENT region for a region-poly result is correct because
// that is the very region the callee allocated into (it threaded the caller's `__region_auto`).
func (a *Analyzer) recordStructLocalAllocRegion(sym *Symbol, bindingType Type, value ast.Expr, valueType Type) {
	if a == nil || sym == nil || value == nil || a.currentStructLocalAllocRegion == nil {
		return
	}
	region := a.activeContainerRegionName()
	if region == "" {
		return
	}
	st, ok := peelToStructType(bindingType)
	if !ok || !structHasRegionlessContainerField(st) {
		return
	}
	if _, isLit := stripParenExpr(value).(*ast.StructLitExpr); isLit {
		a.currentStructLocalAllocRegion[sym] = region
		return
	}
	// Region-poly builder call: result lives in the ambient region (same as a literal).
	if a.exprIsRegionPolyResultCarryingRegionData(value, valueType) {
		a.currentStructLocalAllocRegion[sym] = region
		return
	}
}

// invalidateStructLocalAllocRegionOnAssign drops the region recorded for a struct local when it
// is REASSIGNED. The record is pinned at declaration; a reassignment can point the local at a
// struct whose fields live in a different (possibly shorter-lived, now-dead) region, so the
// recorded region becomes stale. Re-recording from the new RHS is NOT sound here: there is no
// use-site region-liveness check, so a reassignment inside a `region inner:` scope followed by a
// use after `inner` dies must NOT thread `inner`. Clearing reverts the local to the conservative
// "cannot infer region parameter" rejection — sound at the cost of re-rejecting the (niche)
// reassign-then-thread pattern, which a future use-site liveness check could re-admit.
func (a *Analyzer) invalidateStructLocalAllocRegionOnAssign(target ast.Expr) {
	if a == nil || a.currentStructLocalAllocRegion == nil || a.currentScope == nil {
		return
	}
	ident, ok := target.(*ast.Ident)
	if !ok {
		return
	}
	if sym, ok := a.currentScope.Lookup(ident.Name); ok && sym != nil {
		delete(a.currentStructLocalAllocRegion, sym)
	}
}

// structLocalArgRegion returns the recorded alloc-region for an argument expression that is a
// bare identifier bound to a struct local whose container fields live in an ambient region.
func (a *Analyzer) structLocalArgRegion(arg ast.Expr) string {
	if a == nil || arg == nil || a.currentStructLocalAllocRegion == nil {
		return ""
	}
	// Accept `bag` and `&bag` / `(&bag)` reborrow forms.
	for {
		switch e := arg.(type) {
		case *ast.ParenExpr:
			arg = e.Inner
			continue
		case *ast.AddrOfExpr:
			arg = e.Operand
			continue
		}
		break
	}
	ident, ok := arg.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return ""
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return ""
	}
	return a.currentStructLocalAllocRegion[sym]
}

// attachStructLocalArgRegion stamps the recorded struct-local region onto a region-less struct-ref
// argument type so the call's region-param binding (collectRegionBinding via the RefType arm) can
// thread it into the callee. Returns argType unchanged unless the parameter is a region-param
// struct ref and the argument is a region-bearing struct local. The region lands on the RefType
// wrapper, not the StructType — preserving the struct-type-region invariant.
func (a *Analyzer) attachStructLocalArgRegion(arg ast.Expr, argType, paramType Type, regionParams map[string]bool) Type {
	if a == nil || len(regionParams) == 0 {
		return argType
	}
	pr, ok := paramType.(*RefType)
	if !ok || pr == nil || pr.Region == "" || !regionParams[pr.Region] {
		return argType
	}
	if _, ok := pr.Elem.(*StructType); !ok {
		return argType
	}
	region := a.structLocalArgRegion(arg)
	if region == "" {
		return argType
	}
	// The argument is a struct VALUE (auto-ref'd at the call) or already a region-less struct ref.
	// Produce a `Struct& @region` whose RefType carries the threadable region.
	switch at := argType.(type) {
	case *RefType:
		if at.Region != "" {
			return argType
		}
		cp := cloneRefType(at)
		cp.Region = region
		return cp
	case *StructType:
		return &RefType{Mutable: true, Region: region, Elem: at}
	}
	return argType
}
