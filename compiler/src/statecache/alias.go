package statecache

import (
	"fmt"
	"reflect"
	"sort"
)

type pathStep struct {
	index   int
	element bool
}
type nodeAlias struct {
	owner  uint64
	path   []pathStep
	offset int
}
type stateNode struct {
	value reflect.Value
	alias *nodeAlias
}
type allocation struct {
	start, end uintptr
	id         uint64
}

func allocationSize(v reflect.Value) uintptr {
	var size uintptr
	switch v.Kind() {
	case reflect.Pointer:
		size = v.Type().Elem().Size()
	case reflect.Slice:
		size = uintptr(v.Cap()) * v.Type().Elem().Size()
	}
	return size
}

func (e *encoder) recordAllocation(v reflect.Value, id uint64) {
	size := allocationSize(v)
	if size == 0 {
		return
	}
	start := v.Pointer()
	e.allocations = append(e.allocations, allocation{start, start + size, id})
}

// Bind aliases only through real typed fields and array/slice elements. Neither
// addresses nor arbitrary byte offsets enter the wire format. A two-phase
// decoder allocates owners before resolving these references and filling values.
func (e *encoder) bindAliases() error {
	// Inventory has discovered every object. Build this table at its final
	// capacity instead of repeatedly copying a growing multi-million-entry
	// slice while walking the graph. Node order (and therefore IDs) is unchanged.
	e.allocations = make([]allocation, 0, e.allocationCount)
	for i := 0; i < e.nodes.count; i++ {
		node := e.nodes.at(i)
		e.recordAllocation(node.value, uint64(i+1))
	}

	sort.Slice(e.allocations, func(i, j int) bool {
		a, b := e.allocations[i], e.allocations[j]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.end != b.end {
			return a.end > b.end
		}
		// Prefer a backing slice to a pointer to its only element.
		return e.nodes.at(int(a.id-1)).value.Kind() == reflect.Slice && e.nodes.at(int(b.id-1)).value.Kind() != reflect.Slice
	})
	var active []allocation
	for _, current := range e.allocations {
		kept := active[:0]
		for _, owner := range active {
			if owner.end > current.start {
				kept = append(kept, owner)
			}
		}
		active = kept
		bound := false
		for _, owner := range active {
			if owner.end < current.end {
				return fmt.Errorf("partially overlapping state allocations need a backing-store adapter")
			}
			if alias, ok := e.deriveAlias(owner, current); ok {
				e.nodes.at(int(current.id - 1)).alias = alias
				bound = true
				break
			}
			if owner.start == current.start && owner.end == current.end && e.nodes.at(int(owner.id-1)).alias == nil {
				if alias, ok := e.deriveAlias(current, owner); ok {
					e.nodes.at(int(owner.id - 1)).alias = alias
					bound = true
					break
				}
			}
		}
		if len(active) > 0 && !bound {
			return fmt.Errorf("unsupported typed state alias %s", e.nodes.at(int(current.id-1)).value.Type())
		}
		active = append(active, current)
	}
	return nil
}

func (e *encoder) deriveAlias(owner, target allocation) (*nodeAlias, bool) {
	parent := e.nodes.at(int(owner.id - 1)).value
	child := e.nodes.at(int(target.id - 1)).value
	var base reflect.Value
	if parent.Kind() == reflect.Pointer {
		base = parent.Elem()
	} else {
		base = parent.Slice(0, parent.Cap())
	}
	if child.Kind() == reflect.Pointer {
		path, ok := e.findSubobject(base, child.Pointer(), child.Type().Elem())
		if ok {
			return &nodeAlias{owner: owner.id, path: path}, true
		}
	} else if child.Kind() == reflect.Slice {
		path, offset, ok := e.findSequence(base, child.Pointer(), child.Cap(), child.Type().Elem())
		if ok {
			return &nodeAlias{owner: owner.id, path: path, offset: offset}, true
		}
	}
	return nil, false
}

func (e *encoder) findSubobject(v reflect.Value, address uintptr, target reflect.Type) ([]pathStep, bool) {
	if v.Type() == target && v.CanAddr() && v.Addr().Pointer() == address {
		return nil, true
	}
	switch v.Kind() {
	case reflect.Struct:
		for _, i := range e.registry.fields[v.Type()] {
			field := e.registry.access(v, i)
			if !field.CanAddr() || address < field.UnsafeAddr() || address >= field.UnsafeAddr()+field.Type().Size() {
				continue
			}
			if path, ok := e.findSubobject(field, address, target); ok {
				return append([]pathStep{{index: i}}, path...), true
			}
		}
	case reflect.Array, reflect.Slice:
		size := v.Type().Elem().Size()
		if size == 0 || v.Len() == 0 {
			return nil, false
		}
		start := v.Index(0).Addr().Pointer()
		if address < start || address >= start+uintptr(v.Len())*size {
			return nil, false
		}
		i := int((address - start) / size)
		if path, ok := e.findSubobject(v.Index(i), address, target); ok {
			return append([]pathStep{{index: i, element: true}}, path...), true
		}
	}
	return nil, false
}

func (e *encoder) findSequence(v reflect.Value, address uintptr, capacity int, element reflect.Type) ([]pathStep, int, bool) {
	if (v.Kind() == reflect.Array || v.Kind() == reflect.Slice) && v.Type().Elem() == element && v.Len() > 0 && element.Size() > 0 {
		start := v.Index(0).Addr().Pointer()
		size := element.Size()
		if address >= start && (address-start)%size == 0 {
			offset := (address - start) / size
			if offset <= uintptr(v.Len()) && uintptr(capacity) <= uintptr(v.Len())-offset {
				return nil, int(offset), true
			}
		}
	}
	switch v.Kind() {
	case reflect.Struct:
		for _, i := range e.registry.fields[v.Type()] {
			field := e.registry.access(v, i)
			if !field.CanAddr() || address < field.UnsafeAddr() || address >= field.UnsafeAddr()+field.Type().Size() {
				continue
			}
			if path, offset, ok := e.findSequence(field, address, capacity, element); ok {
				return append([]pathStep{{index: i}}, path...), offset, true
			}
		}
	case reflect.Array, reflect.Slice:
		size := v.Type().Elem().Size()
		if size == 0 || v.Len() == 0 {
			return nil, 0, false
		}
		start := v.Index(0).Addr().Pointer()
		if address >= start && address < start+uintptr(v.Len())*size {
			i := int((address - start) / size)
			if path, offset, ok := e.findSequence(v.Index(i), address, capacity, element); ok {
				return append([]pathStep{{index: i, element: true}}, path...), offset, true
			}
		}
	}
	return nil, 0, false
}
