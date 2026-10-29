package semantic

import (
	"elisacore/src/ast"
	"maps"
	"reflect"
	"sync"
)

// A reflective slice clone preserves its backing and limits capacity; it does
// not descend into the slice's map elements. Preserve that ownership policy.
var specializationParamStoreTargetsSchema = sync.OnceValue(func() bool {
	field, ok := reflect.TypeFor[Analyzer]().FieldByName("paramStoreTargets")
	return ok && field.Type == reflect.TypeFor[map[*ast.FuncDecl][]map[int]bool]()
})

func cloneSpecializationParamStoreTargets(src map[*ast.FuncDecl][]map[int]bool) map[*ast.FuncDecl][]map[int]bool {
	return cloneSpecializationParamStoreTargetsGuarded(src, specializationParamStoreTargetsSchema())
}

func cloneSpecializationParamStoreTargetsGuarded(src map[*ast.FuncDecl][]map[int]bool, allowed bool) map[*ast.FuncDecl][]map[int]bool {
	if !allowed {
		return deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl][]map[int]bool)
	}
	if src == nil {
		return nil
	}
	dst := maps.Clone(src)
	for decl, row := range src {
		if cap(row) != len(row) {
			dst[decl] = row[:len(row):len(row)]
		}
	}
	return dst
}
