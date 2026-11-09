package semantic

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

// abstractRegionTypeCanContainRefs uses a private, empty traversal path. A
// builtin never holds references, so it needs neither a path map nor a memo
// lookup. Other types retain the regular traversal and caching behavior.
func (a *Analyzer) abstractRegionTypeCanContainRefs(t Type) bool {
	if t == nil {
		return false
	}
	if _, builtin := t.(*BuiltinType); builtin {
		return false
	}
	return a.typeCanContainRegionRefs(t, map[string]bool{})
}

func (a *Analyzer) abstractParamRegionRefStateWalk(t Type, paramIndex int, seen map[string]bool) (regionRefState, bool) {
	if !a.abstractRegionTypeCanContainRefs(t) {
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
		elementSeen := seen
		if element, ok := tt.Elem.(*StructType); ok && a.typeShapesFrozen && len(seen) == 1 && len(element.Fields) >= 8 && a.abstractRefElementCanRestart(element, key) {
			// Removing this sole marker cannot alter the element traversal. An
			// empty path permits the ordinary frozen root-summary memo to share
			// its large immutable Fields map across distinct reference wrappers.
			elementSeen = nil
		}
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, elementSeen); ok {
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
				if a.abstractRegionTypeCanContainRefs(field.Type) {
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
				// A builtin field is unaffected by generic substitution and can
				// never contribute reference provenance.
				if _, builtin := fieldType.(*BuiltinType); builtin {
					continue
				}
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
