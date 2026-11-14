package semantic

import (
	"fmt"
	"os"
	"sort"

	"elisacore/src/ast"
)

// checkCallArgumentStructTargetStoreEscape: a writable STRUCT reference argument (`stash(h, xs)`
// with `def stash(h: mutable Holder&, xs: darray[i64]): h.xs <- xs`) may receive another
// argument's region storage -- a by-value darray HEADER whose backing lives in a local region,
// or anything reachable from it. If that region dies before the struct's storage, the struct is
// left holding a dangling backing (a silent use-after-free). The struct's storage region is the
// region recorded for the argument (struct local / region-carrying ref); a region-less target
// (a parameter, a global, a caller-owned struct) outlives every local region.
func (a *Analyzer) checkCallArgumentStructTargetStoreEscape(call *ast.CallExpr, args []ast.Expr, index int) bool {
	rejected := false
	targetArg := args[index]
	var storeDecl *ast.FuncDecl
	calleeChecked := false
	targetRegion := a.structLocalArgRegion(targetArg)
	if targetRegion == "" {
		if state, ok := a.regionRefStateForExpr(targetArg); ok {
			if names := liveLocalRegionDependencyNames(state); len(names) == 1 {
				targetRegion = names[0]
			}
		}
	}
	for other, arg := range args {
		if other == index || !typeCarriesRegionStorage(a.exprType(arg)) {
			continue
		}
		if a.storeCallRegionsSafeForTarget(arg, targetRegion) {
			continue
		}
		if a.callArgNeverReaches(call, other, index) {
			continue
		}
		// The store-flow summary is undirected between written params; refine it with the
		// directed scan when the callee's body is visible.
		mode := storeHeader
		if !calleeChecked {
			calleeChecked = true
			if decl, ok := a.resolveReadOnlyScanCallee(call); ok && decl != nil && storeFlowCallFitsParams(decl, call) && !funcHasArenaParam(decl) {
				storeDecl = decl
			}
		}
		if storeDecl != nil {
			mode = a.calleeStoreModeInto(storeDecl, other, index)
			// The summary's edge kind bounds it too: an Elements edge stores only element
			// values copied out of the argument, never its storage.
			if mode == storeHeader {
				if sum := a.storeFlowKindsFor(storeDecl); sum != nil && a.storeFlowReachKind(sum, other, index) == storeFlowEdgeElements {
					mode = storeElements
				}
			}
		}
		if mode == storeNone {
			continue
		}
		regions := []string{}
		elementLike := mode == storeElements || mode == storeViews
		if elemState, tracked := a.trackedElementStateOfLocal(arg); elementLike && tracked {
			// Only element values read out of the argument can be stored: the argument's own
			// backing region is not retained, only what its elements depend on.
			regions = liveLocalRegionDependencyNames(elemState)
		} else if elementLike && !a.storedPartsMayHoldRegionRefs(stripRefForBounds(a.exprType(arg))) {
			// Values read out of it that cannot hold a region reference (scalars, packed handles)
			// carry none.
			continue
		} else if mode == storeElements && len(a.programRegions) != 0 && a.darrayElemsProgramLived(a.exprType(arg)) {
			// docs/128: only element values are stored, and every element is program-lived.
			continue
		} else if mode == storeViews && len(a.programRegions) != 0 && a.typeViewsProgramLived(stripRefForBounds(a.exprType(arg)), map[Type]bool{}) {
			// docs/128: view-only values read out of it can point only where their views do.
			// Prefer the directed store summary's exact source projections; only fall back to
			// the whole argument type when that proves every view program-lived.
			continue
		} else if elementLike && storeDecl != nil && len(a.programRegions) != 0 && a.calleeStoresProgramLivedElements(storeDecl, other, index, map[storeScanKey]bool{}) {
			// Run the directed payload proof only after the cheaper provenance and
			// whole-type checks need it. Every exact stored payload is region-free
			// or program-lived; the source aggregate's backing is not stored.
			continue
		} else {
			regions = a.storeCallArgRegions(arg, mode)
		}
		for _, region := range regions {
			if region == targetRegion || (targetRegion == "" && a.lookupRegionParam(region)) {
				continue
			}
			if a.isCompilerGeneratedAutoRegion(region) && a.autoRegionAdoptsCallerArena(targetRegion) {
				continue
			}
			if targetRegion == "" || a.regionStoreEscapes(targetRegion, region) {
				if storeScanTrace {
					_, tracked := a.trackedElementStateOfLocal(arg)
					fmt.Fprintf(os.Stderr, "STORECALL at %s mode=%d tracked=%v nobytes=%v argType=%s region=%s\n", arg.Pos(), mode, tracked, typeHoldsNoBytes(stripRefForBounds(a.exprType(arg)), map[Type]bool{}), typeString(a.exprType(arg)), region)
				}
				a.errorf(arg.Pos(), "call may store a value in region %q into longer-lived storage through argument %d; region %q is freed first, leaving a dangling reference. Copy it into the target's region (or a region that outlives it) before the call", a.regionDisplayName(region), index+1, a.regionDisplayName(region))
				rejected = true
				break
			}
		}
	}
	return rejected
}

