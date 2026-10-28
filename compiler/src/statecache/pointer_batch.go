package statecache

import (
	"fmt"
	"reflect"
)

const pointerBatchBytes = 64 << 10

type pointerBatch struct {
	values          reflect.Value
	remaining, next int
}

// Allocate compiler graph nodes in bounded typed backing arrays. Element
// addresses remain distinct and Go's collector sees their real pointer layout.
// This reduces millions of small allocations without serializing addresses or
// changing storage aliases. Zero-size/canonical pointers use the ordinary path.
// Each backing array is at most 64 KiB; live elements keep that block alive.
func (d *decoder) allocatePointerBatches(descriptions []objectDescription, batches []pointerBatch) error {
	for id := 1; id < len(descriptions); id++ {
		desc := descriptions[id]
		if desc.aliasIndex != 0 || d.objects[id].IsValid() {
			continue
		}
		typeID := uint64(desc.typeID)
		if typeID == 0 {
			var ok bool
			typeID, _, _, ok = d.objectDetails(id, desc)
			if !ok {
				return fmt.Errorf("missing state pointer dimensions")
			}
		}
		if typeID == 0 || typeID > uint64(len(d.registry.types)) {
			return fmt.Errorf("invalid state pointer type")
		}
		t := d.registry.types[typeID-1]
		if t.Kind() != reflect.Pointer {
			continue
		}
		batch := &batches[typeID-1]
		var pointer reflect.Value
		if !batch.values.IsValid() && batch.remaining < 8 {
			pointer = newStatePointer(t)
		} else {
			if !batch.values.IsValid() || batch.next == batch.values.Len() {
				n := min(batch.remaining, int(pointerBatchBytes/t.Elem().Size()))
				batch.values = reflect.MakeSlice(reflect.SliceOf(t.Elem()), n, n)
				batch.next = 0
			}
			pointer = batch.values.Index(batch.next).Addr()
			batch.next++
		}
		batch.remaining--
		if pointer.Type() != t {
			pointer = pointer.Convert(t)
		}
		d.objects[id] = pointer
	}
	return nil
}
