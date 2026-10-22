package semantic

import (
	"fmt"

	"elisacore/src/lexer"
)

func (a *Analyzer) semanticLimitPos() lexer.Pos {
	if a.currentFuncDecl != nil {
		return a.currentFuncDecl.Pos()
	}
	if a.file != nil {
		if len(a.file.Decls) != 0 {
			return a.file.Decls[0].Pos()
		}
		return lexer.Pos{File: a.file.Filename}
	}
	return lexer.Pos{}
}

func (a *Analyzer) reportSemanticDepthLimit(operation string, limit int) {
	a.semanticLimitHits++
	if a.semanticLimitDiagnostics == nil {
		a.semanticLimitDiagnostics = map[string]bool{}
	}
	key := operation
	if a.currentFuncDecl != nil {
		key += ":" + a.currentFuncDecl.Name
	}
	if a.semanticLimitDiagnostics[key] {
		return
	}
	a.semanticLimitDiagnostics[key] = true
	context := "while analyzing top-level declarations"
	if a.currentFuncDecl != nil {
		context = fmt.Sprintf("while analyzing function %q", a.currentFuncDecl.Name)
	}
	a.errorf(a.semanticLimitPos(), "semantic analysis exceeded %s recursion limit (%d) %s", operation, limit, context)
}

func (a *Analyzer) containsAffineHandleValues(t Type, seen map[string]bool) bool {
	switch t.(type) {
	case *ArrayType, *DArrayType, *ViewType, *OptionalType, *ErrorUnionType, *DictType, *SetType,
		*DictEntryType, *PackedVariantViewType, *EnumType, *GenericInstanceType, *StructType:
	default:
		// typeContainsWithSeen descends into none of these, and isAffineHandleType holds only
		// for struct and generic-instance types: the answer is false without a traversal.
		return false
	}
	return a.memoTypePredicate(&a.affineHandleMemo, t, func() bool {
		seen := a.typeSeenScratch
		if seen == nil || a.typeSeenScratchBusy {
			seen = map[Type]bool{}
		} else {
			clear(seen)
			a.typeSeenScratchBusy = true
			defer func() { a.typeSeenScratchBusy = false }()
		}
		result := a.containsAffineHandleValuesWithSeen(t, seen, 0)
		if a.typeSeenScratch == nil && len(seen) <= 1024 {
			a.typeSeenScratch = seen
		}
		return result
	})
}

// memoTypePredicate caches a predicate over t's by-value type graph (reachability of some
// leaf), a pure function of t once declaration shapes are final. Before that, or when the
// traversal hit the depth limit (which reports a diagnostic per function), it is computed
// afresh.
func (a *Analyzer) memoTypePredicate(memo *map[Type]bool, t Type, compute func() bool) bool {
	if !a.typeShapesFrozen || t == nil {
		return compute()
	}
	if cached, ok := (*memo)[t]; ok {
		return cached
	}
	if base := a.parallelTypePredicateMemoBase(memo); base != nil {
		if cached, ok := base[t]; ok {
			return cached
		}
	}
	hits := a.semanticLimitHits
	result := compute()
	if a.semanticLimitHits == hits {
		if *memo == nil {
			*memo = map[Type]bool{}
		}
		(*memo)[t] = result
	}
	return result
}

func (a *Analyzer) parallelTypePredicateMemoBase(memo *map[Type]bool) map[Type]bool {
	if a == nil || a.parallel == nil {
		return nil
	}
	bases := a.parallel.predicateMemoBases
	switch memo {
	case &a.affineHandleMemo:
		return bases.affineHandles
	case &a.borrowedOwnerRefMemo:
		return bases.borrowedOwnerRefs
	case &a.regionRefsMemo:
		return bases.regionRefs
	case &a.mutableBorrowCapabilityMemo:
		return bases.mutableBorrowCapabilities
	case &a.storeScanAddressMemo:
		return bases.storeScanAddresses
	default:
		return nil
	}
}

