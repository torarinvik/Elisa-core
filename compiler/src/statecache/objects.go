package statecache

import (
	"fmt"
	"reflect"
)

type objectDescription struct {
	typeID           uint64
	length, capacity int
	alias            *nodeAlias
}

func (d *decoder) readObjects() error {
	count, err := d.count()
	if err != nil {
		return err
	}
	if err := d.reserve(uint64(count), 128); err != nil {
		return err
	}
	descriptions := make([]objectDescription, count+1)
	d.objects = make([]reflect.Value, count+1)
	// The 128-byte per-object metadata reservation covers descriptions,
	// references, alias bookkeeping and these type-indexed batch headers.
	// Only large graphs with sufficient per-object slack use batching.
	var batches []pointerBatch
	if count >= 4096 && len(d.registry.types) <= count/8 {
		batches = make([]pointerBatch, len(d.registry.types))
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
		desc := objectDescription{typeID: typeID}
		if t.Kind() != reflect.Pointer {
			desc.length, err = d.count()
			if err != nil {
				return err
			}
		}
		if t.Kind() == reflect.Slice {
			desc.capacity, err = d.count()
			if err != nil {
				return err
			}
			if desc.length > desc.capacity {
				return fmt.Errorf("invalid state slice capacity")
			}
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
			alias := &nodeAlias{owner: owner}
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
			desc.alias = alias
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
				if err := d.reserve(uint64(desc.length), t.Key().Size()+t.Elem().Size()+32); err != nil {
					return err
				}
				v = reflect.MakeMapWithSize(t, desc.length)
			case reflect.Slice:
				if err := d.reserve(uint64(desc.capacity), t.Elem().Size()); err != nil {
					return err
				}
				v = reflect.MakeSlice(t, desc.length, desc.capacity)
			}
			d.objects[id] = v
		}
		descriptions[id] = desc
	}
	if batches != nil {
		d.allocatePointerBatches(descriptions, batches)
	}
	visiting := make([]bool, count+1)
	for id := 1; id <= count; id++ {
		if err := d.resolveAlias(id, descriptions, visiting, 0); err != nil {
			return err
		}
	}
	for id := 1; id <= count; id++ {
		desc := descriptions[id]
		if desc.alias != nil {
			continue
		}
		v := d.objects[id]
		switch v.Kind() {
		case reflect.Pointer:
			if err := d.value(v.Elem()); err != nil {
				return err
			}
		case reflect.Slice:
			full := v.Slice(0, v.Cap())
			if v.Type().Elem().Kind() == reflect.Uint8 {
				data, err := d.take(uint64(v.Cap()))
				if err != nil {
					return err
				}
				copy(full.Bytes(), data)
				continue
			}
			for i := 0; i < full.Len(); i++ {
				if err := d.value(full.Index(i)); err != nil {
					return err
				}
			}
		case reflect.Map:
			for i := 0; i < desc.length; i++ {
				key := reflect.New(v.Type().Key()).Elem()
				value := reflect.New(v.Type().Elem()).Elem()
				if err := d.value(key); err != nil {
					return err
				}
				if err := d.value(value); err != nil {
					return err
				}
				if !key.Comparable() {
					return fmt.Errorf("noncomparable state map key")
				}
				if v.MapIndex(key).IsValid() {
					return fmt.Errorf("duplicate state map key")
				}
				v.SetMapIndex(key, value)
			}
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
	t := d.registry.types[desc.typeID-1]
	a := desc.alias
	if a == nil {
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
		if (value.Kind() != reflect.Array && value.Kind() != reflect.Slice) || value.Type().Elem() != t.Elem() || a.offset > value.Len() || desc.capacity > value.Len()-a.offset {
			return fmt.Errorf("state alias slice range mismatch")
		}
		restored = value.Slice3(a.offset, a.offset+desc.length, a.offset+desc.capacity)
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
