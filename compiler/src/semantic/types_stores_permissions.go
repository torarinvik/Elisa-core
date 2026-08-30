package semantic

import (
	"strings"

	"elisacore/src/ast"
)

func permissionFamiliesString(families []string) string {
	if len(families) == 0 {
		return ""
	}
	return " can[" + strings.Join(families, ", ") + "]"
}

func BasePackedEnumStoreType(t Type) (*PackedEnumStoreType, bool) {
	storeType, ok := t.(*PackedEnumStoreType)
	if !ok || storeType == nil {
		return nil, false
	}
	if storeType.Enum != nil && storeType.Enum.StoreType != nil {
		return storeType.Enum.StoreType, true
	}
	base := *storeType
	base.State = nil
	return &base, true
}

func PackedEnumStoreWithState(storeType *PackedEnumStoreType, state Type) *PackedEnumStoreType {
	if base, ok := BasePackedEnumStoreType(storeType); ok && base != nil {
		next := *base
		next.State = state
		return &next
	}
	if storeType == nil {
		return nil
	}
	next := *storeType
	next.State = state
	return &next
}

func PackedEnumStoreState(t Type) Type {
	storeType, ok := t.(*PackedEnumStoreType)
	if !ok || storeType == nil {
		return nil
	}
	return storeType.State
}

func PackedEnumStoreStateName(t Type) string {
	state := PackedEnumStoreState(t)
	if state == nil {
		return ""
	}
	if builtin, ok := state.(*BuiltinType); ok {
		return builtin.Name
	}
	return state.String()
}

func IsLocalPackedEnumStoreType(t Type) bool {
	return PackedEnumStoreStateName(t) == "Local"
}

func IsFrozenPackedEnumStoreType(t Type) bool {
	return PackedEnumStoreStateName(t) == "Frozen"
}

func PermissionRefString(ref ast.PermissionRef) string {
	name := ref.Name
	if len(ref.TypeArgs) != 0 {
		parts := make([]string, 0, len(ref.TypeArgs))
		for _, arg := range ref.TypeArgs {
			parts = append(parts, permissionTypeExprString(arg))
		}
		name += "[" + strings.Join(parts, ", ") + "]"
	}
	if ref.Member != "" {
		name += "." + ref.Member
	}
	if len(ref.Via) != 0 {
		via := make([]string, 0, len(ref.Via))
		for _, realization := range ref.Via {
			via = append(via, PermissionRefString(realization))
		}
		name += " via " + strings.Join(via, ", ")
	}
	return name
}

func permissionTypeExprString(typ ast.TypeExpr) string {
	switch n := typ.(type) {
	case *ast.NamedType:
		return n.Name
	case *ast.GenericType:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, permissionTypeExprString(arg))
		}
		return n.Name + "[" + strings.Join(parts, ", ") + "]"
	case *ast.MutableType:
		return "mutable " + permissionTypeExprString(n.Elem)
	case *ast.RefType:
		return permissionTypeExprString(n.Elem) + "&"
	default:
		return "<type>"
	}
}

func PermissionRefsString(refs []ast.PermissionRef) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, PermissionRefString(ref))
	}
	return " can[" + strings.Join(parts, ", ") + "]"
}

var (
	invalidType = &InvalidType{}
	neverType   = &NeverType{}
	nullType    = &NullType{}
)
