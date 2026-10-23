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
	typ     reflect.Type
	pointer uintptr
	length  int
}
type encoder struct {
	registry    *Registry
	data        bytes.Buffer
	objects     map[identity]uint64
	depth       int
	allocations []allocation
}

func (r *Registry) Encode(root any) ([]byte, error) {
	e := &encoder{registry: r, objects: map[identity]uint64{}}
	e.data.WriteString("ELISTATE1")
	e.data.Write(r.fingerprint[:])
	v := reflect.ValueOf(root)
	if !v.IsValid() {
		return nil, fmt.Errorf("nil state root")
	}
	id := r.ids[v.Type()]
	if id == 0 {
		return nil, fmt.Errorf("unregistered root type %s", v.Type())
	}
	e.uint(id)
	if err := e.value(v); err != nil {
		return nil, err
	}
	if err := e.validateAliases(); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(e.data.Bytes())
	e.data.Write(digest[:])
	return e.data.Bytes(), nil
}
func (e *encoder) uint(v uint64) {
	var b [10]byte
	n := binary.PutUvarint(b[:], v)
	e.data.Write(b[:n])
}
func (e *encoder) str(v string) { e.uint(uint64(len(v))); e.data.WriteString(v) }
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
		key := identity{typ: t}
		if v.Kind() == reflect.Map {
			key.pointer = uintptr(v.UnsafePointer())
		} else {
			key.pointer = v.Pointer()
		}
		if v.Kind() == reflect.Slice {
			key.length = v.Len()
		}
		if id := e.objects[key]; id != 0 {
			e.uint(id << 1)
			return nil
		}
		id := uint64(len(e.objects) + 1)
		e.objects[key] = id
		e.recordAllocation(v)
		e.uint(id<<1 | 1)
		switch v.Kind() {
		case reflect.Pointer:
			return e.value(v.Elem())
		case reflect.Slice:
			e.uint(uint64(v.Len()))
			if t.Elem().Kind() == reflect.Uint8 {
				e.data.Write(v.Bytes())
				return nil
			}
			for i := 0; i < v.Len(); i++ {
				if err := e.value(v.Index(i)); err != nil {
					return fmt.Errorf("%s[%d]: %w", t, i, err)
				}
			}
		case reflect.Map:
			e.uint(uint64(v.Len()))
			it := v.MapRange()
			for it.Next() {
				if err := e.value(it.Key()); err != nil {
					return fmt.Errorf("%s key: %w", t, err)
				}
				if err := e.value(it.Value()); err != nil {
					return fmt.Errorf("%s value: %w", t, err)
				}
			}
		}
	case reflect.Struct:
		if !v.CanAddr() {
			copy := reflect.New(t).Elem()
			copy.Set(v)
			v = copy
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
