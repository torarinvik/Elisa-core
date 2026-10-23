package statecache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
)

type decoder struct {
	registry *Registry
	data     []byte
	at       int
	objects  []reflect.Value
	depth    int
	budget   uint64
}

func (r *Registry) Decode(data []byte) (any, error) {
	return r.DecodeWithLimit(data, 4<<30)
}

// DecodeWithLimit bounds container storage before allocating from a cache file.
func (r *Registry) DecodeWithLimit(data []byte, maxBytes uint64) (any, error) {
	if len(data) < 9+32+1+32 || string(data[:9]) != "ELISTATE1" || !bytes.Equal(data[9:41], r.fingerprint[:]) {
		return nil, fmt.Errorf("state schema mismatch")
	}
	payload := data[:len(data)-32]
	digest := sha256.Sum256(payload)
	if !bytes.Equal(digest[:], data[len(data)-32:]) {
		return nil, fmt.Errorf("state checksum mismatch")
	}
	d := &decoder{registry: r, data: payload, at: 41, objects: []reflect.Value{{}}, budget: maxBytes}
	id, err := d.uint()
	if err != nil {
		return nil, err
	}
	t, err := d.typ(id)
	if err != nil {
		return nil, err
	}
	v := reflect.New(t).Elem()
	if err := d.value(v); err != nil {
		return nil, err
	}
	if d.at != len(d.data) {
		return nil, fmt.Errorf("trailing state data")
	}
	return v.Interface(), nil
}
func (d *decoder) uint() (uint64, error) {
	v, n := binary.Uvarint(d.data[d.at:])
	if n <= 0 {
		return 0, fmt.Errorf("truncated state integer")
	}
	d.at += n
	return v, nil
}
func (d *decoder) take(n uint64) ([]byte, error) {
	if n > uint64(len(d.data)-d.at) {
		return nil, fmt.Errorf("truncated state bytes")
	}
	v := d.data[d.at : d.at+int(n)]
	d.at += int(n)
	return v, nil
}
func (d *decoder) typ(id uint64) (reflect.Type, error) {
	if id == 0 || id > uint64(len(d.registry.types)) {
		return nil, fmt.Errorf("invalid state type ID")
	}
	return d.registry.types[id-1], nil
}
func (d *decoder) count() (int, error) {
	n, err := d.uint()
	if err != nil {
		return 0, err
	}
	if n > uint64(len(d.data)-d.at) {
		return 0, fmt.Errorf("state collection exceeds payload")
	}
	return int(n), nil
}
func (d *decoder) value(v reflect.Value) error {
	d.depth++
	defer func() { d.depth-- }()
	if d.depth > 4096 {
		return fmt.Errorf("state graph nesting exceeds limit")
	}
	t := v.Type()
	switch v.Kind() {
	case reflect.Interface:
		id, err := d.uint()
		if err != nil {
			return err
		}
		if id == 0 {
			return nil
		}
		typ, err := d.typ(id)
		if err != nil {
			return err
		}
		if !typ.AssignableTo(t) {
			return fmt.Errorf("state type %s does not implement %s", typ, t)
		}
		inner := reflect.New(typ).Elem()
		if err := d.value(inner); err != nil {
			return err
		}
		v.Set(inner)
	case reflect.Pointer, reflect.Map, reflect.Slice:
		code, err := d.uint()
		if err != nil {
			return err
		}
		if code == 0 {
			return nil
		}
		id := code >> 1
		if code&1 == 0 {
			if id == 0 || id >= uint64(len(d.objects)) || d.objects[id].Type() != t {
				return fmt.Errorf("invalid state reference")
			}
			v.Set(d.objects[id])
			return nil
		}
		if id != uint64(len(d.objects)) {
			return fmt.Errorf("nonsequential state reference")
		}
		n := 0
		if v.Kind() != reflect.Pointer {
			n, err = d.count()
			if err != nil {
				return err
			}
		}
		switch v.Kind() {
		case reflect.Pointer:
			if err := d.reserve(1, t.Elem().Size()); err != nil {
				return err
			}
			if canonical, ok := d.registry.singletons[t]; ok {
				v.Set(canonical)
			} else {
				v.Set(reflect.New(t.Elem()))
			}
		case reflect.Map:
			if err := d.reserve(uint64(n), t.Key().Size()+t.Elem().Size()+32); err != nil {
				return err
			}
			v.Set(reflect.MakeMapWithSize(t, n))
		case reflect.Slice:
			if err := d.reserve(uint64(n), t.Elem().Size()); err != nil {
				return err
			}
			v.Set(reflect.MakeSlice(t, n, n))
		}
		// Save the value of the header, not a mutable destination reused by a map loop.
		owned := reflect.New(t).Elem()
		owned.Set(v)
		d.objects = append(d.objects, owned)
		switch v.Kind() {
		case reflect.Pointer:
			return d.value(v.Elem())
		case reflect.Slice:
			if t.Elem().Kind() == reflect.Uint8 {
				data, err := d.take(uint64(n))
				if err != nil {
					return err
				}
				reflect.Copy(v, reflect.ValueOf(data))
				return nil
			}
			for i := 0; i < n; i++ {
				if err := d.value(v.Index(i)); err != nil {
					return fmt.Errorf("%s[%d]: %w", t, i, err)
				}
			}
		case reflect.Map:
			for i := 0; i < n; i++ {
				k := reflect.New(t.Key()).Elem()
				x := reflect.New(t.Elem()).Elem()
				if err := d.value(k); err != nil {
					return err
				}
				if err := d.value(x); err != nil {
					return err
				}
				if !k.Comparable() {
					return fmt.Errorf("noncomparable state map key")
				}
				v.SetMapIndex(k, x)
			}
		}
	case reflect.Struct:
		n, err := d.count()
		if err != nil {
			return err
		}
		allowed := d.registry.fields[t]
		seen := map[uint64]bool{}
		for i := 0; i < n; i++ {
			field, err := d.uint()
			if err != nil {
				return err
			}
			valid := false
			for _, j := range allowed {
				if uint64(j) == field {
					valid = true
					break
				}
			}
			if !valid || seen[field] {
				return fmt.Errorf("invalid state field for %s", t)
			}
			seen[field] = true
			if err := d.value(d.registry.access(v, int(field))); err != nil {
				return fmt.Errorf("%s.%s: %w", t, t.Field(int(field)).Name, err)
			}
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := d.value(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.String:
		n, err := d.uint()
		if err != nil {
			return err
		}
		b, err := d.take(n)
		if err != nil {
			return err
		}
		v.SetString(string(b))
	default:
		x, err := d.uint()
		if err != nil {
			return err
		}
		switch v.Kind() {
		case reflect.Bool:
			if x > 1 {
				return fmt.Errorf("invalid boolean")
			}
			v.SetBool(x != 0)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n := int64(x>>1) ^ -int64(x&1)
			if v.OverflowInt(n) {
				return fmt.Errorf("state integer overflow")
			}
			v.SetInt(n)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if v.OverflowUint(x) {
				return fmt.Errorf("state integer overflow")
			}
			v.SetUint(x)
		case reflect.Float32:
			if x > math.MaxUint32 {
				return fmt.Errorf("state float overflow")
			}
			v.SetFloat(float64(math.Float32frombits(uint32(x))))
		case reflect.Float64:
			v.SetFloat(math.Float64frombits(x))
		case reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Uintptr:
			if x != 0 {
				return fmt.Errorf("process-local state is forbidden")
			}
		default:
			return fmt.Errorf("unsupported state type %s", t)
		}
	}
	return nil
}

func (d *decoder) reserve(count uint64, size uintptr) error {
	if size != 0 && count > d.budget/uint64(size) {
		return fmt.Errorf("state allocation budget exceeded")
	}
	d.budget -= count * uint64(size)
	return nil
}
