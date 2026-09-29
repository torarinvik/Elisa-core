package semantic

import (
	"elisacore/src/ast"
)

// ownedExtractionRegion returns the innermost tracked region whose storage an OWNED value was
// extracted from: `xs[0].items`, `b.items`, `xs.pop()`, or a loop binder over `xs`. A darray/dict/set
// element (or a struct field) that itself owns arena storage copies only its HEADER when read; the
// backing still lives wherever the root container or aggregate put it. Element and field types are
// never region-stamped, so without walking back to the root such a read looks region-free and
// `out.push(xs[0].items)` stored a pointer into a local arena that is freed at function exit.
// Only values whose type may own arena storage are considered (scalars and plain structs copy
// completely). "" when no tracked region is found.
func (a *Analyzer) ownedExtractionRegion(expr ast.Expr) string {
	if a == nil || expr == nil {
		return ""
	}
	t := a.exprTypes[stripParenExpr(expr)]
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	if t == nil || !a.typeMayOwnArenaStorage(t, map[string]bool{}) {
		return ""
	}
	return a.ownedStorageRootRegion(expr, map[ast.Expr]bool{})
}

// ownedStorageRootRegion walks field reads, index reads, element-returning container methods and
// loop binders down to the storage root, accumulating the innermost tracked region seen on the way.
func (a *Analyzer) ownedStorageRootRegion(expr ast.Expr, seen map[ast.Expr]bool) string {
	e := stripParenExpr(expr)
	if e == nil || seen[e] || len(seen) > 64 {
		return ""
	}
	seen[e] = true
	own := ""
	if r := containerRegion(a.exprTypes[e]); r != "" {
		if _, ok := a.regionLifetimeOrdinal(r); ok {
			own = r
		}
	}
	switch n := e.(type) {
	case *ast.MoveExpr:
		return a.innerRegion(own, a.ownedStorageRootRegion(n.Operand, seen))
	case *ast.AddrOfExpr:
		return a.innerRegion(own, a.ownedStorageRootRegion(n.Operand, seen))
	case *ast.CastExpr:
		return a.innerRegion(own, a.ownedStorageRootRegion(n.Operand, seen))
	case *ast.GetExpr:
		r := a.innerRegion(own, a.ownedStorageRootRegion(n.Value, seen))
		if n.Fallback != nil {
			r = a.innerRegion(r, a.ownedStorageRootRegion(n.Fallback, seen))
		}
		return r
	case *ast.UnwrapElseExpr:
		r := a.innerRegion(own, a.ownedStorageRootRegion(n.Value, seen))
		if n.Fallback != nil {
			r = a.innerRegion(r, a.ownedStorageRootRegion(n.Fallback, seen))
		}
		return r
	case *ast.Ident:
		r := a.innerRegion(own, a.structInteriorTaintRegion(n))
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok && sym != nil {
				if own == "" {
					// A synthesized alias-root ident has no recorded expression type.
					if region := containerRegion(sym.Type); region != "" {
						if _, tracked := a.regionLifetimeOrdinal(region); tracked {
							r = a.innerRegion(r, region)
						}
					}
				}
				if _, isRef := sym.Type.(*RefType); isRef {
					// A reference local reads through to the storage it aliases (`r: Box& = &b`).
					for _, root := range a.aliasRootsForExpr(n) {
						name := root
						if dot := indexOfByte(root, '.'); dot >= 0 {
							name = root[:dot]
						}
						if name != "" && name != n.Name {
							r = a.innerRegion(r, a.ownedStorageRootRegion(&ast.Ident{Position: n.Position, Name: name}, seen))
						}
					}
				}
				if value, bound := a.currentValueBindings[sym]; bound && value != nil {
					// The binding's initializer (`r: Box& = get d.get(1) else return`) names the
					// storage it copied or borrowed from.
					r = a.innerRegion(r, a.ownedStorageRootRegion(value, seen))
				}
				if state, tracked := a.iterBinderElementStates[sym]; tracked {
					r = a.innerRegion(r, a.innermostTrackedRegion(state))
				} else if src := a.iterBindingSources[sym]; src != nil {
					r = a.innerRegion(r, a.ownedStorageRootRegion(src, seen))
				}
			}
		}
		return r
	case *ast.FieldExpr:
		if !ownedStorageProjectable(a.exprTypes[stripParenExpr(n.Object)]) {
			return own
		}
		return a.innerRegion(own, a.ownedStorageRootRegion(n.Object, seen))
	case *ast.IndexExpr:
		if state, tracked := a.localContainerElementState(n); tracked {
			return a.innerRegion(own, a.innermostTrackedRegion(state))
		}
		if !ownedStorageProjectable(a.exprTypes[stripParenExpr(n.Object)]) {
			return own
		}
		return a.innerRegion(own, a.ownedStorageRootRegion(n.Object, seen))
	case *ast.CallExpr:
		field, ok := stripParenExpr(n.Func).(*ast.FieldExpr)
		if !ok || field == nil || field.Object == nil {
			return own
		}
		if !containerMethodYieldsElement(a.exprTypes[stripParenExpr(field.Object)], a.exprTypes[e]) {
			return own
		}
		return a.innerRegion(own, a.ownedStorageRootRegion(field.Object, seen))
	}
	return own
}

