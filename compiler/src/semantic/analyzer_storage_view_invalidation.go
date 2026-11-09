package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"sort"
	"strings"
)

// checkIteratorInvalidationForMutableRefArg rejects passing an actively-iterated relocatable
// container (or a borrow alias of one) to a callee by MUTABLE reference. The function-local
// iteration lock cannot see a push/clear the callee performs through that ref (the callee is
// analyzed in its own scope), so this is the conservative-but-sound call-site guard: any
// mutable-ref pass of an iterated container is rejected, since the callee MIGHT relocate it.
// Immutable-ref args are never routed here (the caller gates on a mutable RefType param), so a
// read-only `count_it(&xs)` during iteration stays allowed.
func (a *Analyzer) checkIteratorInvalidationForMutableRefArg(arg ast.Expr) {
	if a == nil || arg == nil || len(a.currentIteratedSources) == 0 {
		return
	}
	place := arg
	if addr, ok := stripOptimizationParens(arg).(*ast.AddrOfExpr); ok {
		place = addr.Operand
	}
	key := optimizationExprString(place)
	if key != "" {
		if _, iterated := a.currentIteratedSources[key]; iterated {
			a.errorf(arg.Pos(), "cannot pass %q by mutable reference while it is being iterated: the callee may push/clear/relocate its buffer out from under the loop. Iterate by index up to a saved count, or collect into a separate darray first", key)
			return
		}
	}
	for _, root := range a.mutationRootsForTarget(place) {
		if root == key {
			continue
		}
		if _, iterated := a.currentIteratedSources[root]; iterated {
			a.errorf(arg.Pos(), "cannot pass %q (a borrow of %q) by mutable reference while %q is being iterated: the callee may relocate its buffer out from under the loop. Iterate by index up to a saved count, or collect into a separate darray first", key, root, root)
			return
		}
	}
}

// checkIteratorInvalidationForEnclosingMutableRefArg rejects passing, by MUTABLE reference, a
// place that strictly ENCLOSES an actively-iterated container (`grow(&p)` or `p.step()` inside
// `for v in p.items:`). The callee reaches the iterand through the owner and may push/clear/
// replace it, relocating the buffer the loop walks; it is analyzed in its own scope, so the
// call site is the only place this is visible.
func (a *Analyzer) checkIteratorInvalidationForEnclosingMutableRefArg(arg ast.Expr) {
	if a == nil || arg == nil || len(a.currentIteratedSources) == 0 {
		return
	}
	place := stripOptimizationParens(arg)
	if addr, ok := place.(*ast.AddrOfExpr); ok {
		place = addr.Operand
	}
	key := optimizationExprString(place)
	if key == "" {
		return
	}
	if enclosed := iteratedSourceEnclosedBy(a.currentIteratedSources, key); enclosed != "" {
		a.errorf(arg.Pos(), "cannot pass %q by mutable reference while %q is being iterated: the callee may push/clear/replace it through %q and move its buffer out from under the loop. Iterate by index up to a saved count, or collect into a separate darray first", key, enclosed, key)
	}
}

func (a *Analyzer) invalidateStorageViewsForSource(source ast.Expr, reason string) {
	a.invalidateStorageViewsForSourceMode(source, reason, false)
}

// invalidateStorageViewsForSourceMode is invalidateStorageViewsForSource; REPLACED marks a
// whole-value store, which no stable backing can make harmless (see Replaced).
func (a *Analyzer) invalidateStorageViewsForSourceMode(source ast.Expr, reason string, replaced bool) {
	a.invalidateStorageViewsForSourceScoped(source, reason, replaced, true)
}

// iteratedSourceEnclosedBy returns the iterated place that PLACE strictly encloses (`s` encloses
// `s.items` and `s.rows[0]`), or "". The smallest key wins so the report is deterministic.
func iteratedSourceEnclosedBy[V any](iterated map[string]V, place string) string {
	best := ""
	for key := range iterated {
		if len(key) <= len(place) || key[:len(place)] != place {
			continue
		}
		if next := key[len(place)]; next != '.' && next != '[' {
			continue
		}
		if best == "" || key < best {
			best = key
		}
	}
	return best
}