// Complete view provenance means a view-only store cannot add regions beyond
// the tracked state. If even the union of header and element provenance is
// safe for the target, the directed callee scan cannot produce a diagnostic.
func (a *Analyzer) storeCallRegionsSafeForTarget(arg ast.Expr, targetRegion string) bool {
	if !a.storeViewsProvenanceComplete(arg) {
		return false
	}
	safe := func(regions []string) bool {
		for _, region := range regions {
			if region == targetRegion || (targetRegion == "" && a.lookupRegionParam(region)) {
				continue
			}
			if a.isCompilerGeneratedAutoRegion(region) && a.autoRegionAdoptsCallerArena(targetRegion) {
				continue
			}
			if targetRegion == "" || a.regionStoreEscapes(targetRegion, region) {
				return false
			}
		}
		return true
	}
	// The two store modes share the tracked state and container backing. Read
	// them once rather than recomputing provenance and type for each mode.
	if state, ok := a.regionRefStateForExpr(arg); ok && !safe(liveLocalRegionDependencyNames(state)) {
		return false
	}
	typ := a.exprType(arg)
	if own := a.containerRegionOf(stripRefForBounds(typ)); own != "" && a.structLocalRegionIsLive(own) && !safe([]string{own}) {
		return false
	}
	if own := a.structLocalArgRegion(arg); own != "" && a.structLocalRegionIsLive(own) && (storedValuesCarryRegionlessBacking(typ, storeHeader) || storedValuesCarryRegionlessBacking(typ, storeElements)) && !safe([]string{own}) {
		return false
	}
	if elems, tracked := a.trackedElementStateOfLocal(arg); tracked && !safe(liveLocalRegionDependencyNames(elems)) {
		return false
	}
	return true
}

// destinationPlaceRegion returns the caller-owned region a store target place lives in, when the
// place is rooted at a STRUCT reference param p whose ref carries region R (`p`, `p.f`, `p.f[i]`
// -- the struct's field containers live in R by field-region propagation) and R is one of the
// current function's region params (a value-level Arena symbol the hidden region argument can
// name). Container params are excluded: their arena's stack layout assumes no interleaved foreign
// allocations (collectAmbientForwardedContainers). "" otherwise.
func (a *Analyzer) destinationPlaceRegion(target ast.Expr, targetType Type) string {
	if a == nil || a.currentFuncDecl == nil || target == nil || a.currentScope == nil {
		return ""
	}
	root := rootIdentExpr(target)
	if root == nil {
		return ""
	}
	sym, ok := a.currentScope.Lookup(root.Name)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return ""
	}
	if _, isStruct := peelToStructType(sym.Type); !isStruct {
		return ""
	}
	region := a.fieldObjectRegion(sym.Type)
	if region == "" || !a.lookupRegionParam(region) {
		return ""
	}
	for _, rp := range a.currentFuncDecl.RegionParams {
		if rp == region {
			return region
		}
	}
	return ""
}