func (a *Analyzer) containsAffineHandleValuesWithSeen(t Type, seen map[Type]bool, depth int) bool {
	return a.typeContainsWithSeen(t, isAffineHandleType, "affine-handle traversal", seen, depth)
}

// typeContainsWithSeen reports whether a value of type t holds, by value anywhere inside it,
// a type matching leaf.
func (a *Analyzer) typeContainsWithSeen(t Type, leaf func(Type) bool, what string, seen map[Type]bool, depth int) bool {
	if t == nil {
		return false
	}
	if depth > semanticTraversalDepthLimit {
		a.reportSemanticDepthLimit(what, semanticTraversalDepthLimit)
		return false
	}
	if leaf(t) {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.typeContainsWithSeen(tt.Elem, leaf, what, seen, depth+1)
	case *DArrayType:
		return a.typeContainsWithSeen(tt.Elem, leaf, what, seen, depth+1)
	case *ViewType:
		return a.typeContainsWithSeen(tt.Elem, leaf, what, seen, depth+1)
	case *OptionalType:
		return a.typeContainsWithSeen(tt.Value, leaf, what, seen, depth+1)
	case *ErrorUnionType:
		if a.typeContainsWithSeen(tt.Value, leaf, what, seen, depth+1) {
			return true
		}
		if tt.Errors != nil {
			for _, payloads := range tt.Errors.Payloads {
				for _, payloadType := range payloads {
					if a.typeContainsWithSeen(payloadType, leaf, what, seen, depth+1) {
						return true
					}
				}
			}
		}
		return false
	case *DictType:
		return a.typeContainsWithSeen(tt.Key, leaf, what, seen, depth+1) || a.typeContainsWithSeen(tt.Value, leaf, what, seen, depth+1)
	case *SetType:
		return a.typeContainsWithSeen(tt.Elem, leaf, what, seen, depth+1)
	case *DictEntryType:
		return a.typeContainsWithSeen(tt.Dict, leaf, what, seen, depth+1)
	case *PackedVariantViewType:
		for _, field := range tt.Enum.Common {
			if a.typeContainsWithSeen(field.Type, leaf, what, seen, depth+1) {
				return true
			}
		}
		for _, payloadType := range tt.Variant.Payload {
			if a.typeContainsWithSeen(payloadType, leaf, what, seen, depth+1) {
				return true
			}
		}
		return false
	case *EnumType:
		for _, field := range tt.Common {
			if a.typeContainsWithSeen(field.Type, leaf, what, seen, depth+1) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payloadType := range variant.Payload {
				if a.typeContainsWithSeen(payloadType, leaf, what, seen, depth+1) {
					return true
				}
			}
		}
		return false
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if a.typeContainsWithSeen(fieldType, leaf, what, seen, depth+1) {
					return true
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.typeContainsWithSeen(arg, leaf, what, seen, depth+1) {
				return true
			}
		}
		return a.typeContainsWithSeen(tt.Base, leaf, what, seen, depth+1)
	case *StructType:
		for _, field := range tt.Fields {
			if a.typeContainsWithSeen(field.Type, leaf, what, seen, depth+1) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) typeStructurallyAtomicSafe(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if IsNumericType(t) || IsBoolType(t) {
		return true
	}
	if _, ok := t.(*TypeParamType); ok {
		return true
	}
	if isPointerLikeCastType(t) {
		return true
	}
	key := t.String()
	if seen[key] {
		return true
	}
	seen[key] = true
	switch tt := t.(type) {
	case *GenericInstanceType:
		for _, arg := range tt.Args {
			if !a.typeStructurallyAtomicSafe(arg, seen) {
				return false
			}
		}
		return false
	default:
		return false
	}
}

// typeCanContainRegionRefs reports whether a value of type t can hold a region reference. A call
// with an empty `seen` (every caller outside the walk) is memoized per type identity once type
// shapes are frozen: the walk is a reachability query over t's type graph (`seen` is a visited
// set that is never released, so the answer does not depend on visit order), and the only
// analyzer state it reads besides the types themselves is staticImpls (through substituteType's
// associated-type projection), which collectStaticImpls fills before the freeze. Only the bool
// is cached. A walk that hit a depth limit is not cached (memoTypePredicate).
func (a *Analyzer) typeCanContainRegionRefs(t Type, seen map[string]bool) bool {
	if len(seen) != 0 {
		return a.typeCanContainRegionRefsWalk(t, seen)
	}
	return a.memoTypePredicate(&a.regionRefsMemo, t, func() bool {
		return a.typeCanContainRegionRefsWalk(t, seen)
	})
}

func (a *Analyzer) typeCanContainRegionRefsWalk(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if _, ok := t.(*RefType); ok {
		return true
	}
	if _, ok := t.(*PackedEnumStoreType); ok {
		return true
	}
	key := t.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.typeCanContainRegionRefsWalk(tt.Elem, seen)
	case *DArrayType:
		return true
	case *OptionalType:
		return a.typeCanContainRegionRefsWalk(tt.Value, seen)
	case *ViewType:
		return true
	case *CStrType:
		return true
	case *SViewType:
		return true
	case *DictType:
		return a.typeCanContainRegionRefsWalk(tt.Key, seen) || a.typeCanContainRegionRefsWalk(tt.Value, seen)
	case *SetType:
		return a.typeCanContainRegionRefsWalk(tt.Elem, seen)
	case *PackedVariantViewType:
		for _, field := range tt.Enum.Common {
			if a.typeCanContainRegionRefsWalk(field.Type, seen) {
				return true
			}
		}
		for _, payloadType := range tt.Variant.Payload {
			if a.typeCanContainRegionRefsWalk(payloadType, seen) {
				return true
			}
		}
		return false
	case *StructType:
		for _, field := range tt.Fields {
			if a.typeCanContainRegionRefsWalk(field.Type, seen) {
				return true
			}
		}
		return false
	case *EnumType:
		if tt.Packed {
			return true
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if a.typeCanContainRegionRefsWalk(payload, seen) {
					return true
				}
			}
		}
		return false
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if a.typeCanContainRegionRefsWalk(fieldType, seen) {
					return true
				}
			}
			return false
		}
		if base, ok := tt.Base.(*EnumType); ok {
			for _, variant := range base.Variants {
				for _, payload := range variant.Payload {
					payloadType := a.substituteType(payload, nil, nil, nil, nil)
					if a.typeCanContainRegionRefsWalk(payloadType, seen) {
						return true
					}
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.typeCanContainRegionRefsWalk(arg, seen) {
				return true
			}
		}
		return a.typeCanContainRegionRefsWalk(tt.Base, seen)
	default:
		return false
	}
}

