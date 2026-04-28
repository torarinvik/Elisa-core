package semantic

import (
	"strings"

	"llcontext/src/ast"
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

func BaseTreeStoreType(t Type) (*TreeStoreType, bool) {
	storeType, ok := t.(*TreeStoreType)
	if !ok || storeType == nil {
		return nil, false
	}
	if storeType.Family != nil && storeType.Family.StoreType != nil {
		return storeType.Family.StoreType, true
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

func TreeStoreWithState(storeType *TreeStoreType, state Type) *TreeStoreType {
	if base, ok := BaseTreeStoreType(storeType); ok && base != nil {
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

func TreeStoreState(t Type) Type {
	storeType, ok := t.(*TreeStoreType)
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

func TreeStoreStateName(t Type) string {
	state := TreeStoreState(t)
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

func IsLocalTreeStoreType(t Type) bool {
	return TreeStoreStateName(t) == "Local"
}

func PermissionRefString(ref ast.PermissionRef) string {
	if ref.Member != "" {
		return ref.Name + "." + ref.Member
	}
	return ref.Name
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
