package backend

import (
	"reflect"
	"sync"
)

// The reflective AST walkers in this package act only on pointer and interface
// values. astWalkFields lists, per struct type, the exported fields that can
// reach one, so a walk skips positions, strings and other pointer-free data.

var astWalkFieldCache sync.Map    // reflect.Type -> []int
var astMayHoldNodesCache sync.Map // reflect.Type -> bool

func astWalkFields(t reflect.Type) []int {
	if cached, ok := astWalkFieldCache.Load(t); ok {
		return cached.([]int)
	}
	var fields []int
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.IsExported() && astTypeMayHoldNodes(f.Type) {
			fields = append(fields, i)
		}
	}
	astWalkFieldCache.Store(t, fields)
	return fields
}

// astTypeMayHoldNodes reports whether a value of type t can contain a pointer
// or interface value a walker would visit.
func astTypeMayHoldNodes(t reflect.Type) bool {
	if cached, ok := astMayHoldNodesCache.Load(t); ok {
		return cached.(bool)
	}
	var holds bool
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface:
		holds = true
	case reflect.Slice, reflect.Array:
		holds = astTypeMayHoldNodes(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if f := t.Field(i); f.IsExported() && astTypeMayHoldNodes(f.Type) {
				holds = true
				break
			}
		}
	}
	astMayHoldNodesCache.Store(t, holds)
	return holds
}
