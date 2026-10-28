package statecache

import (
	"fmt"
	"reflect"
)

const sliceBatchBytes = 1 << 20

type sliceBatch struct {
	values                 reflect.Value
	remaining, count, next int
	typed                  typedSliceAllocator
	typedChecked           bool
}

func smallSliceBatchEligible(t reflect.Type, capacity int) bool {
	size := t.Elem().Size()
	// Empty/zero-size and larger slices retain the ordinary allocation path.
	return size > 0 && capacity > 0 && uint64(capacity) <= uint64(sliceBatchBytes/128)/uint64(size)
}

// Back small slices with bounded typed arrays. Three-index slicing preserves
// each original capacity, so append cannot write into another logical slice.
// The graph still owns a separate slice value per object ID. Hidden values and
// interior aliases are filled/resolved through those original object bounds.
func (d *decoder) allocateSliceBatches(descriptions []objectDescription, batches []sliceBatch) error {
	for id := 1; id < len(descriptions); id++ {
		desc := descriptions[id]
		if desc.aliasIndex != 0 || d.objects[id].IsValid() {
			continue
		}
		typeID, length, capacity, ok := d.objectDetails(id, desc)
		if !ok || typeID == 0 || typeID > uint64(len(d.registry.types)) {
			return fmt.Errorf("missing or invalid state slice dimensions")
		}
		t := d.registry.types[typeID-1]
		if t.Kind() != reflect.Slice {
			continue
		}
		batch := &batches[typeID-1]
		var value reflect.Value
		if !batch.typedChecked {
			batch.typedChecked = true
			if factory := d.registry.sliceAllocators[t]; factory != nil && batch.count >= 8 {
				batch.typed = factory(batch.count, batch.remaining)
			}
		}
		if batch.typed != nil {
			var err error
			value, err = batch.typed.next(length, capacity, d)
			if err != nil {
				return err
			}
		} else if !batch.values.IsValid() && batch.count < 8 {
			value = reflect.MakeSlice(t, length, capacity)
		} else {
			if !batch.values.IsValid() || batch.next+capacity > batch.values.Len() {
				if batch.values.IsValid() {
					// Request capacities were charged while reading descriptors. Charge
					// any unfilled tail too, since live slices retain that physical block.
					if err := d.reserve(uint64(batch.values.Len()-batch.next), t.Elem().Size()); err != nil {
						return err
					}
				}
				n := min(batch.remaining, int(sliceBatchBytes/t.Elem().Size()))
				batch.values = reflect.MakeSlice(reflect.SliceOf(t.Elem()), n, n)
				batch.next = 0
			}
			value = batch.values.Slice3(batch.next, batch.next+length, batch.next+capacity)
			batch.next += capacity
			if value.Type() != t {
				value = value.Convert(t)
			}
		}
		batch.remaining -= capacity
		batch.count--
		d.objects[id] = value
	}
	return nil
}
