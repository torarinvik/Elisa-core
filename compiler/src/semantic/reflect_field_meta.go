package semantic

import (
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Per-type reflection metadata for the AST reflection walkers.
//
// reflect.Type.Field(i) builds and returns a ~100-byte StructField by value on every call (the
// i6 self-host profile: 1.4 s of duffcopy under (*rtype).Field), and FieldByName searches the
// fields by name each time. The walkers ask the same questions of the same few dozen AST types
// millions of times, so the answers are computed once per type and shared. Pure caching: the
// returned metadata is exactly what reflect would return.

type reflectTypeMeta struct {
	fields          []reflect.StructField
	compositeFields []int
	// byName maps every name t.FieldByName resolves (direct or promoted, unambiguous) to its
	// Index. Built eagerly from VisibleFields, so it is immutable once published.
	byName map[string][]int
}

var reflectTypeMetaCache sync.Map // reflect.Type -> *reflectTypeMeta

// reflectTypeMetaFront is a direct-mapped, lock-free front for reflectTypeMetaCache: a slot hit
// is one atomic load and a type comparison, where sync.Map.Load hashes the interface key (as
// costly as the FieldByName it replaces). A slot only ever holds a complete, immutable entry,
// and a miss or collision falls back to the map, so the answer never depends on slot contents.
type reflectTypeMetaSlot struct {
	t    reflect.Type
	meta *reflectTypeMeta
}

var reflectTypeMetaFront [256]atomic.Pointer[reflectTypeMetaSlot]

func reflectTypeMetaSlotOf(t reflect.Type) *atomic.Pointer[reflectTypeMetaSlot] {
	// The interface's data word is the *rtype; types are statically allocated, so it is stable.
	addr := (*[2]uintptr)(unsafe.Pointer(&t))[1]
	return &reflectTypeMetaFront[(addr>>5^addr>>13)&255]
}

func reflectMetaOf(t reflect.Type) *reflectTypeMeta {
	slot := reflectTypeMetaSlotOf(t)
	if e := slot.Load(); e != nil && e.t == t {
		return e.meta
	}
	meta := reflectMetaSlow(t)
	slot.Store(&reflectTypeMetaSlot{t: t, meta: meta})
	return meta
}

func reflectMetaSlow(t reflect.Type) *reflectTypeMeta {
	if cached, ok := reflectTypeMetaCache.Load(t); ok {
		return cached.(*reflectTypeMeta)
	}
	meta := &reflectTypeMeta{fields: make([]reflect.StructField, t.NumField()), byName: map[string][]int{}}
	for i := range meta.fields {
		meta.fields[i] = t.Field(i)
		f := meta.fields[i]
		if f.IsExported() && f.Type != syntaxWalkPositionType {
			switch f.Type.Kind() {
			case reflect.Pointer, reflect.Interface, reflect.Struct, reflect.Slice, reflect.Array, reflect.Map:
				meta.compositeFields = append(meta.compositeFields, i)
			}
		}
	}
	for _, vf := range reflect.VisibleFields(t) {
		if f, ok := t.FieldByName(vf.Name); ok {
			meta.byName[vf.Name] = f.Index
		}
	}
	cached, _ := reflectTypeMetaCache.LoadOrStore(t, meta)
	return cached.(*reflectTypeMeta)
}

// reflectFields returns t.Field(i) for every field of struct type t, computed once per type.
// Callers must not modify the returned slice; take `&fs[i]` to avoid copying an element.
func reflectFields(t reflect.Type) []reflect.StructField {
	return reflectMetaOf(t).fields
}

// reflectFieldIndex is t.FieldByName(name)'s Index, computed once per type.
func reflectFieldIndex(t reflect.Type, name string) ([]int, bool) {
	index, ok := reflectMetaOf(t).byName[name]
	return index, ok
}

// reflectFieldByName is v.FieldByName(name) (v a struct value) through the per-type cache: the
// zero Value when there is no such field, and the same panic as FieldByName when an embedded
// pointer on the path is nil.
func reflectFieldByName(v reflect.Value, name string) reflect.Value {
	index, ok := reflectFieldIndex(v.Type(), name)
	if !ok {
		return reflect.Value{}
	}
	if len(index) == 1 {
		return v.Field(index[0])
	}
	return v.FieldByIndex(index)
}

// reflectCompositeFields preserves exported composite fields, including maps,
// for walkers that never act on scalar values or source positions.
func reflectCompositeFields(t reflect.Type) []int { return reflectMetaOf(t).compositeFields }
