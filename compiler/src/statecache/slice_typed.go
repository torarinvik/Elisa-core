package statecache

import "reflect"

// RegisterTypedSlice supplies an optional allocation acceleration for a slice
// already present in the frozen schema. It cannot authorize additional types.
// Call during registry construction, before encoding or decoding concurrently.
// The factory is compiler code, never part of the serialized graph.
func RegisterTypedSlice[S ~[]E, E any](r *Registry) {
	t := reflect.TypeFor[S]()
	if r.ids[t] == 0 {
		return
	}
	if r.sliceAllocators == nil {
		r.sliceAllocators = make(map[reflect.Type]func(int, int) typedSliceAllocator)
	}
	r.sliceAllocators[t] = func(count, capacity int) typedSliceAllocator {
		return &typedSliceBatch[S, E]{remainingCount: count, remainingCapacity: capacity, size: reflect.TypeFor[E]().Size()}
	}
}

type typedSliceAllocator interface {
	next(int, int, *decoder) (reflect.Value, error)
}

type typedSliceBatch[S ~[]E, E any] struct {
	data                              []E
	headers                           []S
	nextData, nextHeader              int
	remainingCount, remainingCapacity int
	size                              uintptr
}

func (b *typedSliceBatch[S, E]) next(length, capacity int, d *decoder) (reflect.Value, error) {
	if b.nextData+capacity > len(b.data) {
		if err := d.reserve(uint64(len(b.data)-b.nextData), b.size); err != nil {
			return reflect.Value{}, err
		}
		b.data = make([]E, min(b.remainingCapacity, int(sliceBatchBytes/b.size)))
		b.nextData = 0
	}
	if b.nextHeader == len(b.headers) {
		b.headers = make([]S, min(b.remainingCount, int(sliceBatchBytes/reflect.TypeFor[S]().Size())))
		b.nextHeader = 0
	}
	// This ordinary typed Go slice operation creates no per-object heap header.
	// Reflect refers to a stable cell in a typed header array; both arrays remain
	// visible to the collector through the graph's owned slice values.
	b.headers[b.nextHeader] = S(b.data[b.nextData : b.nextData+length : b.nextData+capacity])
	value := reflect.ValueOf(&b.headers[b.nextHeader]).Elem()
	b.nextHeader++
	b.nextData += capacity
	b.remainingCount--
	b.remainingCapacity -= capacity
	return value, nil
}

// A sparse slice's full backing view is only used synchronously while filling
// its elements. Reusing one cell per type avoids allocating another header per
// graph object. Its real object-table header keeps the original length, which
// matters when an element refers back to its own slice during decoding.
func (d *decoder) fullSlice(v reflect.Value) (reflect.Value, error) {
	if v.Len() == v.Cap() {
		return v, nil
	}
	t := v.Type()
	full, ok := d.sliceFill[t]
	if !ok {
		if err := d.reserve(1, 128+t.Size()); err != nil {
			return reflect.Value{}, err
		}
		if d.sliceFill == nil {
			d.sliceFill = make(map[reflect.Type]reflect.Value)
		}
		full = reflect.New(t).Elem()
		d.sliceFill[t] = full
	}
	full.Set(v)
	full.SetLen(v.Cap())
	return full, nil
}