// invalidateStorageViewsForSourceScoped is the chokepoint. followAliases=false limits the
// mutation to the place's OWN storage: replacing a value-typed local (`f <- other` over a
// struct built from `buf`) rewrites f's bytes and leaves `buf` -- and views into it -- intact.
func (a *Analyzer) invalidateStorageViewsForSourceScoped(source ast.Expr, reason string, replaced, followAliases bool) {
	key := optimizationExprString(source)
	if key == "" {
		return
	}
	mutatedSources := map[string]bool{key: true}
	if followAliases {
		for _, root := range a.mutationRootsForTarget(source) {
			mutatedSources[root] = true
		}
		a.expandStorageViewMutationAliases(mutatedSources)
	}
	// Iterator invalidation: relocating the buffer of a container that is being iterated
	// would leave the live iteration reading freed/stale memory. Reject it at this single
	// chokepoint that every relocating mutation (push/extend/reserve/clear/truncate) funnels
	// through — the same machinery that invalidates interior references.
	if _, iterated := a.currentIteratedSources[key]; iterated {
		a.errorf(source.Pos(), "cannot mutate %q while it is being iterated: %s would move its buffer out from under the loop. Iterate by index up to a saved count, collect into a separate darray, or back it with a stable region (reserve_commit/fixed)", key, reason)
	} else if enclosed := iteratedSourceEnclosedBy(a.currentIteratedSources, key); replaced && enclosed != "" {
		// Replacing a place that ENCLOSES the iterand (`s <- S{...}` inside `for v in
		// s.items:`) replaces the iterand's header along with it: the loop keeps walking the
		// old buffer exactly as after `s.items <- [..]`.
		a.errorf(source.Pos(), "cannot mutate %q while it is being iterated: %s would move its buffer out from under the loop. Iterate by index up to a saved count, collect into a separate darray, or back it with a stable region (reserve_commit/fixed)", enclosed, reason)
	} else {
		// Alias vector: the mutation reaches an iterated container THROUGH a borrow local
		// (`ys: mutable darray[T]& = &xs; for v in xs: ys.push(v)`). The lock keys on the
		// iterand's own spelling ("xs"), but the relocating push is emitted against "ys".
		// Resolve the mutation's laundered roots (the same alias-binding machinery the
		// mutable-alias checker uses) and reject if any of them is the locked iterand.
		for _, root := range a.mutationRootsForTarget(source) {
			if root == key || !followAliases {
				continue
			}
			if _, iterated := a.currentIteratedSources[root]; iterated {
				a.errorf(source.Pos(), "cannot mutate %q through borrow %q while %q is being iterated: %s would move its buffer out from under the loop. Iterate by index up to a saved count, collect into a separate darray, or back it with a stable region (reserve_commit/fixed)", root, key, root, reason)
				break
			}
		}
	}
	a.invalidateStorageViewDepsSparing(mutatedSources, reason, false, replaced, a.storageViewSiblingSpare(key))
}

// storageViewSiblingSpare returns a predicate naming the view sources a mutation of the field
// path KEY provably cannot reach, or nil. Sibling darray fields of one root may share a buffer
// (darray copies are shallow: `P{storage: buf, lines: buf}`), so the root-overlap rule stays the
// default. The one sound exception is a sibling whose ELEMENT type differs: a darray[u8] and a
// darray[u32] can never name the same backing, so growing `p.lines` cannot move or overwrite
// the bytes behind a view of `p.storage`. Only concrete element types qualify (builtins, sview,
// cstr, non-generic structs); anything generic keeps the conservative overlap.
func (a *Analyzer) storageViewSiblingSpare(key string) func(source, candidate string) bool {
	// An element store `p.a[i] = x` writes in place inside p.a's buffer: siblings of p.a are
	// as untouched as by a push to p.a, so the field path before the index decides.
	if cut := strings.Index(key, "["); cut >= 0 {
		key = key[:cut]
	}
	root := storageViewSourceRoot(key)
	if root == key {
		return nil
	}
	mutatedElem := a.storageViewFieldPathElem(key)
	if mutatedElem == "" {
		return nil
	}
	return func(source, candidate string) bool {
		// Alias expansion can add other paths under the same root; the mutated storage is
		// still KEY's, so the sibling argument applies to them too.
		if storageViewSourceRoot(candidate) != root {
			return false
		}
		if source == key || strings.Contains(source, "[") || storageViewSourceRoot(source) != root || source == root {
			return false
		}
		if strings.HasPrefix(source, key+".") || strings.HasPrefix(source, key+"[") || strings.HasPrefix(key, source+".") {
			return false
		}
		elem := a.storageViewFieldPathElem(source)
		return elem != "" && elem != mutatedElem
	}
}

