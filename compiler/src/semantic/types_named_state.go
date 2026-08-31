package semantic

import (
	"elisacore/src/ast"
)

func namedStateStructBase(t Type) (*StructType, bool) {
	// A named-state value is commonly carried through a borrow (`App&` or
	// `mutable App&`).  State predicates inspect the value's logical struct,
	// not the transport reference, so peel the reference before matching.  Do
	// this here rather than making every `is`/flow caller know about references.
	for {
		ref, ok := t.(*RefType)
		if !ok || ref == nil {
			break
		}
		t = ref.Elem
	}
	switch tt := t.(type) {
	case *StructType:
		return tt, tt != nil && len(tt.NamedStateCases) != 0
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		return base, ok && base != nil && len(base.NamedStateCases) != 0
	default:
		return nil, false
	}
}

func namedStateArgs(t Type) ([]Type, bool) {
	for {
		ref, ok := t.(*RefType)
		if !ok || ref == nil {
			break
		}
		t = ref.Elem
	}
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil {
		return nil, false
	}
	base, ok := gi.Base.(*StructType)
	if !ok || base == nil || len(base.NamedStateCases) == 0 {
		return nil, false
	}
	return gi.Args, true
}

func namedStateArgIndex(base *StructType) int {
	if base == nil {
		return -1
	}
	params := genericParamsForStructType(base)
	for i, param := range params {
		if param.Kind == ast.GenericParamState {
			return i
		}
	}
	return -1
}

func namedStateCurrentArg(t Type) (Type, bool) {
	base, ok := namedStateStructBase(t)
	if !ok || base == nil {
		return nil, false
	}
	if args, ok := namedStateArgs(t); ok {
		idx := namedStateArgIndex(base)
		if idx >= 0 && idx < len(args) {
			return args[idx], true
		}
	}
	return fullNamedStateType(base), true
}

func namedStateTypeCases(t Type) ([]string, string, bool) {
	switch tt := t.(type) {
	case *StructStateCaseType:
		if tt == nil {
			return nil, "", false
		}
		return []string{tt.Case}, tt.StructName, true
	case *StructStateSetType:
		if tt == nil {
			return nil, "", false
		}
		cases := append([]string(nil), tt.Cases...)
		return cases, tt.StructName, true
	default:
		return nil, "", false
	}
}

func canonicalizeNamedStateCases(allowed []string, cases []string) []string {
	if len(allowed) == 0 || len(cases) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(cases))
	for _, name := range cases {
		seen[name] = true
	}
	ordered := make([]string, 0, len(seen))
	for _, name := range allowed {
		if seen[name] {
			ordered = append(ordered, name)
		}
	}
	return ordered
}

func newNamedStateType(structName string, allowed []string, cases []string) Type {
	ordered := canonicalizeNamedStateCases(allowed, cases)
	if len(ordered) == 0 {
		return nil
	}
	if len(ordered) == 1 {
		return &StructStateCaseType{StructName: structName, Case: ordered[0]}
	}
	return &StructStateSetType{StructName: structName, Cases: ordered}
}

func NewNamedStateTypeForBackend(structName string, allowed []string, cases []string) Type {
	return newNamedStateType(structName, allowed, cases)
}

func fullNamedStateType(base *StructType) Type {
	if base == nil || len(base.NamedStateCases) == 0 {
		return nil
	}
	return newNamedStateType(base.Name, base.NamedStateCases, base.NamedStateCases)
}

func sameNamedStateType(a Type, b Type) bool {
	acases, astruct, aok := namedStateTypeCases(a)
	bcases, bstruct, bok := namedStateTypeCases(b)
	if !aok || !bok || astruct != bstruct || len(acases) != len(bcases) {
		return false
	}
	for i := range acases {
		if acases[i] != bcases[i] {
			return false
		}
	}
	return true
}

func namedStateTypeAssignable(dst Type, src Type) bool {
	dstCases, dstStruct, dstOK := namedStateTypeCases(dst)
	srcCases, srcStruct, srcOK := namedStateTypeCases(src)
	if !dstOK || !srcOK || dstStruct != srcStruct {
		return false
	}
	allowed := make(map[string]bool, len(dstCases))
	for _, name := range dstCases {
		allowed[name] = true
	}
	for _, name := range srcCases {
		if !allowed[name] {
			return false
		}
	}
	return true
}

func mergeNamedStateTypes(a Type, b Type, allowed []string) Type {
	acases, astruct, aok := namedStateTypeCases(a)
	bcases, bstruct, bok := namedStateTypeCases(b)
	if !aok || !bok || astruct != bstruct {
		return invalidType
	}
	if len(allowed) == 0 {
		allowed = append([]string(nil), acases...)
		for _, name := range bcases {
			seen := false
			for _, existing := range allowed {
				if existing == name {
					seen = true
					break
				}
			}
			if !seen {
				allowed = append(allowed, name)
			}
		}
	}
	merged := make([]string, 0, len(acases)+len(bcases))
	seen := map[string]bool{}
	for _, name := range acases {
		if !seen[name] {
			seen[name] = true
			merged = append(merged, name)
		}
	}
	for _, name := range bcases {
		if !seen[name] {
			seen[name] = true
			merged = append(merged, name)
		}
	}
	return newNamedStateType(astruct, allowed, merged)
}

func subtractNamedStateType(current Type, remove Type, allowed []string) Type {
	currentCases, structName, currentOK := namedStateTypeCases(current)
	removeCases, removeStruct, removeOK := namedStateTypeCases(remove)
	if !currentOK || !removeOK || structName != removeStruct {
		return nil
	}
	removed := make(map[string]bool, len(removeCases))
	for _, name := range removeCases {
		removed[name] = true
	}
	remaining := make([]string, 0, len(currentCases))
	for _, name := range currentCases {
		if !removed[name] {
			remaining = append(remaining, name)
		}
	}
	return newNamedStateType(structName, allowed, remaining)
}

func intersectNamedStateType(a Type, b Type, allowed []string) Type {
	acases, structName, aOK := namedStateTypeCases(a)
	bcases, bStruct, bOK := namedStateTypeCases(b)
	if !aOK || !bOK || structName != bStruct {
		return nil
	}
	allowedSet := make(map[string]bool, len(bcases))
	for _, name := range bcases {
		allowedSet[name] = true
	}
	intersection := make([]string, 0, len(acases))
	for _, name := range acases {
		if allowedSet[name] {
			intersection = append(intersection, name)
		}
	}
	return newNamedStateType(structName, allowed, intersection)
}
