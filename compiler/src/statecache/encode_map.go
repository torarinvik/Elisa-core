package statecache

import (
	"fmt"
	"reflect"
)

type mapEncodeScratch struct {
	key, value reflect.Value
}

// Graph discovery retains reflect.Values for object descriptors. Once the
// inventory is frozen, each worker can copy map entries into reusable slots
// instead of allocating an independent reflect.Value backing for every entry.
func (e *encoder) writeMap(v reflect.Value) error {
	if v.Len() == 0 {
		return nil
	}
	t := v.Type()
	slot, ok := e.mapScratch[t]
	if !ok {
		slot = mapEncodeScratch{reflect.New(t.Key()).Elem(), reflect.New(t.Elem()).Elem()}
		if e.mapScratch == nil {
			e.mapScratch = make(map[reflect.Type]mapEncodeScratch)
		}
		e.mapScratch[t] = slot
	}
	it := v.MapRange()
	for it.Next() {
		slot.key.SetIterKey(it)
		slot.value.SetIterValue(it)
		if err := e.value(slot.key); err != nil {
			return fmt.Errorf("%s key: %w", t, err)
		}
		if err := e.value(slot.value); err != nil {
			return fmt.Errorf("%s value: %w", t, err)
		}
	}
	return nil
}
