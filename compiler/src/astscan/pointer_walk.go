package astscan

import (
	"elisacore/src/astpointerscan"
	"reflect"
)

func WalkPointers(v any, visit func(any), seen map[uintptr]bool) bool {
	return astpointerscan.WalkPointers(v, visit, seen)
}
func WalkPointersReflect(v reflect.Value, visit func(any), seen map[uintptr]bool) {
	astpointerscan.WalkPointersReflect(v, visit, seen)
}