// typeMayOwnArenaStorage reports whether a value of type t may own storage allocated in an
// arena (a container buffer, a packed store), as opposed to only viewing storage owned
// elsewhere. A by-value copy of such a value out of a container still points into the arena
// the container allocated it in. Anything not known to be a pure view or scalar counts.
func (a *Analyzer) typeMayOwnArenaStorage(t Type, seen map[string]bool) bool {
	switch t.(type) {
	case nil:
		return false
	case *RefType, *ViewType, *SViewType, *CStrType, *PackedVariantViewType, *FuncType:
		return false
	case *OpaqueType:
		// An `extern T` handle is foreign memory the C side allocated and owns; safe code has
		// no way to put Elisa arena storage behind it, so copying one out of a region-fed
		// aggregate (`structs.context`) copies a pointer the region never backed.
		return false
	case *DArrayType, *DictType, *SetType, *PackedEnumStoreType, *TypeParamType:
		return true
	}
	key := t.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *BuiltinType, *BitIntType, *IDType, *ConstEnumType, *NullType, *NeverType:
		return false
	case *ArrayType:
		return a.typeMayOwnArenaStorage(tt.Elem, seen)
	case *OptionalType:
		return a.typeMayOwnArenaStorage(tt.Value, seen)
	case *AggregateStateType:
		return a.typeMayOwnArenaStorage(tt.Base, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if a.typeMayOwnArenaStorage(field.Type, seen) {
				return true
			}
		}
		return false
	case *StructType:
		for _, field := range tt.Fields {
			if a.typeMayOwnArenaStorage(field.Type, seen) {
				return true
			}
		}
		return false
	case *EnumType:
		if tt.Packed {
			return true
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if a.typeMayOwnArenaStorage(payload, seen) {
					return true
				}
			}
		}
		return false
	}
	return true
}