// storageViewFieldPathElem resolves a dotted field path (`p.a.b`) from the current scope to a
// darray of a concrete element type and returns that element's key, or "".
func (a *Analyzer) storageViewFieldPathElem(path string) string {
	if a == nil || a.currentScope == nil {
		return ""
	}
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return ""
	}
	sym, ok := a.currentScope.Lookup(parts[0])
	if !ok || sym == nil {
		return ""
	}
	current := a.currentTrackedValueType(sym)
	if current == nil {
		current = sym.Type
	}
	for _, name := range parts[1:] {
		structType, ok := StripAggregateStateType(stripStorageViewRefs(current)).(*StructType)
		if !ok || structType == nil {
			return ""
		}
		field, ok := structType.Fields[name]
		if !ok {
			return ""
		}
		current = field.Type
	}
	darray, ok := StripAggregateStateType(stripStorageViewRefs(current)).(*DArrayType)
	if !ok || darray == nil {
		return ""
	}
	return storageViewConcreteElemKey(darray.Elem)
}

// storageViewConcreteElemKey names a concrete, non-generic element type, or returns "" when
// the type could stand for another (a type parameter, a generic aggregate). Equal keys are
// treated as possibly-aliasing, so a collision only costs precision.
func storageViewConcreteElemKey(t Type) string {
	switch elem := StripAggregateStateType(t).(type) {
	case *BuiltinType:
		if elem != nil {
			return "builtin " + elem.Name
		}
	case *SViewType:
		if elem != nil {
			return "sview"
		}
	case *CStrType:
		if elem != nil {
			return "cstr"
		}
	case *StructType:
		if elem != nil && len(elem.TypeParams) == 0 && len(elem.GenericParams) == 0 {
			return "struct " + elem.Namespace + "::" + elem.Name
		}
	case *EnumType:
		// Enum types carry no namespace; the type's identity distinguishes them.
		if elem != nil {
			return fmt.Sprintf("enum %p", elem)
		}
	}
	return ""
}

// storageViewPathCrossesAppendOnly: the argument type, or a struct reached along the origin's
// `.field` suffix, is an `@append_only` store (a view below it never relocates).
func storageViewPathCrossesAppendOnly(t Type, suffix string) bool {
	segments := strings.Split(strings.TrimPrefix(suffix, "."), ".")
	for i := 0; i <= len(segments); i++ {
		st, isStruct := stripStorageViewRefs(t).(*StructType)
		if !isStruct || st == nil {
			return false
		}
		if st.AppendOnly {
			return true
		}
		if i == len(segments) || segments[i] == "" {
			return false
		}
		name := segments[i]
		if cut := strings.IndexByte(name, '['); cut >= 0 {
			return false
		}
		field, ok := st.Fields[name]
		if !ok {
			return false
		}
		t = field.Type
	}
	return false
}

func stripStorageViewRefs(t Type) Type {
	for {
		ref, ok := t.(*RefType)
		if !ok || ref == nil {
			return t
		}
		t = ref.Elem
	}
}

// invalidateStorageViewsForWholeAssignment invalidates interior references into a container
// that is replaced as a whole (`xs <- [1, 2, 3]`, `s <- S{...}` over a struct holding a darray,
// or a store through a `mutable darray[T]&` parameter). The container's header now names a
// different buffer, so `&xs[0]` taken before the store reads the old one -- exactly the
// staleness a relocating push causes, and funnelled through the same chokepoint (which also
// rejects replacing a container that is being iterated). Rebinding a reference slot to another
// reference (`ys <- &zs`) leaves the old referent untouched and is not a mutation of it.
func (a *Analyzer) invalidateStorageViewsForWholeAssignment(target ast.Expr, targetType, valueType Type) {
	if a == nil || target == nil || isBorrowLikeType(valueType) {
		return
	}
	switch stripRefForBounds(targetType).(type) {
	case *DArrayType, *DictType, *SetType, *StructType:
	default:
		return
	}
	place := stripOptimizationParens(target)
	// The target's OWN facts are not invalidated: recordStorageViewAssignment rebinds them
	// from the value right after. Without this, `xs <- [h for h in xs if ...]` over an
	// element-borrowing darray (darray[sview]) made xs a view of itself, and the next
	// whole assignment poisoned xs through its own replaced binding.
	var self *Symbol
	var selfDep storageViewDependencyState
	selfHad := false
	if ident, ok := place.(*ast.Ident); ok && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(ident.Name); found {
			self = sym
			selfDep, selfHad = a.currentStorageViewDeps[sym]
		}
	}
	// Only a place reached THROUGH a reference writes someone else's container; a value-typed
	// root owns what is replaced, so aliases it was built from are not mutated.
	followAliases := true
	if root := rootIdentExpr(place); root != nil && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(root.Name); found && sym != nil {
			if _, isRef := sym.Type.(*RefType); !isRef {
				followAliases = false
			}
		}
	}
	a.invalidateStorageViewsForSourceScoped(place, storageViewMutationReason(target, "reassignment"), true, followAliases)
	if self != nil && selfHad {
		a.currentStorageViewDeps[self] = selfDep
	}
}

