package statecache

import (
	"fmt"
	"reflect"
)

type objectDescription struct {
	typeID     uint32
	sizes      uint32
	aliasIndex uint32 // zero means no alias; otherwise index+1 in decoder.aliases
}

// Oversized extents are rare. Keep them in a sparse side table so the common
// object descriptor remains three pointer-free uint32 values (12 bytes).
type wideObjectDescription struct {
	typeID           uint64
	length, capacity int
}

func (d *decoder) describeObject(id int, typeID uint64, length, capacity int, compactLimit uint32) (objectDescription, error) {
	if typeID == 0 || length < 0 || capacity < 0 {
		return objectDescription{}, fmt.Errorf("invalid state object dimensions")
	}
	max := uint64(compactLimit)
	if max > uint64(^uint16(0)) {
		max = uint64(^uint16(0))
	}
	if typeID <= uint64(^uint32(0)) && uint64(length) <= max && uint64(capacity) <= max {
		sizes := uint32(uint16(length)) | uint32(uint16(capacity))<<16
		return objectDescription{typeID: uint32(typeID), sizes: sizes}, nil
	}
	if err := d.reserve(1, 128); err != nil {
		return objectDescription{}, err
	}
	if d.wideObjects == nil {
		d.wideObjects = make(map[int]wideObjectDescription)
	}
	d.wideObjects[id] = wideObjectDescription{typeID: typeID, length: length, capacity: capacity}
	return objectDescription{}, nil
}

func (d *decoder) objectDetails(id int, desc objectDescription) (uint64, int, int, bool) {
	if desc.typeID != 0 {
		length := int(uint16(desc.sizes))
		capacity := int(uint16(desc.sizes >> 16))
		return uint64(desc.typeID), length, capacity, true
	}
	wide, ok := d.wideObjects[id]
	if !ok {
		return 0, 0, 0, false
	}
	return wide.typeID, wide.length, wide.capacity, true
}

