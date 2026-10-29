// Package statecache encodes compiler-owned graphs using object IDs rather than
// process addresses. It preserves cycles and shared pointer/map/slice objects.
// A schema is supplied by the compiler; the input never selects arbitrary types.
package statecache

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type FieldAccess func(reflect.Value, int) reflect.Value
type FieldPolicy func(reflect.Type, string) bool

type Registry struct {
	types           []reflect.Type
	ids             map[reflect.Type]uint64
	fields          map[reflect.Type][]int
	scanFields      map[reflect.Type][]int
	hasObjects      map[reflect.Type]bool
	fingerprint     [32]byte
	access          FieldAccess
	singletons      map[reflect.Type]reflect.Value
	sliceAllocators map[reflect.Type]func(int, int) typedSliceAllocator
	typedMaps       map[reflect.Type]func(*decoder, reflect.Value, int) error
	parallelDecode  bool
}

// New freezes the complete local schema. Policy exclusions must describe
// invocation-local state the caller explicitly reconstructs after decoding.
func New(roots []reflect.Type, policy FieldPolicy, access FieldAccess) *Registry {
	if access == nil {
		access = func(v reflect.Value, i int) reflect.Value { return v.Field(i) }
	}
	r := &Registry{ids: map[reflect.Type]uint64{}, fields: map[reflect.Type][]int{}, access: access}
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		if seen[t] {
			return
		}
		seen[t] = true
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(t.Elem())
		case reflect.Map:
			walk(t.Key())
			walk(t.Elem())
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if policy != nil && !policy(t, f.Name) {
					continue
				}
				r.fields[t] = append(r.fields[t], i)
				walk(f.Type)
			}
		}
	}
	for _, t := range roots {
		walk(t)
		walk(reflect.PointerTo(t))
	}
	for _, v := range []any{false, int(0), int8(0), int16(0), int32(0), int64(0), uint(0), uint8(0), uint16(0), uint32(0), uint64(0), float32(0), float64(0), ""} {
		walk(reflect.TypeOf(v))
	}
	// Inventory only values that can contain graph identities. Scalar fields
	// (including positions made of strings/integers) are validated and written
	// in the encoding pass; traversing them adds no owners or aliases.
	r.hasObjects = make(map[reflect.Type]bool)
	r.scanFields = make(map[reflect.Type][]int)
	var hasObjects func(reflect.Type) bool
	hasObjects = func(t reflect.Type) bool {
		if result, ok := r.hasObjects[t]; ok {
			return result
		}
		result := false
		switch t.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Slice, reflect.Map:
			result = true
		case reflect.Array:
			result = hasObjects(t.Elem())
		case reflect.Struct:
			for _, i := range r.fields[t] {
				if hasObjects(t.Field(i).Type) {
					r.scanFields[t] = append(r.scanFields[t], i)
					result = true
				}
			}
		}
		r.hasObjects[t] = result
		return result
	}
	for t := range seen {
		hasObjects(t)
		r.types = append(r.types, t)
	}
	sort.Slice(r.types, func(i, j int) bool { return typeName(r.types[i]) < typeName(r.types[j]) })
	var schema strings.Builder
	for i, t := range r.types {
		r.ids[t] = uint64(i + 1)
		fmt.Fprintf(&schema, "%s:%d:%d\n", typeName(t), t.Kind(), t.Size())
		for _, j := range r.fields[t] {
			f := t.Field(j)
			fmt.Fprintf(&schema, "%s:%s\n", f.Name, typeName(f.Type))
		}
	}
	r.fingerprint = sha256.Sum256([]byte(schema.String()))
	return r
}

func typeName(t reflect.Type) string {
	if t.Name() != "" {
		return t.PkgPath() + "/" + t.Name()
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + typeName(t.Elem())
	case reflect.Slice:
		return "[]" + typeName(t.Elem())
	case reflect.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), typeName(t.Elem()))
	case reflect.Map:
		return "map[" + typeName(t.Key()) + "]" + typeName(t.Elem())

	case reflect.Struct:
		var b strings.Builder
		b.WriteString("struct{")
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			fmt.Fprintf(&b, "%s/%s:%s:%q:%v;", f.PkgPath, f.Name, typeName(f.Type), string(f.Tag), f.Anonymous)
		}
		b.WriteString("}")
		return b.String()
	default:
		return t.String()
	}
}

// InternZeroPointer binds a stateless marker to the current compiler's canonical
// singleton. Call while constructing the registry, before concurrent use.
func (r *Registry) InternZeroPointer(value any) {
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Type().Elem().Kind() != reflect.Struct || v.Type().Elem().NumField() != 0 {
		panic("state singleton must point to an empty struct")
	}
	if r.singletons == nil {
		r.singletons = map[reflect.Type]reflect.Value{}
	}
	r.singletons[v.Type()] = v
}

// EnableParallelDecode asserts that the registered field accessor, and any
// projection passed to an encode, is safe for concurrent calls on disjoint
// objects. Framed decoding and encoding then use workers. Call only during
// registry construction.
func (r *Registry) EnableParallelDecode() { r.parallelDecode = true }
