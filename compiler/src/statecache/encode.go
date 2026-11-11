package statecache

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"slices"
)

type identity struct {
	typ      reflect.Type
	pointer  uintptr
	length   int
	capacity int
}

// Most addresses have one concrete type. Keep the common lookup keyed by
// address, while preserving distinct identities for typed aliases.
type pointerIdentity struct {
	typ     reflect.Type
	pointer uintptr
}

type pointerObject struct {
	typ reflect.Type
	id  uint64
}

type pointerObjectIndex struct {
	byAddress map[uintptr]pointerObject
	aliases   map[pointerIdentity]uint64
}

func (index *pointerObjectIndex) lookup(address uintptr, typ reflect.Type) uint64 {
	object := index.byAddress[address]
	if object.typ == typ {
		return object.id
	}
	if object.id != 0 {
		return index.aliases[pointerIdentity{typ, address}]
	}
	return 0
}

func (index *pointerObjectIndex) add(address uintptr, typ reflect.Type, id uint64) {
	if index.byAddress[address].id == 0 {
		index.byAddress[address] = pointerObject{typ, id}
		return
	}
	if index.aliases == nil {
		index.aliases = make(map[pointerIdentity]uint64)
	}
	index.aliases[pointerIdentity{typ, address}] = id
}

type encoder struct {
	registry        *Registry
	data            []byte
	objects         map[identity]uint64
	pointerObjects  pointerObjectIndex
	depth           int
	allocationCount int
	allocations     []allocation
	nodes           nodeInventory
	scanning        bool
	strings         map[string]uint64
	framed          bool
	stringTable     []string
	// interner replaces strings/stringTable once framed bodies are written in parallel.
	interner   *stringInterner
	mapScratch map[reflect.Type]mapEncodeScratch
}

func (r *Registry) Encode(root any) ([]byte, error) { return r.encode(root, false, 0) }

// EncodeWithCapacity reserves output space using a trusted caller's size estimate.
// The hint changes allocations only; undersized hints grow normally and the wire
// format, graph inventory, validation and checksum are identical to Encode.
// Callers must bound positive capacity hints to their allocation budget.
func (r *Registry) EncodeWithCapacity(root any, capacity int) ([]byte, error) {
	return r.encode(root, false, capacity)
}

// EncodeFramed writes independent object records and a shared string dictionary.
// Decode accepts both this format and the preceding sequential format.
func (r *Registry) EncodeFramed(root any) ([]byte, error) { return r.encode(root, true, 0) }

// EncodeFramedWithCapacity reserves output space without changing the framed
// format. Positive hints must be bounded to the caller's allocation budget.
func (r *Registry) EncodeFramedWithCapacity(root any, capacity int) ([]byte, error) {
	return r.encode(root, true, capacity)
}

