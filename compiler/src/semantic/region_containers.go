package semantic

import (
	"os"

	"elisacore/src/ast"
)

var debugRegionContainers = os.Getenv("ELISA_REGION_DEBUG") != ""

// Region-parameterized containers. See REGION_CONTAINERS_DESIGN.md. This stamps
// a container's allocation region from the enclosing `in <region>:` scope. The
// stamped Region is load-bearing: escape checks (return/store/nested-store),
// flow destruction-invalidation, region-param binding, and backend arena
// routing (regionArenaOwner) all consult it.

// stampContainerRegion returns t with its container region inferred from the
// active allocation scope, if it is a region-less darray and a region scope is
// active. Returns a copy (never mutates the input type) so shared/annotation
// types are not polluted.
func (a *Analyzer) stampContainerRegion(t Type) Type {
	if a == nil || t == nil {
		return t
	}
	region := a.activeContainerRegionName()
	if region == "" {
		return t
	}
	switch c := t.(type) {
	case *DArrayType:
		if c != nil && c.Region == "" {
			cp := *c
			cp.Region = region
			if debugRegionContainers {
				println("REGIONSTAMP darray ->", region)
			}
			return &cp
		}
	case *DictType:
		if c != nil && c.Region == "" {
			cp := *c
			cp.Region = region
			return &cp
		}
	case *SetType:
		if c != nil && c.Region == "" {
			cp := *c
			cp.Region = region
			return &cp
		}
	}
	return t
}

// (semanticContainerDArray removed — superseded by containerRegion.)

// containerRegion peels ref wrappers and returns the allocation region of the
// underlying container (darray or dict), or "" if t is not a region-carrying
// container.
// containerRegionOf is containerRegion, memoized per type once declaration shapes are final
// (the struct/tuple case walks the whole by-value type graph).
func (a *Analyzer) containerRegionOf(t Type) string {
	if !a.typeShapesFrozen || t == nil {
		return containerRegion(t)
	}
	if region, ok := a.containerRegionMemo[t]; ok {
		return region
	}
	if a.noStorageOverlay != nil {
		if region, ok := a.noStorageOverlay.containerRegions[t]; ok {
			return region
		}
	}
	region := containerRegion(t)
	if a.containerRegionMemo == nil {
		a.containerRegionMemo = map[Type]string{}
	}
	a.containerRegionMemo[t] = region
	return region
}

