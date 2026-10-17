package semantic

import (
	"elisacore/src/lexer"
	"reflect"
	"sync"
)

var syntaxWalkFieldPlans sync.Map // reflect.Type -> immutable []int
var syntaxWalkPositionType = reflect.TypeOf(lexer.Pos{})

// syntaxWalkFields serves walkers whose callbacks inspect pointers, interfaces
// or non-map composite containers, never scalar values or lexer.Pos. These
// callers do not traverse maps. Keep their other composite fields, including
// future AST fields and lowered expression links.
// Source-position records and scalars cannot lead to a syntax callback.
func syntaxWalkFields(typ reflect.Type) []int {
	if cached, ok := syntaxWalkFieldPlans.Load(typ); ok {
		return cached.([]int)
	}
	fields := make([]int, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Type == syntaxWalkPositionType {
			continue
		}
		switch field.Type.Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Struct, reflect.Slice, reflect.Array:
			fields = append(fields, i)
		}
	}
	cached, _ := syntaxWalkFieldPlans.LoadOrStore(typ, fields)
	return cached.([]int)
}
