package statecache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"sync/atomic"
	"unsafe"
)

type decoder struct {
	registry     *Registry
	data         []byte
	at           int
	objects      []reflect.Value
	aliases      decodedAliasTable
	wideObjects  map[int]wideObjectDescription
	depth        int
	budget       uint64
	strings      []string
	mapEntries   map[reflect.Type]decodedMapEntry
	sliceFill    map[reflect.Type]reflect.Value
	framed       bool
	sharedBudget *atomic.Uint64
	// ifaceWords caches, per (concrete, interface) type pair already checked assignable, the
	// interface's first word (itab or type), so a pointer-shaped value is stored without
	// reflect's per-assignment implements check.
	ifaceWords map[[2]reflect.Type]unsafe.Pointer
}

func (r *Registry) Decode(data []byte) (any, error) {
	return r.DecodeWithLimit(data, 4<<30)
}

// DecodeWithLimit bounds container storage before allocating from a cache file.
func (r *Registry) DecodeWithLimit(data []byte, maxBytes uint64) (any, error) {
	if len(data) < 9+32+1+32 || (string(data[:9]) != "ELISTATE3" && string(data[:9]) != "ELISTATE4") || !bytes.Equal(data[9:41], r.fingerprint[:]) {
		return nil, fmt.Errorf("state schema mismatch")
	}
	payload := data[:len(data)-32]
	// The checksum runs beside decoding, which validates every reference and budget as it
	// goes, so a corrupt payload fails closed either way; the result is used only if it holds.
	checked := make(chan bool, 1)
	go func() {
		digest := sha256.Sum256(payload)
		checked <- bytes.Equal(digest[:], data[len(data)-32:])
	}()
	value, err := r.decodePayload(payload, data, maxBytes)
	if !<-checked {
		return nil, fmt.Errorf("state checksum mismatch")
	}
	return value, err
}

