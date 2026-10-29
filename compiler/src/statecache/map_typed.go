package statecache

import (
	"fmt"
	"reflect"
	"unsafe"
)

// RegisterTypedMap accelerates insertion for a registered map type while the
// ordinary decoder continues to validate every encoded key and value.
// Register only during registry construction, before concurrent decoding.
func RegisterTypedMap[M ~map[K]V, K comparable, V any](r *Registry) {
	t := reflect.TypeFor[M]()
	if r.ids[t] == 0 {
		return
	}
	if r.typedMaps == nil {
		r.typedMaps = make(map[reflect.Type]func(*decoder, reflect.Value, int) error)
	}
	r.typedMaps[t] = func(d *decoder, v reflect.Value, length int) error {
		dst := v.Interface().(M)
		entry, err := d.mapEntryScratch(v.Type())
		if err != nil {
			return err
		}
		key, value := entry.key, entry.value
		kp := (*K)(unsafe.Pointer(key.UnsafeAddr()))
		vp := (*V)(unsafe.Pointer(value.UnsafeAddr()))
		for i := 0; i < length; i++ {
			key.SetZero()
			value.SetZero()
			if err := d.value(key); err != nil {
				return err
			}
			if err := d.value(value); err != nil {
				return err
			}
			if !key.Comparable() {
				return fmt.Errorf("noncomparable state map key")
			}
			dst[*kp] = *vp
		}
		if len(dst) != length {
			return fmt.Errorf("duplicate state map key")
		}
		return nil
	}
}
