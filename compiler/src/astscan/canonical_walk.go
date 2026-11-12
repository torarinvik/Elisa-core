package astscan

import (
	"elisacore/src/astcanonicalscan"
	"reflect"
)

func WalkCanonical(v any, visit func(any) bool, seen map[uintptr]bool) bool {
	return astcanonicalscan.WalkCanonical(v, visit, seen)
}
func WalkCanonicalOpaque(v reflect.Value, visit func(any) bool, seen map[uintptr]bool) {
	astcanonicalscan.WalkCanonicalOpaque(v, visit, seen)
}