// invalidateStorageViewsForMutableRefArg invalidates interior references into a container passed
// to a MUTABLE reference parameter that can reach relocatable storage (a darray, or a struct that
// may hold one). The callee is analyzed in its own scope, so its push/clear through the borrow is
// invisible here; the call site is the only place a live `&xs[i]` can be seen outliving it. The
// iteration lock has its own call-site guard (checkIteratorInvalidationForMutableRefArg), so this
// touches only the storage-view facts. A stable backing (reserve_commit/fixed) still drops the
// error in the pending post-pass, exactly as for a direct push.
func (a *Analyzer) invalidateStorageViewsForMutableRefArg(arg ast.Expr, callee string) {
	a.invalidateStorageViewsForMutableRefArgSparing(arg, callee, nil)
}

// invalidateStorageViewsForMutableRefArgSparing is invalidateStorageViewsForMutableRefArg with a
// per-callee SPARE (see storageViewCalleeWriteSpare): field views the callee cannot write survive.
func (a *Analyzer) invalidateStorageViewsForMutableRefArgSparing(arg ast.Expr, callee string, spare func(source, candidate string) bool) {
	if a == nil || arg == nil || len(a.currentStorageViewDeps) == 0 {
		return
	}
	place := stripOptimizationParens(arg)
	if addr, ok := place.(*ast.AddrOfExpr); ok {
		place = addr.Operand
	}
	key := optimizationExprString(place)
	if key == "" {
		return
	}
	mutatedSources := map[string]bool{key: true}
	for _, root := range a.mutationRootsForTarget(place) {
		mutatedSources[root] = true
	}
	a.invalidateStorageViewDepsSparing(mutatedSources, fmt.Sprintf("mutable borrow of %s by %s", key, callee), true, false, spare)
}

// invalidateStorageViewDeps marks every live view depending on MUTATED_SOURCES stale. With
// INTERIOR_ONLY, only addresses into a container buffer are affected: a value merely derived
// from the container (a comprehension copy, a parse result) survives a callee's borrow, which
// cannot relocate storage the value does not point into.
func (a *Analyzer) invalidateStorageViewDeps(mutatedSources map[string]bool, reason string, interiorOnly bool) {
	a.invalidateStorageViewDepsMode(mutatedSources, reason, interiorOnly, false)
}

func (a *Analyzer) invalidateStorageViewDepsMode(mutatedSources map[string]bool, reason string, interiorOnly, replaced bool) {
	a.invalidateStorageViewDepsSparing(mutatedSources, reason, interiorOnly, replaced, nil)
}

// invalidateStorageViewDepsSparing is invalidateStorageViewDepsMode; SPARE(source, candidate)
// exempts a (view source, mutated source) pair proven disjoint (see storageViewSiblingSpare).
func (a *Analyzer) invalidateStorageViewDepsSparing(mutatedSources map[string]bool, reason string, interiorOnly, replaced bool, spare func(source, candidate string) bool) {
	candidates := sortedStorageViewSources(mutatedSources)
	a.noteStorageViewLoopMutation(candidates, reason, interiorOnly, replaced, spare)
	if len(a.currentStorageViewDeps) == 0 {
		return
	}
	for sym, dep := range a.currentStorageViewDeps {
		if !dep.Valid || (interiorOnly && !dep.Interior) {
			continue
		}
		matchedSource := storageViewDepMatchedSource(dep, candidates, spare)
		if matchedSource == "" {
			continue
		}
		dep.Valid = false
		dep.InvalidatedBy = reason
		dep.Replaced = replaced
		if matchedSource != "" {
			dep.InvalidatedBy += fmt.Sprintf(" (matched mutation source %q)", matchedSource)
		}
		a.currentStorageViewDeps[sym] = dep
	}
}

// storageViewDepMatchedSource names the first mutated candidate that reaches one of dep's
// sources (and is not spared), or "".
func storageViewDepMatchedSource(dep storageViewDependencyState, candidates []string, spare func(source, candidate string) bool) string {
	for _, source := range dep.Sources {
		for _, candidate := range candidates {
			if storageViewSourcesOverlap(source, candidate) && (spare == nil || !spare(source, candidate)) {
				return candidate
			}
		}
	}
	return ""
}

func sortedStorageViewSources(sources map[string]bool) []string {
	out := make([]string, 0, len(sources))
	for source := range sources {
		out = append(out, source)
	}
	sort.Strings(out)
	return out
}

func storageViewMutationReason(source ast.Expr, operation string) string {
	key := optimizationExprString(source)
	if key == "" {
		key = "container"
	}
	return fmt.Sprintf("%s of %s", operation, key)
}