// destinationAllocatedInto reports whether value is a region-polymorphic call whose hidden
// allocation region was bound to region by the destination rule.
func (a *Analyzer) destinationAllocatedInto(value ast.Expr, region string) bool {
	if a == nil || region == "" || a.destinationAllocated == nil {
		return false
	}
	call, ok := storeScanUnwrap(value).(*ast.CallExpr)
	return ok && call != nil && a.destinationAllocated[call] == region
}

// argCarriesRegionlessBacking reports whether a value of type t (refs peeled) holds an OWNING
// region-less container's backing -- a darray/dict/set with no region of its own -- directly or
// through struct fields, inline enum payloads, tuples, optionals and fixed arrays. Views
// (sview/cstr) own no backing; their provenance is tracked separately.
func argCarriesRegionlessBacking(t Type) bool {
	return ownsRegionlessContainerRec(t, map[Type]bool{})
}

func ownsRegionlessContainerRec(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *DArrayType, *DictType, *SetType:
		return containerRegion(t) == ""
	case *RefType:
		return tt != nil && ownsRegionlessContainerRec(tt.Elem, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if ownsRegionlessContainerRec(field.Type, seen) {
				return true
			}
		}
	case *OptionalType:
		return ownsRegionlessContainerRec(tt.Value, seen)
	case *ErrorUnionType:
		return ownsRegionlessContainerRec(tt.Value, seen)
	case *ArrayType:
		return ownsRegionlessContainerRec(tt.Elem, seen)
	case *GenericInstanceType:
		for _, argument := range tt.Args {
			if ownsRegionlessContainerRec(argument, seen) {
				return true
			}
		}
		return ownsRegionlessContainerRec(tt.Base, seen)
	case *AggregateStateType:
		return tt != nil && ownsRegionlessContainerRec(tt.Base, seen)
	case *StructType:
		if tt == nil {
			return false
		}
		if tt.Store {
			return true
		}
		for _, f := range tt.Fields {
			if ownsRegionlessContainerRec(f.Type, seen) {
				return true
			}
		}
	case *EnumType:
		if tt == nil || tt.Packed || tt.Root().Packed {
			return false
		}
		for _, v := range tt.Variants {
			for _, payload := range v.Payload {
				if ownsRegionlessContainerRec(payload, seen) {
					return true
				}
			}
		}
	}
	return false
}

