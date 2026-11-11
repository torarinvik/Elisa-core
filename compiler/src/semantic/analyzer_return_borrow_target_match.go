package semantic

// Large comparisons usually reject almost every pair. Index canonical
// components once and keep only the exceptional types for pairwise checks.
func (scratch *returnBorrowTargetScratch) anyMatchingComponent() bool {
	if scratch.componentIDSet == nil {
		scratch.componentIDSet = make(map[TypeID]struct{}, len(scratch.components))
	}
	for _, component := range scratch.components {
		id := tryCanonicalTypeIDResult(component)
		if !id.ok {
			scratch.otherComponents = append(scratch.otherComponents, component)
			continue
		}
		scratch.componentIDSet[id.id] = struct{}{}
		if returnBorrowRuntimeEqualityCandidate(component) {
			scratch.bridgeComponents = append(scratch.bridgeComponents, component)
		}
	}
	for _, target := range scratch.targets {
		id := tryCanonicalTypeIDResult(target)
		if !id.ok {
			for _, component := range scratch.components {
				if SameType(target, component) {
					return true
				}
			}
			continue
		}
		if _, ok := scratch.componentIDSet[id.id]; ok {
			return true
		}
		for _, component := range scratch.otherComponents {
			if SameType(target, component) {
				return true
			}
		}
		if returnBorrowRuntimeEqualityCandidate(target) {
			for _, component := range scratch.bridgeComponents {
				if sameTypeRuntimeCompatible(target, component) {
					return true
				}
			}
		}
	}
	return false
}

// Keep both sides of every equality bridge in sameTypeRuntimeCompatible.
func returnBorrowRuntimeEqualityCandidate(typ Type) bool {
	switch t := typ.(type) {
	case *DArrayType, *DictType, *SViewType:
		return true
	case *GenericInstanceType:
		return t.Name == "DynArray" || t.Name == "DynDict"
	case *StructType:
		return t.Name == "StringView"
	default:
		return false
	}
}
