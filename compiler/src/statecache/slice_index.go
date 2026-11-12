package statecache

type indexedSlice struct {
	key identity
	id  uint64
}

// A backing address usually has one slice shape. Keep alternate lengths,
// capacities and types in a separate map so they retain distinct IDs.
type sliceIndex struct {
	entries []indexedSlice
	count   int
	aliases map[identity]uint64
}

func newSliceIndex(capacity int) sliceIndex {
	var result sliceIndex
	if capacity > 0 {
		result.grow(capacity)
	}
	return result
}

func (index *sliceIndex) slot(address uintptr) int {
	mask := uintptr(len(index.entries) - 1)
	hash := uint64(address)
	hash ^= hash >> 16
	hash *= 0x9e3779b97f4a7c15
	hash ^= hash >> 32
	for slot := uintptr(hash) & mask; ; slot = (slot + 1) & mask {
		entry := &index.entries[slot]
		if entry.id == 0 || entry.key.pointer == address {
			return int(slot)
		}
	}
}

func (index *sliceIndex) grow(capacity int) {
	size := 8
	for size*3/4 < capacity {
		size *= 2
	}
	old := index.entries
	index.entries = make([]indexedSlice, size)
	for _, entry := range old {
		if entry.id != 0 {
			index.entries[index.slot(entry.key.pointer)] = entry
		}
	}
}

func (index *sliceIndex) lookup(key identity) (uint64, int) {
	if len(index.entries) == 0 {
		return 0, -1
	}
	slot := index.slot(key.pointer)
	entry := &index.entries[slot]
	if entry.id == 0 || entry.key == key {
		return entry.id, slot
	}
	return index.aliases[key], slot
}

func (index *sliceIndex) add(key identity, id uint64, slot int) {
	if slot < 0 || index.entries[slot].id == 0 {
		if index.count >= len(index.entries)*3/4 {
			index.grow(index.count + 1)
			slot = index.slot(key.pointer)
		}
		index.entries[slot] = indexedSlice{key, id}
		index.count++
		return
	}
	if index.aliases == nil {
		index.aliases = make(map[identity]uint64)
	}
	index.aliases[key] = id
}
