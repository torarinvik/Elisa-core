package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"sync"
)

// Clone the outer map while preserving the reflective clone's restricted slice
// capacities and shared backing arrays. A caller cannot append into the source.
var specializationGraphCacheSchemas = sync.OnceValue(func() map[string]bool {
	result := make(map[string]bool, 2)
	for _, name := range []string{"storeFlowSCC", "callEdgeCache"} {
		field, ok := reflect.TypeFor[Analyzer]().FieldByName(name)
		result[name] = ok && field.Type == reflect.TypeFor[map[*ast.FuncDecl][]*ast.FuncDecl]()
	}
	return result
})

func specializationGraphCacheSchema(name string) bool {
	return specializationGraphCacheSchemas()[name]
}

func cloneSpecializationFuncDeclSliceMap(src map[*ast.FuncDecl][]*ast.FuncDecl, allowed bool) map[*ast.FuncDecl][]*ast.FuncDecl {
	if !allowed {
		return deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl][]*ast.FuncDecl)
	}
	if src == nil {
		return nil
	}
	dst := make(map[*ast.FuncDecl][]*ast.FuncDecl, len(src))
	for decl, row := range src {
		if row != nil {
			row = row[:len(row):len(row)]
		}
		dst[decl] = row
	}
	return dst
}