func (a *Analyzer) abstractParamBorrowedOwnerRefState(t Type, baseKey affineValueKey, seen map[string]bool) (borrowedOwnerRefState, bool) {
	if t == nil || !a.containsBorrowedOwnerRefValues(t) {
		return borrowedOwnerRefState{}, false
	}
	if _, ok := borrowableOwnerRefElemType(t); ok {
		return borrowedOwnerRefState{HasDirect: true, Direct: baseKey}, true
	}
	// `seen` guards the current PATH, not the whole walk: it exists to stop a
	// recursive type (`struct Node: next: Node`) from descending forever, and it is
	// released on the way back up. Leaving the mark set made the memo leak across
	// SIBLINGS — two fields of the same type (`names: darray[sview]` beside
	// `origins: darray[sview]`) raced for it, and whichever the map handed us second
	// got the truncated answer. Since `StructType.Fields` is a map, which field lost
	// differed run to run, so a function's borrow/return summary was nondeterministic
	// AND, for the loser, wrongly said the result borrows nothing.
	key := t.String()
	if seen[key] {
		return borrowedOwnerRefState{}, false
	}
	seen[key] = true
	defer delete(seen, key)
	state := borrowedOwnerRefState{}
	switch tt := t.(type) {
	case *OptionalType:
		return a.abstractParamBorrowedOwnerRefState(tt.Value, baseKey, seen)
	case *RefType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, baseKey, seen); ok {
			if elemState.HasDirect {
				state.HasDirect = true
				state.Direct = elemState.Direct
			}
			if len(elemState.Fields) != 0 {
				state.Fields = cloneBorrowedOwnerRefState(elemState).Fields
			}
		}
		return state, hasBorrowedOwnerRefState(state)
	case *StructType:
		for _, field := range tt.Fields {
			fieldState, ok := a.abstractParamBorrowedOwnerRefState(field.Type, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, field.Name)}, seen)
			if !ok {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]borrowedOwnerRefState{}
			}
			state.Fields[field.Name] = fieldState
		}
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				fieldState, ok := a.abstractParamBorrowedOwnerRefState(fieldType, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, field.Name)}, seen)
				if !ok {
					continue
				}
				if state.Fields == nil {
					state.Fields = map[string]borrowedOwnerRefState{}
				}
				state.Fields[field.Name] = fieldState
			}
			return state, hasBorrowedOwnerRefState(state)
		}
	case *ArrayType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): elemState}
		}
	case *DArrayType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): elemState}
		}
	case *ViewType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): elemState}
		}
	case *DictType:
		// dict elements are not index-addressable; seed the value (and key) under the
		// wildcard element key so a borrowed owner stored in a dict keeps its
		// must-consume obligation across the call boundary (deep audit #12/#14).
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Value, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): elemState}
		} else if keyState, ok := a.abstractParamBorrowedOwnerRefState(tt.Key, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): keyState}
		}
	case *SetType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): elemState}
		}
	case *EnumType:
		for _, variant := range tt.Variants {
			for i, payload := range variant.Payload {
				fieldKey := moveBindVariantFieldKey(variant, i)
				fieldState, ok := a.abstractParamBorrowedOwnerRefState(payload, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, fieldKey)}, seen)
				if !ok {
					continue
				}
				if state.Fields == nil {
					state.Fields = map[string]borrowedOwnerRefState{}
				}
				state.Fields[fieldKey] = fieldState
			}
		}
	case *PackedVariantViewType:
		for i, payload := range tt.Variant.Payload {
			fieldKey := moveBindVariantFieldKey(tt.Variant, i)
			fieldState, ok := a.abstractParamBorrowedOwnerRefState(payload, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, fieldKey)}, seen)
			if !ok {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]borrowedOwnerRefState{}
			}
			state.Fields[fieldKey] = fieldState
		}
	}
	return state, hasBorrowedOwnerRefState(state)
}

