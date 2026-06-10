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
	return a.containsAffineHandleValuesWithSeen(t, map[Type]bool{}, 0)
}

func (a *Analyzer) containsAffineHandleValuesWithSeen(t Type, seen map[Type]bool, depth int) bool {
	if t == nil {
		return false
	}
	if depth > semanticTraversalDepthLimit {
		a.reportSemanticDepthLimit("affine-handle traversal", semanticTraversalDepthLimit)
		return false
	}
	if isAffineHandleType(t) {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.containsAffineHandleValuesWithSeen(tt.Elem, seen, depth+1)
	case *DArrayType:
		return a.containsAffineHandleValuesWithSeen(tt.Elem, seen, depth+1)
	case *ViewType:
		return a.containsAffineHandleValuesWithSeen(tt.Elem, seen, depth+1)
	case *OptionalType:
		return a.containsAffineHandleValuesWithSeen(tt.Value, seen, depth+1)
	case *ErrorUnionType:
		if a.containsAffineHandleValuesWithSeen(tt.Value, seen, depth+1) {
			return true
		}
		if tt.Errors != nil {
			for _, payloads := range tt.Errors.Payloads {
				for _, payloadType := range payloads {
					if a.containsAffineHandleValuesWithSeen(payloadType, seen, depth+1) {
						return true
					}
				}
			}
		}
		return false
	case *DictType:
		return a.containsAffineHandleValuesWithSeen(tt.Key, seen, depth+1) || a.containsAffineHandleValuesWithSeen(tt.Value, seen, depth+1)
	case *DictEntryType:
		return a.containsAffineHandleValuesWithSeen(tt.Dict, seen, depth+1)
	case *PackedVariantViewType:
		for _, field := range tt.Enum.Common {
			if a.containsAffineHandleValuesWithSeen(field.Type, seen, depth+1) {
				return true
			}
		}
		for _, payloadType := range tt.Variant.Payload {
			if a.containsAffineHandleValuesWithSeen(payloadType, seen, depth+1) {
				return true
			}
		}
		return false
	case *EnumType:
		for _, field := range tt.Common {
			if a.containsAffineHandleValuesWithSeen(field.Type, seen, depth+1) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payloadType := range variant.Payload {
				if a.containsAffineHandleValuesWithSeen(payloadType, seen, depth+1) {
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
				if a.containsAffineHandleValuesWithSeen(fieldType, seen, depth+1) {
					return true
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.containsAffineHandleValuesWithSeen(arg, seen, depth+1) {
				return true
			}
		}
		return a.containsAffineHandleValuesWithSeen(tt.Base, seen, depth+1)
	case *StructType:
		for _, field := range tt.Fields {
			if a.containsAffineHandleValuesWithSeen(field.Type, seen, depth+1) {
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
	case *DStrType:
		return true
	case *SViewType:
		return true
	case *DictType:
		return a.typeCanContainRegionRefs(tt.Key, seen) || a.typeCanContainRegionRefs(tt.Value, seen)
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

func (a *Analyzer) abstractParamBorrowedOwnerRefState(t Type, baseKey affineValueKey, seen map[string]bool) (borrowedOwnerRefState, bool) {
	if t == nil || !a.containsBorrowedOwnerRefValues(t, map[string]bool{}) {
		return borrowedOwnerRefState{}, false
	}
	if _, ok := borrowableOwnerRefElemType(t); ok {
		return borrowedOwnerRefState{HasDirect: true, Direct: baseKey}, true
	}
	key := t.String()
	if seen[key] {
		return borrowedOwnerRefState{}, false
	}
	seen[key] = true
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
	}
	return state, hasBorrowedOwnerRefState(state)
}

func (a *Analyzer) abstractParamRegionRefState(t Type, paramIndex int, seen map[string]bool) (regionRefState, bool) {
	if t == nil || !a.typeCanContainRegionRefs(t, map[string]bool{}) {
		return regionRefState{}, false
	}
	key := t.String()
	if seen[key] {
		return regionRefStateFromParamDependency(paramIndex), true
	}
	seen[key] = true
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
	}
	state.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(state), true
}
