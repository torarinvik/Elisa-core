package semantic

import (
	"elisacore/src/ast"
	"maps"
	"reflect"
	"sync"
)

// Mutable nested sets need independent rows, including when source entries share a row.
// The two completed per-function name caches have a specialized read-only clone path below.
var specializationNestedSetSchemas = sync.OnceValue(func() map[string]bool {
	expected := map[string]reflect.Type{
		"reassignedParamCache":    reflect.TypeFor[map[*ast.FuncDecl]map[string]bool](),
		"privateFreshDArrayCache": reflect.TypeFor[map[*ast.FuncDecl]map[string]bool](),
		"disjointDependents":      reflect.TypeFor[map[*ast.FuncDecl]map[*ast.FuncDecl]bool](),
		"ambientFillFreshParams":  reflect.TypeFor[map[*ast.FuncDecl]map[int]bool](),
	}
	result := make(map[string]bool, len(expected))
	for name, typ := range expected {
		field, ok := reflect.TypeFor[Analyzer]().FieldByName(name)
		result[name] = ok && field.Type == typ
	}
	return result
})

func cloneSpecializationNestedSet[K comparable](src map[*ast.FuncDecl]map[K]bool, allowed bool) map[*ast.FuncDecl]map[K]bool {
	if !allowed {
		return deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl]map[K]bool)
	}
	if src == nil {
		return nil
	}
	dst := make(map[*ast.FuncDecl]map[K]bool, len(src))
	for key, row := range src {
		dst[key] = maps.Clone(row)
	}
	return dst
}

func cloneSpecializationNestedSetField(dst, src *Analyzer, name string) bool {
	switch name {
	case "reassignedParamCache":
		if specializationNestedSetSchemas()[name] {
			// Each row is completed before publication and only read afterward.
			// Copy the mutable index while sharing those immutable name sets.
			dst.reassignedParamCache = maps.Clone(src.reassignedParamCache)
		} else {
			dst.reassignedParamCache = cloneSpecializationNestedSet(src.reassignedParamCache, false)
		}
	case "privateFreshDArrayCache":
		if specializationNestedSetSchemas()[name] {
			// As above, entries are complete read-only sets once cached.
			dst.privateFreshDArrayCache = maps.Clone(src.privateFreshDArrayCache)
		} else {
			dst.privateFreshDArrayCache = cloneSpecializationNestedSet(src.privateFreshDArrayCache, false)
		}
	case "disjointDependents":
		dst.disjointDependents = cloneSpecializationNestedSet(src.disjointDependents, specializationNestedSetSchemas()[name])
	case "ambientFillFreshParams":
		dst.ambientFillFreshParams = cloneSpecializationNestedSet(src.ambientFillFreshParams, specializationNestedSetSchemas()[name])
	default:
		return false
	}
	return true
}
