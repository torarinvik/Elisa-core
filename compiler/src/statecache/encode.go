package statecache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
)

type identity struct {
	typ      reflect.Type
	pointer  uintptr
	length   int
	capacity int
}
type encoder struct {
	registry    *Registry
	data        bytes.Buffer
	objects     map[identity]uint64
	depth       int
	allocations []allocation
	nodes       []stateNode
	scanning    bool
	strings     map[string]uint64
}

func (r *Registry) Encode(root any) ([]byte, error) {
	e := &encoder{registry: r, objects: map[identity]uint64{}, scanning: true, strings: map[string]uint64{}}
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
	e.scanning = false
	e.data.WriteString("ELISTATE3")
	e.data.Write(r.fingerprint[:])
	e.uint(r.ids[v.Type()])
	e.uint(uint64(len(e.nodes)))
	for _, node := range e.nodes {
		if err := e.descriptor(node); err != nil {
			return nil, err
		}
	}
	for _, node := range e.nodes {
		if node.alias == nil {
			if err := e.body(node.value); err != nil {
				return nil, err
			}
		}
	}
	if err := e.value(v); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(e.data.Bytes())
	e.data.Write(digest[:])
	return e.data.Bytes(), nil
}
func (e *encoder) uint(v uint64) {
	if e.scanning {
		return
	}
	var b [10]byte
	n := binary.PutUvarint(b[:], v)
	e.data.Write(b[:n])
}
func (e *encoder) str(v string) {
	if e.scanning {
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
	e.data.WriteString(v)
}
func (e *encoder) value(v reflect.Value) error {
	e.depth++
	defer func() { e.depth-- }()
	if e.depth > 4096 {
		return fmt.Errorf("state graph nesting exceeds limit")
	}
	t := v.Type()
	switch v.Kind() {
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

		key := objectIdentity(v)
		if id := e.objects[key]; id != 0 {
			e.uint(id)
			return nil
		}
		if !e.scanning {
			return fmt.Errorf("state graph changed while encoding %s", t)
		}
		id := uint64(len(e.nodes) + 1)
		e.objects[key] = id
		e.nodes = append(e.nodes, stateNode{value: v})
		e.recordAllocation(v, id)
		return e.body(v)

	case reflect.Struct:
		if !v.CanAddr() {
			copy := reflect.New(t).Elem()
			copy.Set(v)
			v = copy
		}
		if e.scanning {
			for _, i := range e.registry.scanFields[t] {
				f := e.registry.access(v, i)
				if !f.IsZero() {
					if err := e.value(f); err != nil {
						return fmt.Errorf("%s.%s: %w", t, t.Field(i).Name, err)
					}
				}
			}
			return nil
		}
		fields := e.registry.fields[t]
		live := 0
		for _, i := range fields {
			if !e.registry.access(v, i).IsZero() {
				live++
			}
		}
		e.uint(uint64(live))
		for _, i := range fields {
			f := e.registry.access(v, i)
			if f.IsZero() {
				continue
			}
			e.uint(uint64(i))
			if err := e.value(f); err != nil {
				return fmt.Errorf("%s.%s: %w", t, t.Field(i).Name, err)
			}
		}
	case reflect.Array:
		if e.scanning && !e.registry.hasObjects[t.Elem()] {
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
			return fmt.Errorf("process-local value %s cannot be persisted", t)
		}
		e.uint(0)
	default:
		return fmt.Errorf("unsupported state type %s", t)
	}
	return nil
}

func objectIdentity(v reflect.Value) identity {
	key := identity{typ: v.Type()}
	if v.Kind() == reflect.Map {
		key.pointer = uintptr(v.UnsafePointer())
	} else {
		key.pointer = v.Pointer()
	}
	if v.Kind() == reflect.Slice {
		key.length = v.Len()
		key.capacity = v.Cap()
	}
	return key
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
		full := v.Slice(0, v.Cap())
		if v.Type().Elem().Kind() == reflect.Uint8 {
			if !e.scanning {
				e.data.Write(full.Bytes())
			}
			return nil
		}
		for i := 0; i < full.Len(); i++ {
			if err := e.value(full.Index(i)); err != nil {
				return fmt.Errorf("%s[%d]: %w", v.Type(), i, err)
			}
		}
	case reflect.Map:
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
