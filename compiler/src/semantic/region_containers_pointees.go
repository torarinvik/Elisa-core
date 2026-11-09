package semantic

import (
	"elisacore/src/ast"
)

func collectImmediateRegionPointees(t Type, p *regionPointees, seen map[Type]bool) {
	if t == nil {
		p.wildcard = true
		return
	}
	switch tt := t.(type) {
	case *CStrType, *SViewType:
		p.keys["u8"] = true
	case *DArrayType:
		collectInlineRegionPointees(tt.Elem, p, map[Type]bool{})
	case *ViewType:
		collectInlineRegionPointees(tt.Elem, p, map[Type]bool{})
	case *SetType:
		collectInlineRegionPointees(tt.Elem, p, map[Type]bool{})
	case *DictType:
		collectInlineRegionPointees(tt.Key, p, map[Type]bool{})
		collectInlineRegionPointees(tt.Value, p, map[Type]bool{})
	case *RefType:
		collectInlineRegionPointees(tt.Elem, p, map[Type]bool{})
		collectImmediateRegionPointees(tt.Elem, p, seen)
	case *OptionalType:
		collectImmediateRegionPointees(tt.Value, p, seen)
	case *ErrorUnionType:
		collectImmediateRegionPointees(tt.Value, p, seen)
	case *EnumType:
		if tt.Packed {
			// A packed handle points at a store row, never into another value's inline slots.
			p.keys[tt.String()] = true
			return
		}
		collectRegionPointees(t, p, seen)
	default:
		collectRegionPointees(t, p, seen)
	}
}

// collectInlineRegionPointees adds t and every type stored inline in a value of t: the
// targets a pointer into storage that holds t values may have.
func collectInlineRegionPointees(t Type, p *regionPointees, seen map[Type]bool) {
	if t == nil {
		p.wildcard = true
		return
	}
	if seen[t] {
		return
	}
	seen[t] = true
	p.keys[t.String()] = true
	switch tt := t.(type) {
	case *StructType:
		if tt.Store {
			p.wildcard = true
			return
		}
		for _, field := range tt.Fields {
			collectInlineRegionPointees(field.Type, p, seen)
		}
	case *TupleType:
		for _, field := range tt.Fields {
			collectInlineRegionPointees(field.Type, p, seen)
		}
	case *ArrayType:
		collectInlineRegionPointees(tt.Elem, p, seen)
	case *OptionalType:
		collectInlineRegionPointees(tt.Value, p, seen)
	case *ErrorUnionType:
		collectInlineRegionPointees(tt.Value, p, seen)
	case *AggregateStateType:
		collectInlineRegionPointees(tt.Base, p, seen)
	case *EnumType:
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				collectInlineRegionPointees(payload, p, seen)
			}
		}
	case *BuiltinType, *BitIntType, *CStrType, *SViewType, *DArrayType, *ViewType, *DictType, *SetType, *RefType, *ConstEnumType, *IDType, *NullType, *NeverType, *ErrorSetType:
	default:
		p.wildcard = true
	}
}

func collectRegionPointees(t Type, p *regionPointees, seen map[Type]bool) {
	if t == nil || p.wildcard || seen[t] {
		return
	}
	seen[t] = true
	point := func(elem Type) {
		if elem == nil {
			p.wildcard = true
			return
		}
		p.keys[elem.String()] = true
		collectRegionPointees(elem, p, seen)
	}
	switch tt := t.(type) {
	case *BuiltinType, *BitIntType, *NullType, *NeverType, *IDType, *ConstEnumType, *ErrorSetType, *ConstValueType:
	case *CStrType, *SViewType:
		p.keys["u8"] = true
	case *DArrayType:
		point(tt.Elem)
	case *ViewType:
		point(tt.Elem)
	case *RefType:
		point(tt.Elem)
	case *ArrayType:
		collectRegionPointees(tt.Elem, p, seen)
	case *OptionalType:
		collectRegionPointees(tt.Value, p, seen)
	case *ErrorUnionType:
		collectRegionPointees(tt.Value, p, seen)
	case *DictType:
		point(tt.Key)
		point(tt.Value)
	case *SetType:
		point(tt.Elem)
	case *TupleType:
		for _, field := range tt.Fields {
			collectRegionPointees(field.Type, p, seen)
		}
	case *AggregateStateType:
		collectRegionPointees(tt.Base, p, seen)
	case *StructType:
		if tt.Store {
			p.wildcard = true
			return
		}
		for _, field := range tt.Fields {
			collectRegionPointees(field.Type, p, seen)
		}
	case *EnumType:
		if tt.Packed {
			// A packed handle points at a store row, never into another value's inline slots.
			p.keys[tt.String()] = true
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				collectRegionPointees(payload, p, seen)
			}
		}
	default:
		p.wildcard = true
	}
}