// storeViewsProvenanceComplete: the argument's tracked region-ref state is a complete account of
// where the views inside it point -- a parameter (its views come from the caller; storing a local
// view into it is rejected where that store happens), or an immutable, non-reference local (its
// state is recorded once, at its declaration, and nothing can change the value afterwards).
// Mutable locals are not complete until their field writes are tracked.
func (a *Analyzer) storeViewsProvenanceComplete(arg ast.Expr) bool {
	ident, ok := stripAddrAndParens(arg).(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(ident.Name)
	if !found || sym == nil {
		return false
	}
	switch sym.Kind {
	case SymbolParam:
		return true
	case SymbolLocal:
		if sym.Mutable {
			return false
		}
		if _, isRef := sym.Type.(*RefType); isRef {
			return false
		}
		decl, isDecl := sym.Node.(*ast.VarDeclStmt)
		if !isDecl || decl == nil || decl.Name != sym.Name || decl.Value == nil {
			return false
		}
		if _, mutableType := decl.Type.(*ast.MutableType); mutableType {
			return false
		}
		// The recorded state is derived from the initializer; a mutable local's state may lack
		// views pushed into its fields (not yet tracked), so an initializer reading one is not
		// a complete account.
		if storeScanMentions(decl.Value, a.currentFunctionMutableLocals()) {
			return false
		}
		_, recorded := a.regionRefStateForExpr(ident)
		return recorded
	}
	return false
}

// currentFunctionMutableLocals: names declared mutable (binding or type) anywhere in the current
// function body, memoized per function.
func (a *Analyzer) currentFunctionMutableLocals() map[string]bool {
	fn := a.currentFuncDecl
	if fn == nil {
		return map[string]bool{}
	}
	if cached, ok := a.mutableLocalsCache[fn]; ok {
		return cached
	}
	out := map[string]bool{}
	walkStoreScanNodes(fn.Body, func(node any) {
		v, ok := node.(*ast.VarDeclStmt)
		if !ok || v == nil {
			return
		}
		if _, mutableType := v.Type.(*ast.MutableType); v.Mutable || mutableType {
			out[v.Name] = true
		}
	})
	if a.mutableLocalsCache == nil {
		a.mutableLocalsCache = map[*ast.FuncDecl]map[string]bool{}
	}
	a.mutableLocalsCache[fn] = out
	return out
}

// storedValuesCarryRegionlessBacking: under storeHeader the argument value itself is retained, so
// its own backing matters; under storeElements only values read out of it are retained -- a
// struct's field values (whose container headers point into the struct's backing region) or a
// darray's elements (which carry backing only when they are themselves region-less containers).
func storedValuesCarryRegionlessBacking(t Type, mode storeMode) bool {
	if mode == storeHeader {
		return argCarriesRegionlessBacking(t)
	}
	t = stripRefForBounds(t)
	if elem, ok := darrayElemType(t); ok {
		return argCarriesRegionlessBacking(elem)
	}
	return argCarriesRegionlessBacking(t)
}

// trackedElementStateOfLocal returns the tracked element provenance of a local darray argument
// (`xs`, `&xs`), when element tracking models it.
func (a *Analyzer) trackedElementStateOfLocal(arg ast.Expr) (regionRefState, bool) {
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
	elems, tracked := a.currentElementStates[sym]
	if !tracked {
		return regionRefState{}, false
	}
	if inlineContainerElementValue(sourceElem) {
		if sourceRegion := containerRegion(sym.Type); sourceRegion != "" {
			elems = withoutRootRegionDependency(elems, sourceRegion)
		}
	}
	return elems, true
}

// liveLocalRegionDependencyNames lists every live region a region-ref state depends on, including
// through aggregate fields, in a deterministic order.
func liveLocalRegionDependencyNames(state regionRefState) []string {
	if len(state.Deps) == 0 && len(state.Fields) == 0 {
		return []string{}
	}
	seen := map[string]bool{}
	var collect func(regionRefState)
	collect = func(s regionRefState) {
		for region, dep := range s.Deps {
			if region != nil && dep.Valid {
				seen[region.Name] = true
			}
		}
		for _, field := range s.Fields {
			collect(field)
		}
	}
	collect(state)
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// containerMayHoldRegionRefs reports whether a container's elements (dict keys or values) can
// hold a region reference.
// storedPartsMayHoldRegionRefs: can a value READ OUT OF an argument of type t (an element of a
// container; a field or payload of anything else) hold a region reference? For a container this
// is containerMayHoldRegionRefs (its element types); for any other type -- a struct, tuple or
// enum whose fields are what gets read out -- it is whether the type itself can carry region
// storage or references. (Asking containerMayHoldRegionRefs of a struct answered false, so an
// element-mode store out of a struct argument was never checked.)
func (a *Analyzer) storedPartsMayHoldRegionRefs(t Type) bool {
	switch t.(type) {
	case *DArrayType, *DictType, *SetType:
		return a.containerMayHoldRegionRefs(t)
	case *SViewType, *CStrType:
		return false // what is read out of a view is its bytes
	}
	if t == nil || containsTypeParam(t) {
		return true
	}
	return typeCarriesRegionStorage(t) || a.typeCanContainRegionRefs(t, map[string]bool{})
}

func (a *Analyzer) containerMayHoldRegionRefs(t Type) bool {
	can := func(elem Type) bool { return elem != nil && a.typeCanContainRegionRefs(elem, map[string]bool{}) }
	switch tt := t.(type) {
	case *DArrayType:
		return can(tt.Elem)
	case *DictType:
		return can(tt.Key) || can(tt.Value)
	case *SetType:
		return can(tt.Elem)
	}
	return false
}

// regionPointees is the set of types (keyed by spelling) a value may point at through region
// storage; wildcard means any type (a type parameter, closure or opaque storage).
type regionPointees struct {
	keys     map[string]bool
	wildcard bool
}

// The two queries include different roots: an argument includes its backing slots,
// while a container query includes only what its stored elements can point at.
type regionPointeesMemoKey struct {
	typ               Type
	containerElements bool
}

func (a *Analyzer) containerElementPointeesOf(t Type) regionPointees {
	return a.memoRegionPointees(t, true)
}

func (a *Analyzer) argumentStorePointeesOf(t Type) regionPointees {
	return a.memoRegionPointees(t, false)
}

func (a *Analyzer) memoRegionPointees(t Type, containerElements bool) regionPointees {
	key := regionPointeesMemoKey{typ: t, containerElements: containerElements}
	frozen := a != nil && a.typeShapesFrozen && t != nil
	if frozen {
		if result, ok := a.regionPointeesMemo[key]; ok {
			return result
		}
	}
	var result regionPointees
	if containerElements {
		result = containerElementPointees(t)
	} else {
		result = argumentStorePointees(t)
	}
	if frozen {
		if a.regionPointeesMemo == nil {
			a.regionPointeesMemo = make(map[regionPointeesMemoKey]regionPointees)
		}
		// Callers only inspect/intersect the completed set; no traversal appends to it.
		a.regionPointeesMemo[key] = result
	}
	return result
}

func (p regionPointees) intersects(other regionPointees) bool {
	if len(p.keys) == 0 && !p.wildcard || len(other.keys) == 0 && !other.wildcard {
		return false
	}
	if p.wildcard || other.wildcard {
		return true
	}
	for key := range p.keys {
		if other.keys[key] {
			return true
		}
	}
	return false
}

// containerElementPointees lists what a container's stored elements may point at; the
// elements themselves live in the container's own region, so they are not included.
func containerElementPointees(t Type) regionPointees {
	p := regionPointees{keys: map[string]bool{}}
	switch tt := t.(type) {
	case *DArrayType:
		collectRegionPointees(tt.Elem, &p, map[Type]bool{})
	case *DictType:
		collectRegionPointees(tt.Key, &p, map[Type]bool{})
		collectRegionPointees(tt.Value, &p, map[Type]bool{})
	case *SetType:
		collectRegionPointees(tt.Elem, &p, map[Type]bool{})
	default:
		p.wildcard = true
	}
	return p
}

// argumentRegionPointees lists what a pointer into an argument's own region may point at. A
// container, view or reference argument's region holds its elements (and their inline parts),
// not what those elements point at in turn: a local `darray[Pattern]` of packed handles yields
// only `Pattern` slots. An aggregate's region state is the union of its fields', so it keeps
// the full transitive set.
func argumentRegionPointees(t Type) regionPointees {
	p := regionPointees{keys: map[string]bool{}}
	collectImmediateRegionPointees(t, &p, map[Type]bool{})
	return p
}

// argumentStorePointees includes references carried by values reachable from an argument, not
// only references into the argument's own backing region. For example, a darray[Record] whose
// records contain sviews can supply the viewed bytes to a callee even though those bytes are not
// stored inline in the darray itself.
func argumentStorePointees(t Type) regionPointees {
	p := argumentRegionPointees(t)
	collectRegionPointees(t, &p, map[Type]bool{})
	return p
}