func (a *Analyzer) regionRefAnyIndexField(elemState regionRefState, paramIndex int) map[string]regionRefState {
	// Most container elements abstract to exactly the parameter dependency. Reuse that
	// immutable singleton map across every occurrence for the same parameter.
	if elemState.HasDirectParamDep && elemState.DirectParamDep == paramIndex &&
		len(elemState.Deps) == 0 && len(elemState.StoreDeps) == 0 && elemState.ParamDeps.IsEmpty() &&
		len(elemState.Fields) == 0 && elemState.ParamOnlySummary {
		if a.regionRefAnyIndexParamFields == nil {
			a.regionRefAnyIndexParamFields = map[int]map[string]regionRefState{}
		}
		if fields := a.regionRefAnyIndexParamFields[paramIndex]; fields != nil {
			return fields
		}
		fields := map[string]regionRefState{regionAnyIndexFieldKey(): elemState}
		a.regionRefAnyIndexParamFields[paramIndex] = fields
		return fields
	}
	return map[string]regionRefState{regionAnyIndexFieldKey(): elemState}
}

type abstractParamRegionRefKey struct {
	typeOf     Type
	paramIndex int
}

func (a *Analyzer) abstractParamRegionRefState(t Type, paramIndex int, seen map[string]bool) (regionRefState, bool) {
	if seen == nil {
		seen = map[string]bool{}
	}
	if len(seen) == 0 && a.typeShapesFrozen && t != nil {
		key := abstractParamRegionRefKey{typeOf: t, paramIndex: paramIndex}
		if state, ok := a.abstractParamRegionRefMemo[key]; ok {
			return cloneRegionRefState(state), true
		}
		if a.parallel != nil {
			if state, ok := a.parallel.abstractParamRegionRefMemoBase[key]; ok {
				return cloneRegionRefState(state), true
			}
		}
		hits := a.semanticLimitHits
		state, ok := a.abstractParamRegionRefStateWalk(t, paramIndex, seen)
		if ok && hits == a.semanticLimitHits {
			if a.abstractParamRegionRefMemo == nil {
				a.abstractParamRegionRefMemo = map[abstractParamRegionRefKey]regionRefState{}
			}
			a.abstractParamRegionRefMemo[key] = cloneRegionRefState(state)
		}
		return state, ok
	}
	return a.abstractParamRegionRefStateWalk(t, paramIndex, seen)
}

// abstractParamRegionRefStateForBinding reads a frozen summary for a new local
// parameter binding. recordResolvedRegionRefBinding stores the result through
// copy-on-write transforms, so copying the summary's dependency maps here only
// creates short-lived clones on every function entry.
func (a *Analyzer) abstractParamRegionRefStateForBinding(t Type, paramIndex int) (regionRefState, bool) {
	if a != nil && a.typeShapesFrozen && t != nil {
		key := abstractParamRegionRefKey{typeOf: t, paramIndex: paramIndex}
		if state, ok := a.abstractParamRegionRefMemo[key]; ok {
			return state, true
		}
		if a.parallel != nil {
			if state, ok := a.parallel.abstractParamRegionRefMemoBase[key]; ok {
				return state, true
			}
		}
	}
	return a.abstractParamRegionRefState(t, paramIndex, nil)
}

