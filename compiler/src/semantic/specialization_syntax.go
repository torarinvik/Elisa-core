package semantic

import (
	"fmt"
	"reflect"
	"sort"

	"elisacore/src/ast"
)

// A specialization owns its syntax, including resolved-argument slice backing.
// Same-start views retain lengths and shared observable elements. Spare capacity
// is clamped to the union of visible views, so stale tails cannot retain syntax.
// More general
// overlapping backing is rejected: silently breaking such aliases is unsound.
type specializationPointer struct {
	typ reflect.Type
	ptr uintptr
}
type specializationSlice struct {
	typ             reflect.Type
	start           uintptr
	capacity        int
	length          int
	source, copy    reflect.Value
	copying, copied bool
}
type specializationSyntax struct {
	pointers  map[specializationPointer]reflect.Value
	slices    map[specializationPointer]*specializationSlice
	nodes     map[ast.Node]ast.Node
	originals map[ast.Node]bool
	prepared  []ast.Node
	scanned   map[specializationPointer]bool
	err       error
}

func newSpecializationSyntax() *specializationSyntax {
	return &specializationSyntax{pointers: map[specializationPointer]reflect.Value{}, slices: map[specializationPointer]*specializationSlice{}, nodes: map[ast.Node]ast.Node{}, originals: map[ast.Node]bool{}, scanned: map[specializationPointer]bool{}}
}

func (s *specializationSyntax) prepare(v reflect.Value) {
	if !v.IsValid() || s.err != nil {
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			s.prepare(v.Elem())
		}
	case reflect.Pointer:
		if v.IsNil() || v.Type().Elem().PkgPath() != astGraphPkgPath {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if s.scanned[key] {
			return
		}
		s.scanned[key] = true
		if node, ok := v.Interface().(ast.Node); ok {
			s.originals[node] = true
			s.prepared = append(s.prepared, node)
		}
		s.prepare(v.Elem())
	case reflect.Struct:
		if v.Type().PkgPath() != astGraphPkgPath {
			return
		}
		for _, i := range astCopyTraversalFields(v.Type()) {
			s.prepare(v.Field(i))
		}
	case reflect.Slice:
		if v.IsNil() || v.Cap() == 0 {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		old := s.slices[key]
		if old != nil && old.capacity >= v.Cap() && old.length >= v.Len() {
			return
		}
		from := 0
		if old != nil {
			if old.copy.IsValid() {
				s.err = fmt.Errorf("specialization syntax introduces a larger aliased slice after publication")
				return
			}
			from = old.length
			if v.Cap() > old.capacity {
				old.capacity = v.Cap()
				old.source = v.Slice(0, v.Cap())
			}
			if v.Len() > old.length {
				old.length = v.Len()
			}
		} else {
			old = &specializationSlice{typ: v.Type(), start: v.Pointer(), capacity: v.Cap(), length: v.Len(), source: v.Slice(0, v.Cap())}
			s.slices[key] = old
		}
		if astCopyNeedsTraversal(v.Type().Elem()) {
			for i := from; i < old.length; i++ {
				element := old.source.Index(i)
				if element.Kind() == reflect.Struct && element.Type().PkgPath() == astGraphPkgPath && element.CanAddr() {
					s.prepare(element.Addr())
				} else {
					s.prepare(element)
				}
			}
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if s.scanned[key] {
			return
		}
		s.scanned[key] = true
		it := v.MapRange()
		for it.Next() {
			s.prepare(it.Key())
			s.prepare(it.Value())
		}
	}
}

func (s *specializationSyntax) validate() error {
	if s.err != nil {
		return s.err
	}
	var ranges []*specializationSlice
	for _, r := range s.slices {
		if r.typ.Elem().Size() != 0 {
			ranges = append(ranges, r)
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	var end uintptr
	for _, r := range ranges {
		if r.start < end {
			return fmt.Errorf("specialization syntax has unsupported partially overlapping slice backing")
		}
		end = r.start + uintptr(r.capacity)*r.typ.Elem().Size()
		// An explicit interior pointer is observable too. A pointer into hidden
		// capacity would require preserving otherwise inaccessible gaps; decline
		// that shape rather than retain stale tail syntax outside the closure.
		for pointer := range s.scanned {
			if pointer.typ.Kind() == reflect.Pointer && pointer.typ.Elem() == r.typ.Elem() && pointer.ptr >= r.start && pointer.ptr < end && pointer.ptr >= r.start+uintptr(r.length)*r.typ.Elem().Size() {
				return fmt.Errorf("specialization syntax has an interior pointer beyond visible slice views")
			}
		}
	}
	return nil
}

func (s *specializationSyntax) copy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || v.Type().Elem().PkgPath() != astGraphPkgPath {
			return v
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if old, ok := s.pointers[key]; ok {
			return old
		}
		// An AST pointer may refer into an AST-struct slice. Bind it to that
		// copied element before filling either root, preserving interior identity.
		var out reflect.Value
		for _, r := range s.slices {
			size := r.typ.Elem().Size()
			if size != 0 && r.typ.Elem() == v.Type().Elem() && v.Pointer() >= r.start && v.Pointer() < r.start+uintptr(r.capacity)*size && (v.Pointer()-r.start)%size == 0 {
				s.allocateSlice(r)
				out = r.copy.Index(int((v.Pointer() - r.start) / size)).Addr()
				break
			}
		}
		if !out.IsValid() {
			out = reflect.New(v.Type().Elem())
		}
		s.pointers[key] = out
		if original, ok := v.Interface().(ast.Node); ok {
			s.nodes[original] = out.Interface().(ast.Node)
		}
		out.Elem().Set(s.copy(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(s.copy(v.Elem()))
		return out
	case reflect.Struct:
		if v.Type().PkgPath() != astGraphPkgPath {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for _, i := range astCopyTraversalFields(v.Type()) {
			settable(out.Field(i)).Set(s.copy(settable(v.Field(i))))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		if v.Cap() == 0 {
			return reflect.MakeSlice(v.Type(), v.Len(), v.Cap())
		}
		r := s.slices[specializationPointer{v.Type(), v.Pointer()}]
		if r == nil {
			panic("specialization syntax was not prepared")
		}
		s.allocateSlice(r)
		if !r.copied && !r.copying {
			r.copying = true
			for i := 0; i < r.length; i++ {
				r.copy.Index(i).Set(s.copy(r.source.Index(i)))
			}
			r.copying = false
			r.copied = true
		}
		capacity := min(v.Cap(), r.length)
		return r.copy.Slice3(0, v.Len(), capacity)
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if old, ok := s.pointers[key]; ok {
			return old
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		s.pointers[key] = out
		it := v.MapRange()
		for it.Next() {
			out.SetMapIndex(s.copy(it.Key()), s.copy(it.Value()))
		}
		return out
	}
	return v
}

func (s *specializationSyntax) allocateSlice(r *specializationSlice) {
	if r.copy.IsValid() {
		return
	}
	r.copy = reflect.MakeSlice(r.typ, r.length, r.length)
	// Register AST elements before the recursive copy follows an interior pointer.
	if r.typ.Elem().Kind() == reflect.Struct && r.typ.Elem().PkgPath() == astGraphPkgPath {
		for i := 0; i < r.length; i++ {
			original := r.source.Index(i).Addr()
			copied := r.copy.Index(i).Addr()
			s.pointers[specializationPointer{original.Type(), original.Pointer()}] = copied
			if node, ok := original.Interface().(ast.Node); ok {
				s.nodes[node] = copied.Interface().(ast.Node)
			}
		}
	}
}
