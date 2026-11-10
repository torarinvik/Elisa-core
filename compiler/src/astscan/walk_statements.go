// Package astscan provides pointer-deduplicated traversal of syntax trees.
package astscan

import (
	"reflect"

	"elisacore/src/ast"
)

// WalkStatements traverses a statement list using the caller's visited set.
func WalkStatements(stmts []ast.Stmt, visit func(any), seen map[uintptr]bool) {
	for _, stmt := range stmts {
		if stmt != nil {
			astScanWalkIface_ast_Stmt(stmt, visit, seen)
		}
	}
}

// WalkReflect traverses fallback syntax shapes that the generator cannot name.
func WalkReflect(v reflect.Value, visit func(any), seen map[uintptr]bool) {
	if !v.IsValid() || !v.CanInterface() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		visit(v.Interface())
		WalkReflect(v.Elem(), visit, seen)
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		WalkReflect(v.Elem(), visit, seen)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			WalkReflect(v.Field(i), visit, seen)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			WalkReflect(v.Index(i), visit, seen)
		}
	}
}