func (r *Registry) encode(root any, framed bool, capacity int) ([]byte, error) {
	var pointerCapacity, sliceCapacity int
	if framed && capacity >= 32<<20 {
		// Reused checkpoints usually retain most of their graph. Reserve
		// a conservative fraction of the previous wire size to avoid
		// repeated growth of the address index while discovering objects.
		pointerCapacity = min(capacity/128, 2_000_000)
		if capacity >= 128<<20 {
			// Large resumed analysis graphs contain millions of both kinds
			// of entries. Reserve both tables to avoid growing them mid-walk.
			pointerCapacity = min(capacity/96, 6_000_000)
			sliceCapacity = min(capacity/256, 3_000_000)
		}
	}
	e := &encoder{
		registry: r, objects: make(map[identity]uint64, sliceCapacity), scanning: true,
		pointerObjects: pointerObjectIndex{byAddress: make(map[uintptr]pointerObject, pointerCapacity)},
		strings:        map[string]uint64{}, framed: framed,
	}
	v := reflect.ValueOf(root)
	if !v.IsValid() || r.ids[v.Type()] == 0 {
		return nil, fmt.Errorf("unregistered state root")
	}
	if err := e.value(v); err != nil {
		return nil, err
	}
	if err := e.bindAliases(); err != nil {
		return nil, err
	}
	if capacity > 0 {
		e.data = make([]byte, 0, capacity)
	}
	e.scanning = false
	if framed {
		e.data = append(e.data, "ELISTATE4"...)
	} else {
		e.data = append(e.data, "ELISTATE3"...)
	}
	e.data = append(e.data, r.fingerprint[:]...)
	e.uint(r.ids[v.Type()])
	e.uint(uint64(e.nodes.count))
	for i := 0; i < e.nodes.count; i++ {
		node := e.nodes.at(i)
		if err := e.descriptor(*node); err != nil {
			return nil, err
		}
	}
	framing := len(e.data)
	if framed {
		// Reserve the frame table in the output buffer. Every slot is
		// overwritten before publication, including the dictionary offset
		// and the root body size.
		size := 8 * (e.nodes.count + 2)
		e.data = slices.Grow(e.data, size)
		e.data = e.data[:framing+size]
	}
	if framed && r.parallelDecode && e.nodes.count >= parallelFramedEncodeMin {
		if err := e.writeFramedBodies(framing); err != nil {
			return nil, err
		}
	} else {
		for i := 0; i < e.nodes.count; i++ {
			before := len(e.data)
			node := e.nodes.at(i)
			if node.alias == nil {
				if err := e.body(node.value); err != nil {
					return nil, err
				}
			}
			if framed {
				binary.LittleEndian.PutUint64(e.data[framing+8*(i+1):], uint64(len(e.data)-before))
			}
		}
	}
	before := len(e.data)
	if err := e.value(v); err != nil {
		return nil, err
	}
	if framed {
		binary.LittleEndian.PutUint64(e.data[framing+8*(e.nodes.count+1):], uint64(len(e.data)-before))
		binary.LittleEndian.PutUint64(e.data[framing:], uint64(len(e.data)))
		table := e.stringTable
		if e.interner != nil {
			table = e.interner.table
		}
		e.uint(uint64(len(table)))
		for _, value := range table {
			e.uint(uint64(len(value)))
			e.data = append(e.data, value...)
		}
	}
	digest := sha256.Sum256(e.data)
	e.data = append(e.data, digest[:]...)
	return e.data, nil
}
func (e *encoder) uint(v uint64) {
	if e.scanning {
		return
	}
	if v < 0x80 {
		e.data = append(e.data, byte(v))
		return
	}
	e.data = binary.AppendUvarint(e.data, v)
}
func (e *encoder) str(v string) {
	if e.scanning {
		return
	}
	if e.framed {
		if v == "" {
			e.uint(0)
			return
		}
		if e.interner != nil {
			e.uint(e.interner.id(v))
			return
		}
		id := e.strings[v]
		if id == 0 {
			id = uint64(len(e.stringTable) + 1)
			e.strings[v] = id
			e.stringTable = append(e.stringTable, v)
		}
		e.uint(id)
		return
	}
	// Zero denotes the empty string; even codes refer to an earlier string.
	// Odd codes introduce a new string with code>>1 bytes. Intern by content,
	// since independent tokens often carry equal filenames in distinct storage.
	if v == "" {
		e.uint(0)
		return
	}
	if id := e.strings[v]; id != 0 {
		e.uint(id << 1)
		return
	}
	e.strings[v] = uint64(len(e.strings) + 1)
	e.uint(uint64(len(v))<<1 | 1)
	e.data = append(e.data, v...)
}
func (e *encoder) value(v reflect.Value) error {
	e.depth++
	if e.depth > 4096 {
		e.depth--
		return fmt.Errorf("state graph nesting exceeds limit")
	}
	err := e.valueAtDepth(v)
	e.depth--
	return err
}