func containerRegion(t Type) string {
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

type explicitTypeRegionSummary struct {
	region  string
	unknown bool
}

func commonExplicitTypeRegion(typ Type) string {
	summary := explicitTypeRegionSummary{}
	collectExplicitTypeRegions(typ, &summary, map[Type]bool{}, map[Type]uint8{}, map[Type]bool{})
	if summary.unknown {
		return ""
	}
	return summary.region
}

func collectExplicitTypeRegions(typ Type, summary *explicitTypeRegionSummary, seen map[Type]bool, infoMemo map[Type]uint8, infoActive map[Type]bool) {
	if typ == nil || summary == nil || summary.unknown || seen[typ] {
		return
	}
	seen[typ] = true
	addRegion := func(region string) {
		if region == "" {
			summary.unknown = true
			return
		}
		if summary.region == "" {
			summary.region = region
		} else if summary.region != region {
			summary.unknown = true
		}
	}
	visit := func(child Type) { collectExplicitTypeRegions(child, summary, seen, infoMemo, infoActive) }
	switch value := typ.(type) {
	case *DArrayType:
		addRegion(value.Region)
		visit(value.Elem)
	case *DictType:
		addRegion(value.Region)
		visit(value.Key)
		visit(value.Value)
	case *SetType:
		addRegion(value.Region)
		visit(value.Elem)
	case *CStrType:
		addRegion(value.Region)
	case *SViewType:
		addRegion(value.Region)
	case *ViewType:
		addRegion(value.Region)
		visit(value.Elem)
	case *RefType:
		if value.Region != "" {
			addRegion(value.Region)
			visit(value.Elem)
			return
		}
		// Determine whether this pointee type carries region information on its own.
		// Aggregate-set cardinality cannot answer that: another field may already have
		// contributed the same region. The memoized type-local predicate keeps this
		// independent of sibling order without rescanning the full subtree per ref.
		pointeeCarriesRegionInfo, _ := explicitTypeCarriesRegionInfo(value.Elem, infoMemo, infoActive)
		if !pointeeCarriesRegionInfo {
			addRegion("")
		}
		visit(value.Elem)
	case *GenericInstanceType:
		if value.Region != "" {
			addRegion(value.Region)
		}
		for _, argument := range value.Args {
			visit(argument)
		}
	case *TupleType:
		for _, field := range value.Fields {
			visit(field.Type)
		}
	case *OptionalType:
		visit(value.Value)
	case *ErrorUnionType:
		visit(value.Value)
	case *ArrayType:
		visit(value.Elem)
	case *AggregateStateType:
		visit(value.Base)
	case *StructType:
		for _, field := range value.Fields {
			visit(field.Type)
		}
	}
}

// explicitTypeCarriesRegionInfo reports whether collecting this type can add a
// concrete region or uncertainty. An empty-region RefType contributes uncertainty
// only when its pointee is regionless and acyclic; a pointee cycle must leave that
// decision to the outer collector. State 1 memoizes a regionless acyclic type and
// state 2 memoizes a positive one; cycle-dependent negative results are left uncached.
func explicitTypeCarriesRegionInfo(typ Type, memo map[Type]uint8, active map[Type]bool) (bool, bool) {
	if typ == nil {
		return false, false
	}
	if state, ok := memo[typ]; ok {
		return state == 2, false
	}
	if active[typ] {
		return false, true
	}
	active[typ] = true
	defer delete(active, typ)

	var carries bool
	var cycle bool
	visit := func(child Type) {
		childCarries, childCycle := explicitTypeCarriesRegionInfo(child, memo, active)
		carries = carries || childCarries
		cycle = cycle || childCycle
	}
	switch value := typ.(type) {
	case *DArrayType:
		carries = value != nil
	case *DictType:
		carries = value != nil
	case *SetType:
		carries = value != nil
	case *CStrType:
		carries = value != nil
	case *SViewType:
		carries = value != nil
	case *ViewType:
		carries = value != nil
	case *RefType:
		if value != nil {
			if value.Region != "" {
				carries = true
			} else {
				pointeeCarries, pointeeCycle := explicitTypeCarriesRegionInfo(value.Elem, memo, active)
				carries = pointeeCarries || !pointeeCycle
				cycle = pointeeCycle
			}
		}
	case *GenericInstanceType:
		if value != nil {
			carries = value.Region != ""
			if !carries {
				for _, argument := range value.Args {
					visit(argument)
					if carries {
						break
					}
				}
			}
		}
	case *TupleType:
		if value != nil {
			for _, field := range value.Fields {
				visit(field.Type)
				if carries {
					break
				}
			}
		}
	case *OptionalType:
		if value != nil {
			visit(value.Value)
		}
	case *ErrorUnionType:
		if value != nil {
			visit(value.Value)
		}
	case *ArrayType:
		if value != nil {
			visit(value.Elem)
		}
	case *AggregateStateType:
		if value != nil {
			visit(value.Base)
		}
	case *StructType:
		if value != nil {
			for _, field := range value.Fields {
				visit(field.Type)
				if carries {
					break
				}
			}
		}
	}
	if carries {
		memo[typ] = 2
	} else if !cycle {
		memo[typ] = 1
	}
	return carries, cycle
}

// checkRegionParamReturnEscape rejects returning a value tied to a function REGION PARAMETER's
// region (`@r`) when the declared return type does not carry that same region. A region param's
// lifetime is the caller's argument; returning a borrow/value of it with a region-LESS type loses the
// tie, so a caller could store the result past the region's death — a use-after-free. The sound form
// is `-> T @r` (verified: the escape checker catches a caller that then stores it past the region,
// and accepts use within the region's lifetime). This is the borrow-out coverage that makes S4
// (struct-field container growth on a `Mod& @r` param) safe — docs/91 S4. Conservative: it only
// fires for a region that is a function region PARAM (caller-provided lifetime); scope-owned region
// returns are handled by checkReturnRegionContainerEscape.
func (a *Analyzer) checkRegionParamReturnEscape(valueExpr ast.Expr, valueType Type) {
	if a == nil || valueExpr == nil {
		return
	}
	if a.checkUnannotatedSViewParamReturn(valueExpr) {
		return
	}
	a.checkDeclaredAggregateReturnParamRegions(valueExpr, valueType)
	// The returned value's region: its own type's region if it carries one (a returned container /
	// view), else the region it BORROWS from — peeling `&`/index/slice/field down to the underlying
	// region-carrying base (catches `return &m.bits[0]`, a ref INTO the @r field, whose `u8&` type
	// carries no region itself).
	region := regionParamReturnTypeRegion(valueType)
	if region == "" && (isBorrowLikeType(valueType) || isRegionBorrowedStringOrViewType(valueType) || isRegionBorrowedAggregateType(valueType)) {
		region = a.returnBorrowedRegion(valueExpr)
	}
	if region == "" || !a.lookupRegionParam(region) {
		return
	}
	declaredReturnRegion := regionParamReturnTypeRegion(a.currentReturn)
	if a.currentFuncType != nil && a.currentFuncType.ReturnRegion != "" {
		declaredReturnRegion = a.currentFuncType.ReturnRegion
	}
	if declaredReturnRegion == region {
		return // the return type carries the same @r — sound, and checked at the call site
	}
	a.errorf(valueExpr.Pos(), "value tied to region parameter %q cannot be returned with a region-less type (its lifetime is the caller's %q region); annotate the return type with `@%s` (e.g. `-> view[u8] @%s`) so the borrow stays tied to the caller's region", region, region, region, region)
}

// checkDeclaredAggregateReturnParamRegions checks interior provenance as well as
// the nominal result type: a struct literal may have been contextualized as
// Packet[r] even though a field still borrows an independent input region s.
func (a *Analyzer) checkDeclaredAggregateReturnParamRegions(value ast.Expr, valueType Type) {
	if a.currentFuncType == nil || a.currentFuncType.ReturnRegion == "" || !typeCarriesRegionStorage(valueType) {
		return
	}
	target := a.currentFuncType.ReturnRegion
	state, known := a.regionRefStateForExpr(value)
	if !known {
		return
	}
	checked := map[string]bool{}
	check := func(source string) {
		if source == "" || checked[source] || !a.isSourceDeclaredRegionParam(source) {
			return
		}
		checked[source] = true
		if a.regionStoreEscapes(target, source) {
			a.errorf(value.Pos(), "return contract @%s cannot carry storage tied to independent region parameter %q", target, source)
		}
	}
	var visit func(regionRefState)
	visit = func(state regionRefState) {
		param := func(index int) {
			if index >= 0 && index < len(a.currentFuncType.Params) {
				check(regionParamReturnTypeRegion(a.currentFuncType.Params[index]))
			}
		}
		if state.HasDirectParamDep {
			param(state.DirectParamDep)
		}
		state.ParamDeps.ForEach(param)
		for owner := range state.Deps {
			if owner != nil {
				check(owner.Name)
			}
		}
		for _, field := range state.Fields {
			visit(field)
		}
	}
	visit(state)
}

// checkUnannotatedSViewParamReturn rejects explicitly region-bound sview parameters when a
// direct return erases that declared region. Unannotated sview forwarding is permitted here:
// the region escape analysis records call-result provenance at the use site, and existing
// standard-library/compiler APIs depend on forwarding unannotated descriptors.
func (a *Analyzer) checkUnannotatedSViewParamReturn(valueExpr ast.Expr) bool {
	if a == nil || a.currentFuncDecl == nil || a.currentFuncType == nil || a.currentReturn == nil {
		return false
	}
	returned, ok := stripParenExpr(valueExpr).(*ast.Ident)
	if !ok || returned == nil {
		return false
	}
	returnType := StripAggregateStateType(a.currentReturn)
	if _, ok := returnType.(*SViewType); !ok {
		return false
	}
	for index, parameter := range a.currentFuncDecl.Params {
		if parameter.Name != returned.Name || index >= len(a.currentFuncType.Params) {
			continue
		}
		parameterType := StripAggregateStateType(a.currentFuncType.Params[index])
		viewParameter, ok := parameterType.(*SViewType)
		if !ok || viewParameter == nil {
			return false
		}
		parameterRegion := explicitTypeRegion(parameter.Type)
		if parameterRegion == "" || a.currentFuncType.ReturnRegion == parameterRegion {
			return false
		}
		a.errorf(valueExpr.Pos(), "sview parameter %q is tied to region %q but the return type does not carry that region; annotate the return type with `@%s`", returned.Name, parameterRegion, parameterRegion)
		return true
	}
	return false
}

func explicitTypeRegion(typ ast.TypeExpr) string {
	switch t := typ.(type) {
	case *ast.NamedType:
		return t.Region
	case *ast.BuiltinTypeExpr:
		return t.Region
	case *ast.GenericType:
		return t.Region
	case *ast.RefType:
		return t.Region
	case *ast.MutableType:
		return explicitTypeRegion(t.Elem)
	case *ast.TupleTypeExpr:
		return t.Region
	}
	return ""
}

// regionParamReturnTypeRegion returns a type's region for the return-escape check: a reference's own
// `@r` (RefType.Region, e.g. `u8& @r`) or a container/view's stamped region. Used for both the
// returned value's type and the declared return type so `-> u8& @r` / `-> view[u8] @r` are recognized.
func regionParamReturnTypeRegion(t Type) string {
	if rt, ok := stripRefForBounds(t).(*RefType); ok && rt != nil && rt.Region != "" {
		return rt.Region
	}
	if rt, ok := t.(*RefType); ok && rt != nil && rt.Region != "" {
		return rt.Region
	}
	if region := containerOrEntryRegion(t); region != "" {
		return region
	}
	return commonExplicitTypeRegion(t)
}

// returnBorrowedRegion finds the region a returned BORROW points into, peeling address-of, indexing,
// slicing, parens/casts, and field access down to the underlying region-carrying base. "" if none.
func (a *Analyzer) returnBorrowedRegion(e ast.Expr) string {
	switch n := stripParenExpr(e).(type) {
	case *ast.AddrOfExpr:
		return a.returnBorrowedRegion(n.Operand)
	case *ast.IndexExpr:
		return a.returnBorrowedRegion(n.Object)
	case *ast.SliceExpr:
		return a.returnBorrowedRegion(n.Object)
	case *ast.CastExpr:
		return a.returnBorrowedRegion(n.Operand)
	case *ast.FieldExpr:
		if r := containerOrEntryRegion(a.exprType(n)); r != "" {
			return r
		}
		return a.returnBorrowedRegion(n.Object)
	case *ast.TupleExpr:
		region := ""
		for _, element := range n.Elems {
			elementRegion := a.returnBorrowedRegion(element)
			region = a.innerRegion(region, elementRegion)
		}
		return region
	case *ast.Ident:
		// An identifier can name either an owning container or a borrowed view. Only
		// views/refs contribute a borrow lifetime here; owning container escape checks
		// use their separate region rules.
		if isRegionBorrowedStringOrViewType(a.exprType(n)) || isRegionBorrowedAggregateType(a.exprType(n)) {
			if region := regionParamReturnTypeRegion(a.exprType(n)); region != "" {
				return region
			}
		}
		if region := a.fieldObjectRegion(a.exprType(n)); region != "" {
			return region
		}
		return ""
	}
	return regionParamReturnTypeRegion(a.exprType(e))
}

// checkReturnRegionContainerEscape rejects returning a container allocated in a
// *scope-owned* region (`region NAME(...):`), which would dangle once the region
// is freed at scope exit. Borrowed arenas (`in <arena>:`, region == arena var,
// not a tracked region) and caller-provided region params are fine: those
// outlive the call. This is the bucket-granular analogue of Rust's
// "reference does not outlive its referent".
func (a *Analyzer) checkReturnRegionContainerEscape(valueExpr ast.Expr, valueType Type) {
	a.checkRegionContainerEscape(valueExpr, valueType, "return")
}
