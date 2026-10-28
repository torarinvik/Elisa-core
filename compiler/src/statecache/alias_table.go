package statecache

import (
	"fmt"
	"unsafe"
)

const decodedAliasChunkSize = 1024

// decodedAliasTable uses bounded backing slabs so appending aliases never
// reallocates or copies earlier records. The final slab is limited by the
// maximum possible alias count; descriptors retain only stable indices.
type decodedAliasTable struct {
	chunks     [][]nodeAlias
	count      uint32
	maxAliases uint64
}

func (a *decodedAliasTable) append(d *decoder, alias nodeAlias) (uint32, error) {
	chunk := int(a.count) / decodedAliasChunkSize
	if chunk == len(a.chunks) {
		if len(a.chunks) == cap(a.chunks) {
			if a.maxAliases == 0 {
				return 0, fmt.Errorf("too many state aliases")
			}
			newCapacity := cap(a.chunks) * 2
			if newCapacity == 0 {
				newCapacity = 1
			}
			maxChunks := (a.maxAliases-1)/decodedAliasChunkSize + 1
			if uint64(newCapacity) > maxChunks {
				newCapacity = int(maxChunks)
			}
			if newCapacity <= cap(a.chunks) {
				return 0, fmt.Errorf("too many state aliases")
			}
			if err := d.reserve(uint64(newCapacity), unsafe.Sizeof([]nodeAlias{})); err != nil {
				return 0, err
			}
			grown := make([][]nodeAlias, len(a.chunks), newCapacity)
			copy(grown, a.chunks)
			a.chunks = grown
		}
		remaining := a.maxAliases - uint64(chunk*decodedAliasChunkSize)
		chunkLength := min(uint64(decodedAliasChunkSize), remaining)
		if chunkLength == 0 {
			return 0, fmt.Errorf("too many state aliases")
		}
		if err := d.reserve(chunkLength, unsafe.Sizeof(nodeAlias{})); err != nil {
			return 0, err
		}
		a.chunks = append(a.chunks, make([]nodeAlias, int(chunkLength)))
	}
	a.chunks[chunk][int(a.count)%decodedAliasChunkSize] = alias
	a.count++
	return a.count, nil
}

func (a *decodedAliasTable) get(index uint32) (*nodeAlias, bool) {
	if index == 0 || index > a.count {
		return nil, false
	}
	zero := int(index - 1)
	return &a.chunks[zero/decodedAliasChunkSize][zero%decodedAliasChunkSize], true
}

func (a *decodedAliasTable) len() uint64 { return uint64(a.count) }

func (a *decodedAliasTable) capacity() uint64 {
	var capacity uint64
	for _, chunk := range a.chunks {
		capacity += uint64(len(chunk))
	}
	return capacity
}