func (a *Analyzer) abstractParamRegionRefStateWalk(t Type, paramIndex int, seen map[string]bool) (regionRefState, bool) {
	if t == nil || !a.typeCanContainRegionRefs(t, map[string]bool{}) {
		return regionRefState{}, false
	}
	// Path-scoped, exactly as in abstractParamBorrowedOwnerRefState above: release the
	// mark on the way out so sibling fields of the same type each get the full answer.
	key := t.String()
	if seen[key] {
		return regionRefStateFromParamDependency(paramIndex), true
	}
	seen[key] = true
	defer delete(seen, key)
	state := regionRefStateFromParamDependency(paramIndex)
	switch tt := t.(type) {
	case *OptionalType:
		return a.abstractParamRegionRefState(tt.Value, paramIndex, seen)
	case *RefType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			if len(elemState.Fields) != 0 {
				// This recursive result is newly built and its Fields map is immutable once
				// reachable from a regionRefState. cloneRegionRefState(...).Fields therefore
				// returned the same map after needlessly cloning Deps, StoreDeps, and ParamDeps.
				state.Fields = elemState.Fields
			}
		}
		state.PackedStoreSummaryKnown = false
		return withPackedStoreProvenanceSummary(state), true
	case *StructType:
		fieldCapacity := 0
		if a.typeShapesFrozen {
			// Most aggregate fields are scalar. Sizing this map to every field
			// over-allocates hundreds of megabytes while constructing parameter
			// provenance for the large self-hosted compiler types.
			for _, field := range tt.Fields {
				if a.typeCanContainRegionRefs(field.Type, map[string]bool{}) {
					fieldCapacity++
				}
			}
		}
		for _, field := range tt.Fields {
			fieldState, ok := a.abstractParamRegionRefState(field.Type, paramIndex, seen)
			if !ok {
				continue
			}
			if state.Fields == nil {
				state.Fields = make(map[string]regionRefState, fieldCapacity)
			}
			state.Fields[field.Name] = fieldState
		}
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			fieldCapacity := len(base.Fields)
			if a.typeShapesFrozen {
				fieldCapacity = 0
				for _, field := range base.Fields {
					// Exclude only fixed scalar types. Generic parameters and other
					// unresolved fields may become references after substitution.
					if !IsNumericType(field.Type) && !IsBoolType(field.Type) {
						fieldCapacity++
					}
				}
			}
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				fieldState, ok := a.abstractParamRegionRefState(fieldType, paramIndex, seen)
				if !ok {
					continue
				}
				if state.Fields == nil {
					state.Fields = make(map[string]regionRefState, fieldCapacity)
				}
				state.Fields[field.Name] = fieldState
			}
			state.PackedStoreSummaryKnown = false
			return withPackedStoreProvenanceSummary(state), true
		}
		if base, ok := tt.Base.(*EnumType); ok {
			for _, variant := range base.Variants {
				for i, payload := range variant.Payload {
					fieldType := a.substituteType(payload, map[string]Type{}, nil, nil, nil)
					fieldState, ok := a.abstractParamRegionRefState(fieldType, paramIndex, seen)
					if !ok {
						continue
					}
					if state.Fields == nil {
						state.Fields = map[string]regionRefState{}
					}
					state.Fields[moveBindVariantFieldKey(variant, i)] = fieldState
				}
			}
			state.PackedStoreSummaryKnown = false
			return withPackedStoreProvenanceSummary(state), true
		}
	case *EnumType:
		if tt.Packed {
			return state, true
		}
		for _, variant := range tt.Variants {
			for i, payload := range variant.Payload {
				fieldState, ok := a.abstractParamRegionRefState(payload, paramIndex, seen)
				if !ok {
					continue
				}
				if state.Fields == nil {
					state.Fields = map[string]regionRefState{}
				}
				state.Fields[moveBindVariantFieldKey(variant, i)] = fieldState
			}
		}
	case *ArrayType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = a.regionRefAnyIndexField(elemState, paramIndex)
		}
	case *DArrayType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = a.regionRefAnyIndexField(elemState, paramIndex)
		}
	case *ViewType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = a.regionRefAnyIndexField(elemState, paramIndex)
		}
	case *DictType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Value, paramIndex, seen); ok {
			state.Fields = a.regionRefAnyIndexField(elemState, paramIndex)
		} else if keyState, ok := a.abstractParamRegionRefState(tt.Key, paramIndex, seen); ok {
			state.Fields = a.regionRefAnyIndexField(keyState, paramIndex)
		}
	case *SetType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = a.regionRefAnyIndexField(elemState, paramIndex)
		}
	}
	state.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(state), true
}