func (e *encoder) valueAtDepth(v reflect.Value) error {
	kind := v.Kind()
	switch kind {
	case reflect.Interface:
		if v.IsNil() {
			e.uint(0)
			return nil
		}
		inner := v.Elem()
		id := e.registry.ids[inner.Type()]
		if id == 0 {
			return fmt.Errorf("unregistered dynamic type %s", inner.Type())
		}
		e.uint(id)
		return e.value(inner)
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if v.IsNil() {
			e.uint(0)
			return nil
		}

		t := v.Type()
		address := uintptr(v.UnsafePointer())
		var key identity
		var prior uint64
		if kind == reflect.Slice {
			key = identity{typ: t, pointer: address, length: v.Len(), capacity: v.Cap()}
			prior = e.objects[key]
		} else {
			prior = e.pointerObjects.lookup(address, t)
		}
		if prior != 0 {
			e.uint(prior)
			return nil
		}
		if !e.scanning {
			return fmt.Errorf("state graph changed while encoding %s", t)
		}
		id := uint64(e.nodes.count + 1)
		if kind == reflect.Slice {
			e.objects[key] = id
		} else {
			e.pointerObjects.add(address, t, id)
		}
		e.nodes.add(stateNode{value: v})
		if allocationSize(v) != 0 {
			e.allocationCount++
		}
		return e.body(v)

	case reflect.Struct:
		t := v.Type()
		var fields []int
		if e.scanning {
			fields = e.registry.scanFields[t]
			if len(fields) == 0 {
				return nil
			}
		} else {
			fields = e.registry.fields[t]
		}
		if !v.CanAddr() {
			copy := reflect.New(t).Elem()
			copy.Set(v)
			v = copy
		}
		if e.scanning {
			for _, i := range fields {
				f := e.registry.access(v, i)
				if !f.IsZero() {
					if err := e.value(f); err != nil {
						return fmt.Errorf("%s.%s: %w", t, t.Field(i).Name, err)
					}
				}
			}
			return nil
		}
		var small [32]reflect.Value
		present := small[:0]
		if len(fields) > len(small) {
			present = make([]reflect.Value, 0, len(fields))
		}
		live := 0
		for _, i := range fields {
			f := e.registry.access(v, i)
			if f.IsZero() {
				f = reflect.Value{}
			} else {
				live++
			}
			present = append(present, f)
		}
		e.uint(uint64(live))
		for k, i := range fields {
			f := present[k]
			if !f.IsValid() {
				continue
			}
			e.uint(uint64(i))
			if err := e.value(f); err != nil {
				return fmt.Errorf("%s.%s: %w", t, t.Field(i).Name, err)
			}
		}
	case reflect.Array:
		if e.scanning && !e.registry.hasObjects[v.Type().Elem()] {
			return nil
		}
		for i := 0; i < v.Len(); i++ {
			if err := e.value(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Bool:
		if v.Bool() {
			e.uint(1)
		} else {
			e.uint(0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		x := v.Int()
		e.uint(uint64(x<<1) ^ uint64(x>>63))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		e.uint(v.Uint())
	case reflect.Float32:
		e.uint(uint64(math.Float32bits(float32(v.Float()))))
	case reflect.Float64:
		e.uint(math.Float64bits(v.Float()))
	case reflect.String:
		e.str(v.String())
	case reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Uintptr:
		if !v.IsZero() {
			return fmt.Errorf("process-local value %s cannot be persisted", v.Type())
		}
		e.uint(0)
	default:
		return fmt.Errorf("unsupported state type %s", v.Type())
	}
	return nil
}

func (e *encoder) body(v reflect.Value) error {
	switch v.Kind() {
	case reflect.Pointer:
		if e.scanning && !e.registry.hasObjects[v.Type().Elem()] {
			return nil
		}
		return e.value(v.Elem())
	case reflect.Slice:
		if e.scanning && !e.registry.hasObjects[v.Type().Elem()] {
			return nil
		}
		full := v
		if v.Len() != v.Cap() {
			full = v.Slice(0, v.Cap())
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			if !e.scanning {
				e.data = append(e.data, full.Bytes()...)
			}
			return nil
		}
		for i := 0; i < full.Len(); i++ {
			if err := e.value(full.Index(i)); err != nil {
				return fmt.Errorf("%s[%d]: %w", v.Type(), i, err)
			}
		}
	case reflect.Map:
		if !e.scanning && v.CanInterface() {
			return e.writeMap(v)
		}
		keys := !e.scanning || e.registry.hasObjects[v.Type().Key()]
		values := !e.scanning || e.registry.hasObjects[v.Type().Elem()]
		if !keys && !values {
			return nil
		}
		it := v.MapRange()
		for it.Next() {
			if keys {
				if err := e.value(it.Key()); err != nil {
					return fmt.Errorf("%s key: %w", v.Type(), err)
				}
			}
			if values {
				if err := e.value(it.Value()); err != nil {
					return fmt.Errorf("%s value: %w", v.Type(), err)
				}
			}
		}
	}
	return nil
}
func (e *encoder) descriptor(node stateNode) error {
	v := node.value
	id := e.registry.ids[v.Type()]
	if id == 0 {
		return fmt.Errorf("unregistered state object %s", v.Type())
	}
	e.uint(id)
	if v.Kind() == reflect.Slice {
		e.uint(uint64(v.Len()))
		e.uint(uint64(v.Cap()))
	} else if v.Kind() == reflect.Map {
		e.uint(uint64(v.Len()))
	}
	if node.alias == nil {
		e.uint(0)
		return nil
	}
	a := node.alias
	e.uint(a.owner)
	e.uint(uint64(len(a.path)))
	for _, step := range a.path {
		code := uint64(step.index) << 1
		if step.element {
			code |= 1
		}
		e.uint(code)
	}
	if v.Kind() == reflect.Slice {
		e.uint(uint64(a.offset))
	}
	return nil
}

// EncodeWithFieldAccess applies an invocation-local read projection while keeping
// the registered field types and wire schema. The supplied values must have the
// same types as their fields. The registry and original graph are not modified.
func (r *Registry) EncodeWithFieldAccess(root any, access FieldAccess) ([]byte, error) {
	projected := *r
	if access != nil {
		projected.access = access
	}
	return projected.Encode(root)
}

// EncodeFramedWithFieldAccess applies the same read projection as EncodeWithFieldAccess.
func (r *Registry) EncodeFramedWithFieldAccess(root any, access FieldAccess) ([]byte, error) {
	projected := *r
	if access != nil {
		projected.access = access
	}
	return projected.EncodeFramed(root)
}
