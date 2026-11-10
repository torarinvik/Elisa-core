package astscan

import "reflect"

// A generated AST walk delegates unknown interfaces, maps, and inaccessible
// fields back through this marker. The original membership walker therefore
// keeps its complete semantic metadata policy and map/pointer identity guards.
func WalkCanonicalOpaque(value reflect.Value, visit func(any) bool, _ map[uintptr]bool) {
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Map, reflect.Slice, reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return
		}
	}
	// Avoid boxing a reflect.Value for absent optional AST fields. Keep
	// nonnil empty maps: the complete walk still records their map identity.
	visit(value)
}
