package astscan

import (
	"elisacore/src/ast"
	"elisacore/src/aststatementscan"
	"reflect"
)

func Walk(v any, visit func(any), seen map[uintptr]bool) bool {
	return aststatementscan.Walk(v, visit, seen)
}
func WalkStatements(stmts []ast.Stmt, visit func(any), seen map[uintptr]bool) {
	aststatementscan.WalkStatements(stmts, visit, seen)
}
func WalkReflect(v reflect.Value, visit func(any), seen map[uintptr]bool) {
	aststatementscan.WalkReflect(v, visit, seen)
}
