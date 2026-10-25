package semantic

import (
	"reflect"
	"sync"

	"elisacore/src/ast"
)

// Canonical membership scanning alone may skip values whose complete static
// closure cannot carry syntax or borrowed identities. Private publication must
// still validate every value, including AST-free resource/schema violations.
var canonicalMembershipTypeCache sync.Map // reflect.Type -> bool

func canonicalMembershipMayContainIdentity(typ reflect.Type) bool {
	if typ == nil {
		return true
	}
	if cached, ok := canonicalMembershipTypeCache.Load(typ); ok {
		return cached.(bool)
	}
	relevant := canonicalMembershipTypeContainsIdentity(typ, map[reflect.Type]bool{})
	canonicalMembershipTypeCache.Store(typ, relevant)
	return relevant
}

func canonicalMembershipTypeContainsIdentity(typ reflect.Type, active map[reflect.Type]bool) bool {
	if typ == nil {
		return true
	}
	// Interfaces are open: their runtime values may carry syntax or identities.
	// Keep nominal Type dispatch in the original walker, including its deliberate
	// leaf treatment, rather than making this classifier another type authority.
	if typ.Kind() == reflect.Interface || typ.Implements(reflect.TypeFor[ast.Node]()) || typ.Implements(reflect.TypeFor[Type]()) || typ == reflect.TypeFor[*Symbol]() || typ == reflect.TypeFor[*Scope]() {
		return true
	}
	if active[typ] {
		return true
	} // Recursive/unknown closure: use the full walk.
	active[typ] = true
	defer delete(active, typ)
	switch typ.Kind() {
	case reflect.Pointer, reflect.Array, reflect.Slice:
		return canonicalMembershipTypeContainsIdentity(typ.Elem(), active)
	case reflect.Map:
		return canonicalMembershipTypeContainsIdentity(typ.Key(), active) || canonicalMembershipTypeContainsIdentity(typ.Elem(), active)
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			if canonicalMembershipTypeContainsIdentity(typ.Field(i).Type, active) {
				return true
			}
		}
		return false
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.String:
		return false
	default:
		// Unsupported kinds receive the existing value walk, never a new exemption.
		return true
	}
}
