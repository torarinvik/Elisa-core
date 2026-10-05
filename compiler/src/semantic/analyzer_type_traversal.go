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
		return a.containsAffineHandleValuesWithSeen(t, map[Type]bool{}, 0)
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

func (a *Analyzer) typeCanContainRegionRefs(t Type, seen map[string]bool) bool {
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
		return a.typeCanContainRegionRefs(tt.Elem, seen)
	case *DArrayType:
		return true
	case *OptionalType:
		return a.typeCanContainRegionRefs(tt.Value, seen)
	case *ViewType:
		return true
	case *CStrType:
		return true
	case *SViewType:
		return true
	case *DictType:
		return a.typeCanContainRegionRefs(tt.Key, seen) || a.typeCanContainRegionRefs(tt.Value, seen)
	case *SetType:
		return a.typeCanContainRegionRefs(tt.Elem, seen)
	case *PackedVariantViewType:
		for _, field := range tt.Enum.Common {
			if a.typeCanContainRegionRefs(field.Type, seen) {
				return true
			}
		}
		for _, payloadType := range tt.Variant.Payload {
			if a.typeCanContainRegionRefs(payloadType, seen) {
				return true
			}
		}
		return false
	case *StructType:
		for _, field := range tt.Fields {
			if a.typeCanContainRegionRefs(field.Type, seen) {
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
				if a.typeCanContainRegionRefs(payload, seen) {
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
				if a.typeCanContainRegionRefs(fieldType, seen) {
					return true
				}
			}
			return false
		}
		if base, ok := tt.Base.(*EnumType); ok {
			for _, variant := range base.Variants {
				for _, payload := range variant.Payload {
					payloadType := a.substituteType(payload, nil, nil, nil, nil)
					if a.typeCanContainRegionRefs(payloadType, seen) {
						return true
					}
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.typeCanContainRegionRefs(arg, seen) {
				return true
			}
		}
		return a.typeCanContainRegionRefs(tt.Base, seen)
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
	if t == nil || !a.containsBorrowedOwnerRefValues(t, map[string]bool{}) {
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

func (a *Analyzer) abstractParamRegionRefState(t Type, paramIndex int, seen map[string]bool) (regionRefState, bool) {
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
				state.Fields = cloneRegionRefState(elemState).Fields
			}
		}
		state.PackedStoreSummaryKnown = false
		return withPackedStoreProvenanceSummary(state), true
	case *StructType:
		for _, field := range tt.Fields {
			fieldState, ok := a.abstractParamRegionRefState(field.Type, paramIndex, seen)
			if !ok {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]regionRefState{}
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
				fieldState, ok := a.abstractParamRegionRefState(fieldType, paramIndex, seen)
				if !ok {
					continue
				}
				if state.Fields == nil {
					state.Fields = map[string]regionRefState{}
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
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): elemState,
			}
		}
	case *DArrayType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): elemState,
			}
		}
	case *ViewType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): elemState,
			}
		}
	case *DictType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Value, paramIndex, seen); ok {
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): elemState,
			}
		} else if keyState, ok := a.abstractParamRegionRefState(tt.Key, paramIndex, seen); ok {
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): keyState,
			}
		}
	case *SetType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): elemState,
			}
		}
	}
	state.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(state), true
}
