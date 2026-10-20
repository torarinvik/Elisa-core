package semantic

import (
	"reflect"
)

// walkPruned visits every non-nil pointer reachable from root through exported
// struct fields, slices, arrays and interfaces (not map values), parents before
// children, descending into a node only when visit returns true. Shared subtrees
// are visited once per path, as the reflective predicate walks it replaces did.
func walkPruned(root any, visit func(any) bool) {
	if root == nil || pruneWalkTyped(root, visit, nil) {
		return
	}
	pruneWalkReflect(reflect.ValueOf(root), visit, nil)
}

// pruneWalkReflect is the reflective specification of walkPruned.
func pruneWalkReflect(v reflect.Value, visit func(any) bool, seen map[uintptr]bool) {
	if !v.IsValid() || !v.CanInterface() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || !visit(v.Interface()) {
			return
		}
		pruneWalkReflect(v.Elem(), visit, seen)
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		pruneWalkReflect(v.Elem(), visit, seen)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			pruneWalkReflect(v.Field(i), visit, seen)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			pruneWalkReflect(v.Index(i), visit, seen)
		}
	}
}
