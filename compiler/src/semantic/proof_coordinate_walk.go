package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
)

// Exact reflective policy retained for unknown dynamic AST and inaccessible
// fields. Typed capture never assumes those subtrees are empty or canonical.
func proofCoordinateWalkReflect(v reflect.Value, add func(lexer.Pos), seen map[uintptr]bool) {
	if !v.IsValid() {
		return
	}
	if v.Type() == retainedPosType {
		// Some producers intentionally use only a token start. Preserve that
		// contract; never manufacture an end span during rendering.
		add(v.Interface().(lexer.Pos))
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			proofCoordinateWalkReflect(v.Elem(), add, seen)
		}
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		if v.CanInterface() {
			if node, ok := v.Interface().(ast.Node); ok {
				add(node.Pos())
			}
		}
		proofCoordinateWalkReflect(v.Elem(), add, seen)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			proofCoordinateWalkReflect(v.Field(i), add, seen)
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := 0; i < v.Len(); i++ {
			proofCoordinateWalkReflect(v.Index(i), add, seen)
		}
	}
}