func (r *Registry) decodePayload(payload, data []byte, maxBytes uint64) (any, error) {
	d := &decoder{registry: r, data: payload, at: 41, objects: []reflect.Value{{}}, budget: maxBytes, strings: []string{""}, framed: string(data[:9]) == "ELISTATE4"}
	id, err := d.uint()
	if err != nil {
		return nil, err
	}
	t, err := d.typ(id)
	if err != nil {
		return nil, err
	}
	if err := d.readObjects(); err != nil {
		return nil, err
	}
	if err := d.reserve(1, t.Size()); err != nil {
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
	if d.depth > 4096 {
		d.depth--
		return fmt.Errorf("state graph nesting exceeds limit")
	}
	err := d.valueAtDepth(v)
	d.depth--
	return err
}

func (d *decoder) valueAtDepth(v reflect.Value) error {
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
		if !d.assignable(typ, t) {
			return fmt.Errorf("state type %s does not implement %s", typ, t)
		}
		// Reference objects already own a stable typed value. Setting the
		// interface from that value preserves identity without allocating an
		// intermediate mutable pointer/map/slice header for every occurrence.
		switch typ.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice:
			ref, err := d.uint()
			if err != nil {
				return err
			}
			if ref == 0 {
				v.Set(reflect.Zero(typ)) // a typed nil, not a nil interface
				return nil
			}
			if ref >= uint64(len(d.objects)) || d.objects[ref].Type() != typ {
				return fmt.Errorf("invalid state reference")
			}
			d.setInterface(v, d.objects[ref])
			return nil
		}
		if err := d.reserve(1, typ.Size()); err != nil {
			return err
		}
		inner := reflect.New(typ).Elem()
		if err := d.value(inner); err != nil {
			return err
		}
		v.Set(inner)

	case reflect.Pointer, reflect.Map, reflect.Slice:
		id, err := d.uint()
		if err != nil {
			return err
		}
		if id == 0 {
			return nil
		}
		if id >= uint64(len(d.objects)) || d.objects[id].Type() != t {
			return fmt.Errorf("invalid state reference")
		}
		v.Set(d.objects[id])

	case reflect.Struct:
		n, err := d.count()
		if err != nil {
			return err
		}
		allowed := d.registry.fields[t]
		// The encoder emits nonzero fields in increasing schema order. Walk
		// that same order once: omissions remain valid, while duplicates,
		// descending IDs and excluded/unknown fields are rejected without a
		// per-value map or repeatedly scanning the entire schema.
		next := 0
		for i := 0; i < n; i++ {
			field, err := d.uint()
			if err != nil {
				return err
			}
			for next < len(allowed) && uint64(allowed[next]) < field {
				next++
			}
			if next == len(allowed) || uint64(allowed[next]) != field {
				return fmt.Errorf("invalid state field for %s", t)
			}
			next++
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
		s, err := d.stateString()
		if err != nil {
			return err
		}
		v.SetString(s)
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
	if d.sharedBudget != nil {
		for {
			remaining := d.sharedBudget.Load()
			if size != 0 && count > remaining/uint64(size) {
				return fmt.Errorf("state allocation budget exceeded")
			}
			if d.sharedBudget.CompareAndSwap(remaining, remaining-count*uint64(size)) {
				return nil
			}
		}
	}
	if size != 0 && count > d.budget/uint64(size) {
		return fmt.Errorf("state allocation budget exceeded")
	}
	d.budget -= count * uint64(size)
	return nil
}

// stateString copies each distinct byte string once. Later references share its
// immutable bytes, never the input buffer. Both bytes and dictionary entries
// count against the caller's decode allocation budget.
func (d *decoder) stateString() (string, error) {
	code, err := d.uint()
	if err != nil {
		return "", err
	}
	if d.framed {
		if code >= uint64(len(d.strings)) {
			return "", fmt.Errorf("invalid state string ID")
		}
		return d.strings[code], nil
	}
	if code == 0 {
		return "", nil
	}
	if code&1 == 0 {
		id := code >> 1
		if id >= uint64(len(d.strings)) {
			return "", fmt.Errorf("invalid state string ID")
		}
		return d.strings[id], nil
	}
	n := code >> 1
	if n == 0 {
		return "", fmt.Errorf("empty state string literal")
	}
	b, err := d.take(n)
	if err != nil {
		return "", err
	}
	if err := d.reserve(n, 1); err != nil {
		return "", err
	}
	if err := d.reserve(1, reflect.TypeFor[string]().Size()); err != nil {
		return "", err
	}
	s := string(b)
	d.strings = append(d.strings, s)
	return s, nil
}

func (d *decoder) assignable(typ, iface reflect.Type) bool {
	if _, ok := d.ifaceWords[[2]reflect.Type{typ, iface}]; ok {
		return true
	}
	return typ.AssignableTo(iface)
}

// setInterface stores x, a pointer or map value already checked assignable to v's interface
// type, into v. The interface's words are (itab or type, data pointer); for pointer-shaped
// x the data word is x itself.
func (d *decoder) setInterface(v, x reflect.Value) {
	key := [2]reflect.Type{x.Type(), v.Type()}
	word, ok := d.ifaceWords[key]
	if !ok {
		if !v.CanAddr() || (x.Kind() != reflect.Pointer && x.Kind() != reflect.Map) {
			v.Set(x)
			return
		}
		proto := reflect.New(v.Type()).Elem()
		proto.Set(x)
		word = (*[2]unsafe.Pointer)(proto.Addr().UnsafePointer())[0]
		if d.ifaceWords == nil {
			d.ifaceWords = map[[2]reflect.Type]unsafe.Pointer{}
		}
		d.ifaceWords[key] = word
	}
	if !v.CanAddr() {
		v.Set(x)
		return
	}
	slot := (*[2]unsafe.Pointer)(v.Addr().UnsafePointer())
	slot[0], slot[1] = word, x.UnsafePointer()
}
