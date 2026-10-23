package statecache

import (
	"fmt"
	"reflect"
	"sort"
)

type allocation struct {
	start, end uintptr
	typ        reflect.Type
}

// The initial codec supports whole shared objects. Refuse interior pointers and
// partially overlapping slices rather than silently materialize independent
// storage. A compiler cache miss is preferable to an incorrect restored graph.
func (e *encoder) validateAliases() error {
	sort.Slice(e.allocations, func(i, j int) bool { return e.allocations[i].start < e.allocations[j].start })
	for i := 1; i < len(e.allocations); i++ {
		a, b := e.allocations[i-1], e.allocations[i]
		if a.end > b.start {
			return fmt.Errorf("overlapping state allocations %s and %s need an alias adapter", a.typ, b.typ)
		}
	}
	return nil
}
func (e *encoder) recordAllocation(v reflect.Value) {
	var size uintptr
	switch v.Kind() {
	case reflect.Pointer:
		size = v.Type().Elem().Size()
	case reflect.Slice:
		size = uintptr(v.Len()) * v.Type().Elem().Size()
	default:
		return
	}
	if size == 0 {
		return
	}
	start := v.Pointer()
	e.allocations = append(e.allocations, allocation{start, start + size, v.Type()})
}
