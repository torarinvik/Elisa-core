package semantic

import (
	"elisacore/src/ast"
	"maps"
	"reflect"
	"sync"
)

// This path exactly mirrors reflection's independent copies of the two maps;
// additions to the flow schema must fall back to the reflection clone.
var specializationReturnBorrowSchema = sync.OnceValue(func() bool {
	field, ok := reflect.TypeFor[Analyzer]().FieldByName("returnBorrowFuncSummaries")
	return ok && field.Type == reflect.TypeFor[map[*ast.FuncDecl]returnBorrowFlow]() &&
		specializationCloneSchemaMatches(reflect.TypeFor[returnBorrowFlow](), []specializationCloneField{
			{"Params", reflect.TypeFor[map[int]bool]()},
			{"Local", reflect.TypeFor[bool]()},
			{"Contents", reflect.TypeFor[map[int]bool]()},
		})
})

func cloneSpecializationReturnBorrowSummaries(src map[*ast.FuncDecl]returnBorrowFlow) map[*ast.FuncDecl]returnBorrowFlow {
	return cloneSpecializationReturnBorrowSummariesGuarded(src, specializationReturnBorrowSchema())
}
func cloneSpecializationReturnBorrowSummariesGuarded(src map[*ast.FuncDecl]returnBorrowFlow, allowed bool) map[*ast.FuncDecl]returnBorrowFlow {
	if !allowed {
		return deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl]returnBorrowFlow)
	}
	if src == nil {
		return nil
	}
	dst := make(map[*ast.FuncDecl]returnBorrowFlow, len(src))
	for decl, flow := range src {
		flow.Params = maps.Clone(flow.Params)
		flow.Contents = maps.Clone(flow.Contents)
		dst[decl] = flow
	}
	return dst
}
