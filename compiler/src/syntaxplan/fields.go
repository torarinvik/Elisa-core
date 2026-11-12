package syntaxplan

import (
	"reflect"
	"sync"

	"elisacore/src/lexer"
)

var syntaxWalkFieldPlans sync.Map // reflect.Type -> immutable []int
var syntaxWalkPositionType = reflect.TypeOf(lexer.Pos{})

// Fields serves walkers whose callbacks inspect pointers, interfaces
// or non-map composite containers, never scalar values or lexer.Pos. These
// callers do not traverse maps. Keep their other composite fields, including
// future AST fields and lowered expression links.
// Source-position records and scalars cannot lead to a syntax callback.
func Fields(typ reflect.Type) []int {
	if cached, ok := syntaxWalkFieldPlans.Load(typ); ok {
		return cached.([]int)
	}
	fields := make([]int, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if mayReachNode(field.Type) {
			fields = append(fields, i)
		}
	}
	cached, _ := syntaxWalkFieldPlans.LoadOrStore(typ, fields)
	return cached.([]int)
}

// Scalar-only composites such as call argument names have no syntax to visit.
func mayReachNode(typ reflect.Type) bool {
	if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = typ.Elem()
		if typ.Kind() == reflect.Struct {
			return typ != syntaxWalkPositionType
		}
	}
	switch typ.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Array:
		return true
	case reflect.Struct:
		return typ != syntaxWalkPositionType && len(Fields(typ)) != 0
	default:
		return false
	}
}
