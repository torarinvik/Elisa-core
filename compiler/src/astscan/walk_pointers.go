package astscan

import (
	"reflect"
	"sync"
)

// WalkPointersReflect traverses unknown syntax shapes through exported fields,
// including map values, while deduplicating pointers in the caller's set.
func WalkPointersReflect(v reflect.Value, visit func(any), seen map[uintptr]bool) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || !v.CanInterface() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		visit(v.Interface())
		WalkPointersReflect(v.Elem(), visit, seen)
	case reflect.Interface:
		if !v.IsNil() {
			WalkPointersReflect(v.Elem(), visit, seen)
		}
	case reflect.Struct:
		for _, i := range pointerWalkExportedFields(v.Type()) {
			WalkPointersReflect(v.Field(i), visit, seen)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			WalkPointersReflect(v.Index(i), visit, seen)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			WalkPointersReflect(iter.Value(), visit, seen)
		}
	}
}

var pointerWalkFieldCache sync.Map

func pointerWalkExportedFields(typ reflect.Type) []int {
	if cached, ok := pointerWalkFieldCache.Load(typ); ok {
		return cached.([]int)
	}
	var indexes []int
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			indexes = append(indexes, i)
		}
	}
	cached, _ := pointerWalkFieldCache.LoadOrStore(typ, indexes)
	return cached.([]int)
}