func (d *decoder) readObjects() error {
	count, err := d.count()
	if err != nil {
		return err
	}
	d.aliases.maxAliases = uint64(count)
	if err := d.reserve(uint64(count), 128); err != nil {
		return err
	}
	descriptions := make([]objectDescription, count+1)
	d.objects = make([]reflect.Value, count+1)
	// Only large graphs with sufficient per-object slack use batching.
	var batches []pointerBatch
	var sliceBatches []sliceBatch
	if count >= 4096 && len(d.registry.types) <= count/8 {
		batches = make([]pointerBatch, len(d.registry.types))
		sliceBatches = make([]sliceBatch, len(d.registry.types))
	}
	for id := 1; id <= count; id++ {
		typeID, err := d.uint()
		if err != nil {
			return err
		}
		t, err := d.typ(typeID)
		if err != nil {
			return err
		}
		if t.Kind() != reflect.Pointer && t.Kind() != reflect.Map && t.Kind() != reflect.Slice {
			return fmt.Errorf("invalid state object kind")
		}
		length, capacity := 0, 0
		if t.Kind() != reflect.Pointer {
			length, err = d.count()
			if err != nil {
				return err
			}
		}
		if t.Kind() == reflect.Slice {
			capacity, err = d.count()
			if err != nil {
				return err
			}
			if length > capacity {
				return fmt.Errorf("invalid state slice capacity")
			}
		}
		desc, err := d.describeObject(id, typeID, length, capacity, uint32(^uint16(0)))
		if err != nil {
			return err
		}
		owner, err := d.uint()
		if err != nil {
			return err
		}
		if owner != 0 {
			if owner > uint64(count) || owner == uint64(id) || t.Kind() == reflect.Map {
				return fmt.Errorf("invalid state alias owner")
			}
			n, err := d.count()
			if err != nil {
				return err
			}
			if d.aliases.count == ^uint32(0) {
				return fmt.Errorf("too many state aliases")
			}
			alias := nodeAlias{owner: owner}
			if err := d.reserve(uint64(n), 16); err != nil {
				return err
			}
			for j := 0; j < n; j++ {
				step, err := d.uint()
				if err != nil {
					return err
				}
				if step>>1 > uint64(len(d.data)) {
					return fmt.Errorf("invalid state alias step")
				}
				alias.path = append(alias.path, pathStep{index: int(step >> 1), element: step&1 != 0})
			}
			if t.Kind() == reflect.Slice {
				alias.offset, err = d.count()
				if err != nil {
					return err
				}
			}
			aliasIndex, err := d.aliases.append(d, alias)
			if err != nil {
				return err
			}
			desc.aliasIndex = aliasIndex
		} else {
			var v reflect.Value
			switch t.Kind() {
			case reflect.Pointer:
				if err := d.reserve(1, t.Elem().Size()); err != nil {
					return err
				}
				if canonical, ok := d.registry.singletons[t]; ok {
					v = canonical
				} else if batches != nil && t.Elem().Size() > 0 && t.Elem().Size() <= pointerBatchBytes/8 {
					batches[typeID-1].remaining++
				} else {
					v = newStatePointer(t)
					if v.Type() != t {
						v = v.Convert(t)
					}
				}
			case reflect.Map:
				if err := d.reserve(uint64(length), t.Key().Size()+t.Elem().Size()+32); err != nil {
					return err
				}
				v = reflect.MakeMapWithSize(t, length)
			case reflect.Slice:
				if err := d.reserve(uint64(capacity), t.Elem().Size()); err != nil {
					return err
				}
				if sliceBatches != nil && smallSliceBatchEligible(t, capacity) {
					batch := &sliceBatches[typeID-1]
					batch.remaining += capacity
					batch.count++
				} else {
					v = reflect.MakeSlice(t, length, capacity)
				}
			}
			d.objects[id] = v
		}
		descriptions[id] = desc
	}
	if batches != nil {
		if err := d.allocatePointerBatches(descriptions, batches); err != nil {
			return err
		}
		if err := d.allocateSliceBatches(descriptions, sliceBatches); err != nil {
			return err
		}
	}
	visiting := make([]bool, count+1)
	for id := 1; id <= count; id++ {
		if err := d.resolveAlias(id, descriptions, visiting, 0); err != nil {
			return err
		}
	}
	if d.framed {
		return d.readFramedBodies(descriptions)
	}
	for id := 1; id <= count; id++ {
		if descriptions[id].aliasIndex == 0 {
			if err := d.fillObject(id, descriptions[id]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *decoder) fillObject(id int, desc objectDescription) error {
	v := d.objects[id]
	_, length, _, ok := d.objectDetails(id, desc)
	if !ok {
		return fmt.Errorf("missing state object dimensions")
	}
	switch v.Kind() {
	case reflect.Pointer:
		if err := d.value(v.Elem()); err != nil {
			return err
		}
	case reflect.Slice:
		full, err := d.fullSlice(v)
		if err != nil {
			return err
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			data, err := d.take(uint64(v.Cap()))
			if err != nil {
				return err
			}
			copy(full.Bytes(), data)
			return nil
		}
		for i := 0; i < full.Len(); i++ {
			if err := d.value(full.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if length == 0 {
			return nil
		}
		entry, err := d.mapEntryScratch(v.Type())
		if err != nil {
			return err
		}
		key, value := entry.key, entry.value
		for i := 0; i < length; i++ {
			// Sparse values leave zero fields untouched. Clear both cells before
			// reuse; SetMapIndex copies the values into the destination map.
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
			v.SetMapIndex(key, value)
		}
		if v.Len() != length {
			return fmt.Errorf("duplicate state map key")
		}
	}
	return nil
}

func (d *decoder) resolveAlias(id int, descriptions []objectDescription, visiting []bool, depth int) error {
	if d.objects[id].IsValid() {
		return nil
	}
	if depth > 4096 || visiting[id] {
		return fmt.Errorf("cyclic state storage alias")
	}
	visiting[id] = true
	desc := descriptions[id]
	// Type IDs were checked before any description was stored. Keeping the ID
	// avoids retaining a reflected type interface for every graph object.
	typeID, length, capacity, ok := d.objectDetails(id, desc)
	if !ok || typeID == 0 || typeID > uint64(len(d.registry.types)) {
		return fmt.Errorf("missing or invalid state object dimensions")
	}
	t := d.registry.types[typeID-1]
	a, ok := d.aliases.get(desc.aliasIndex)
	if !ok {
		return fmt.Errorf("missing state allocation")
	}
	if err := d.resolveAlias(int(a.owner), descriptions, visiting, depth+1); err != nil {
		return err
	}
	owner := d.objects[a.owner]
	var value reflect.Value
	if owner.Kind() == reflect.Pointer {
		value = owner.Elem()
	} else if owner.Kind() == reflect.Slice {
		value = owner.Slice(0, owner.Cap())
	} else {
		return fmt.Errorf("invalid state alias base")
	}
	for _, step := range a.path {
		if step.element {
			if (value.Kind() != reflect.Array && value.Kind() != reflect.Slice) || step.index >= value.Len() {
				return fmt.Errorf("invalid state alias element")
			}
			value = value.Index(step.index)
		} else {
			if value.Kind() != reflect.Struct {
				return fmt.Errorf("invalid state alias field")
			}
			allowed := false
			for _, field := range d.registry.fields[value.Type()] {
				if field == step.index {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("unknown state alias field")
			}
			value = d.registry.access(value, step.index)
		}
	}
	var restored reflect.Value
	if t.Kind() == reflect.Pointer {
		if !value.CanAddr() || value.Addr().Type() != t {
			return fmt.Errorf("state alias pointer type mismatch")
		}
		restored = value.Addr()
	} else {
		if (value.Kind() != reflect.Array && value.Kind() != reflect.Slice) || value.Type().Elem() != t.Elem() || a.offset > value.Len() || capacity > value.Len()-a.offset {
			return fmt.Errorf("state alias slice range mismatch")
		}
		restored = value.Slice3(a.offset, a.offset+length, a.offset+capacity)
		if restored.Type() != t {
			if !restored.CanConvert(t) {
				return fmt.Errorf("state alias slice type mismatch")
			}
			restored = restored.Convert(t)
		}
	}
	// Copy the header rather than keeping a destination that its owner later fills.
	stable := reflect.New(t).Elem()
	stable.Set(restored)
	d.objects[id] = stable
	visiting[id] = false
	return nil
}

// Go may coalesce ordinary zero-size allocations. Give each encoded identity
// distinct nonzero backing storage unless the registry explicitly interns it.
func newStatePointer(t reflect.Type) reflect.Value {
	if t.Elem().Size() != 0 {
		return reflect.New(t.Elem())
	}
	backing := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: t.Elem()}, {Name: "Padding", Type: reflect.TypeFor[byte]()}})
	return reflect.New(backing).Elem().Field(0).Addr()
}

// These cells never become graph objects. Map insertion copies their values,
// and decoding a nested map reference does not recursively fill that map, so a
// pair can be reused across every map with this type during one decode.
type decodedMapEntry struct{ key, value reflect.Value }

func (d *decoder) mapEntryScratch(t reflect.Type) (decodedMapEntry, error) {
	if entry, ok := d.mapEntries[t]; ok {
		return entry, nil
	}
	if err := d.reserve(1, 128); err != nil {
		return decodedMapEntry{}, err
	}
	if err := d.reserve(1, t.Key().Size()); err != nil {
		return decodedMapEntry{}, err
	}
	if err := d.reserve(1, t.Elem().Size()); err != nil {
		return decodedMapEntry{}, err
	}
	if d.mapEntries == nil {
		d.mapEntries = make(map[reflect.Type]decodedMapEntry)
	}
	entry := decodedMapEntry{reflect.New(t.Key()).Elem(), reflect.New(t.Elem()).Elem()}
	d.mapEntries[t] = entry
	return entry, nil
}
