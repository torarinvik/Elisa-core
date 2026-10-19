package parser

import (
	"reflect"
	"sync"
)

// These immutable plans describe Go types, never AST instances or scan results.
// Auto-region visitors inspect pointers/interfaces; scalar-only fields (notably
// source positions and names) cannot contribute a match and need no reflection.
var autoRegionFieldPlans sync.Map // reflect.Type -> []int

func autoRegionChildFields(t reflect.Type) []int {
	if fields, ok := autoRegionFieldPlans.Load(t); ok {
		return fields.([]int)
	}
	var fields []int
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath == "" && autoRegionTypeMayContainNode(field.Type) {
			fields = append(fields, i)
		}
	}
	actual, _ := autoRegionFieldPlans.LoadOrStore(t, fields)
	return actual.([]int)
}

func autoRegionTypeMayContainNode(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface:
		return true
	case reflect.Slice, reflect.Array:
		return autoRegionTypeMayContainNode(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath == "" && autoRegionTypeMayContainNode(field.Type) {
				return true
			}
		}
	}
	// The existing visitors do not traverse maps, channels, functions or scalars.
	return false
}