// ownedStorageProjectable reports whether a field/index read of an object of type t can be walked to
// its storage root: an owned aggregate/container, or a reference (whose alias roots the Ident case
// follows). A view's elements are borrowed bytes; that flow is the region-ref provenance's job.
func ownedStorageProjectable(t Type) bool {
	switch t.(type) {
	case nil, *ViewType, *SViewType, *CStrType, *PackedVariantViewType:
		return false
	}
	return true
}

// containerMethodYieldsElement reports whether a method on a darray/dict/set receiver returns one of
// the receiver's own elements (`pop`, `get`, `remove`, `first`, ...): the result type is the element
// (or dict value) type, possibly optional. Such a result shares the receiver's backing storage.
func containerMethodYieldsElement(receiver, result Type) bool {
	if result == nil {
		return false
	}
	if ref, ok := receiver.(*RefType); ok && ref != nil {
		receiver = ref.Elem
	}
	if opt, ok := result.(*OptionalType); ok && opt != nil {
		result = opt.Value
	}
	if ref, ok := result.(*RefType); ok && ref != nil {
		result = ref.Elem
	}
	var elems []Type
	switch rt := receiver.(type) {
	case *DArrayType:
		if rt != nil {
			elems = append(elems, rt.Elem)
		}
	case *DictType:
		if rt != nil {
			elems = append(elems, rt.Key, rt.Value)
		}
	case *SetType:
		if rt != nil {
			elems = append(elems, rt.Elem)
		}
	}
	want := result.String()
	for _, elem := range elems {
		if elem != nil && elem.String() == want {
			return true
		}
	}
	return false
}

// nestedFreshStorageRegion returns the region a fresh container NESTED inside another producer
// (`Box{items: [65, 66]}`, `[[1, 2]]`) is allocated in. The outer producer's own header is copied into
// its target, but the nested literal's backing is allocated where it is evaluated — its own stamped
// region, else the innermost active allocation region. Only fresh producers that may own arena
// storage count; "" otherwise.
func (a *Analyzer) nestedFreshStorageRegion(expr ast.Expr) string {
	if a == nil || expr == nil {
		return ""
	}
	e := stripParenExpr(expr)
	switch lit := e.(type) {
	case *ast.ListLitExpr:
		// An empty literal allocates nothing: growth later is tracked at the growing store.
		if len(lit.Elems) == 0 && len(lit.Spreads) == 0 && lit.Keys == nil {
			return ""
		}
	case *ast.ListComprehensionExpr:
	default:
		return ""
	}
	t := a.exprTypes[e]
	if r := containerRegion(t); r != "" {
		if _, ok := a.regionLifetimeOrdinal(r); ok {
			return r
		}
		return ""
	}
	if t == nil || !a.typeMayOwnArenaStorage(t, map[string]bool{}) {
		return ""
	}
	if r := a.activeContainerRegionName(); r != "" {
		if _, ok := a.regionLifetimeOrdinal(r); ok {
			return r
		}
	}
	return ""
}

// elementStorageState is the provenance a value carries INTO a container it is pushed into: its
// region-ref state plus the region its own backing storage lives in (a stamped local container,
// an interior-tainted aggregate, a nested fresh literal, an owned extraction). The element
// tracker merges these, so a tracked container's element provenance bounds every element's
// storage and can stand in for the container's own region. known is false when the
// storage region cannot be expressed as a dependency; the caller must then stop tracking.
func (a *Analyzer) elementStorageState(expr ast.Expr) (state regionRefState, ok bool, known bool) {
	state, ok = a.regionRefStateForExpr(expr)
	e := stripParenExpr(expr)
	region := a.innerRegion(a.valueStoreRegion(e, a.exprTypes[e]), a.nestedFreshStorageRegion(e))
	region = a.innerRegion(region, a.valueInteriorRegion(e))
	if region == "" {
		return state, ok, true
	}
	sym, regionState := a.lookupRegionState(region)
	if sym == nil || regionState.Destroyed {
		return regionRefState{}, false, false
	}
	dep := regionRefStateFromDependency(sym, regionState.Generation)
	if !ok {
		return dep, true, true
	}
	merged, _ := mergeRegionRefStates(state, dep)
	return merged, true, true
}

// innermostTrackedRegion is the shortest-lived tracked region a provenance state depends on
// (fields included); "" when it names none.
func (a *Analyzer) innermostTrackedRegion(state regionRefState) string {
	r := ""
	for _, name := range liveLocalRegionDependencyNames(state) {
		if _, ok := a.regionLifetimeOrdinal(name); ok {
			r = a.innerRegion(r, name)
		}
	}
	return r
}