// callArgumentElementStateWithoutHeaderEscape returns tracked element provenance for a local
// darray argument when the destination cannot retain the source darray's backing storage. The
// element type may be any concrete aggregate: each element's state retains nested borrows, while
// the temporary source buffer is not itself stored by a callee that only copies its elements.
func (a *Analyzer) callArgumentElementStateWithoutHeaderEscape(arg ast.Expr, containerType Type) (regionRefState, bool) {
	ident, ok := stripAddrAndParens(arg).(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil || a.currentElementStates == nil {
		return regionRefState{}, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return regionRefState{}, false
	}
	sourceElem, ok := darrayElemType(sym.Type)
	if !ok || sourceElem == nil {
		return regionRefState{}, false
	}
	targetElem, ok := darrayElemType(containerType)
	if !ok || typeMayReachDarrayStorage(targetElem, sourceElem.String()) {
		return regionRefState{}, false
	}
	elems, tracked := a.currentElementStates[sym]
	if !tracked {
		return regionRefState{}, false
	}
	if inlineContainerElementValue(sourceElem) {
		// Element tracking also records where an inline aggregate's array slot
		// lives. Copying the aggregate by value does not retain that slot. Remove
		// only this root dependency: nested field states still retain real borrows
		// into the same region (including a view stored in an aggregate field).
		if sourceRegion := containerRegion(sym.Type); sourceRegion != "" {
			elems = withoutRootRegionDependency(elems, sourceRegion)
		}
	}
	return elems, true
}

func inlineContainerElementValue(t Type) bool {
	switch element := StripAggregateStateType(t).(type) {
	case *StructType:
		return element != nil && !element.Store
	case *EnumType:
		return element != nil && !element.Packed
	case *TupleType, *ArrayType, *OptionalType, *ErrorUnionType:
		return true
	default:
		return false
	}
}

func withoutRootRegionDependency(state regionRefState, name string) regionRefState {
	state = cloneRegionRefState(state)
	for region := range state.Deps {
		if region != nil && region.Name == name {
			delete(state.Deps, region)
		}
	}
	return state
}

// storeCallArgRegions: the live local regions a value stored out of arg (in mode) may depend on,
// for an argument whose stored parts may hold region references.
func (a *Analyzer) storeCallArgRegions(arg ast.Expr, mode storeMode) []string {
	regions := []string{}
	if state, ok := a.regionRefStateForExpr(arg); ok {
		regions = liveLocalRegionDependencyNames(state)
	}
	// storeViews: only view-only values are stored and the source holds no bytes, so its own
	// storage is never referenced -- what they point at is exactly what the tracked state
	// records, when that state is complete (storeViewsProvenanceComplete). Every dependency the
	// state has is still judged; only the storage-region proxy is dropped.
	ownStorageIrrelevant := mode == storeViews && a.storeViewsProvenanceComplete(arg)
	if mode == storeViews && !ownStorageIrrelevant {
		// A byte-free local: what its views point at is the union of what was written into it
		// (localViewProvenance), complete over the function body.
		if provenance, ok := a.localViewProvenance(arg); ok {
			regions = provenance
			ownStorageIrrelevant = true
		}
	}
	if own := a.containerRegionOf(stripRefForBounds(a.exprType(arg))); own != "" && a.structLocalRegionIsLive(own) && !ownStorageIrrelevant {
		regions = append(regions, own)
	}
	// A struct local's region-less container fields live in the region it was built in
	// (recorded at construction / growth); its type carries no region, so without this a grown
	// local struct passed by value looked region-free. Only an argument that carries that
	// backing (the struct or a region-less container in it) can retain it; a scalar/view element
	// read out of it carries its own tracked provenance.
	if own := a.structLocalArgRegion(arg); own != "" && a.structLocalRegionIsLive(own) && storedValuesCarryRegionlessBacking(a.exprType(arg), mode) && !ownStorageIrrelevant {
		regions = append(regions, own)
	}
	return regions
}

// containerViewsLiveIn: every view reachable through t's container elements is annotated with
// `target` or a program region, so storing those elements into `target` storage cannot dangle.
// A bare (region-less) view anywhere answers false.
func (a *Analyzer) containerViewsLiveIn(t Type, target string) bool {
	found := false
	var walk func(Type, int) bool
	walk = func(t Type, depth int) bool {
		if t == nil || depth > 8 {
			return false
		}
		switch tt := t.(type) {
		case *RefType:
			return walk(tt.Elem, depth+1)
		case *DArrayType:
			return walk(tt.Elem, depth+1)
		case *ArrayType:
			return walk(tt.Elem, depth+1)
		case *OptionalType:
			return walk(tt.Value, depth+1)
		case *SViewType:
			if tt.Region == "" || !(tt.Region == target || a.isProgramRegion(tt.Region)) {
				return false
			}
			found = true
			return true
		}
		return !a.typeCanContainRegionRefs(t, map[string]bool{})
	}
	return walk(t, 0) && found
}
