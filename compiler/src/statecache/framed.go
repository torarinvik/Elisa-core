package statecache

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// Keep one body offset for each fixed-size worker batch instead of an end for
// every object. Workers recover individual ends from the validated wire table.
const framedDecodeBatchSize = 64

// Keep restored dictionary strings in small immutable allocations rather than
// one allocation per dictionary entry. An entry larger than this limit remains
// an individual string so no arena allocation grows without bound.
const framedStringArenaChunkBytes = 1 << 20

func (d *decoder) readFramedBodies(descriptions []objectDescription) error {
	word, err := d.take(8)
	if err != nil {
		return err
	}
	dictionary := binary.LittleEndian.Uint64(word)
	count := len(descriptions) - 1
	table, err := d.take(uint64(count+1) * 8)
	if err != nil {
		return err
	}
	if dictionary < uint64(d.at) || dictionary > uint64(len(d.data)) {
		return fmt.Errorf("invalid state dictionary offset")
	}
	frameBatchCount := count / framedDecodeBatchSize
	if count%framedDecodeBatchSize != 0 {
		frameBatchCount++
	}
	if err := d.reserve(uint64(frameBatchCount), reflect.TypeFor[int]().Size()); err != nil {
		return err
	}
	frameStarts := make([]int, frameBatchCount)
	bodiesStart := d.at
	cursor := uint64(d.at)
	rootStart := bodiesStart
	for i := 0; i <= count; i++ {
		if i < count && i%framedDecodeBatchSize == 0 {
			frameStarts[i/framedDecodeBatchSize] = int(cursor)
		}
		if i == count {
			rootStart = int(cursor)
		}
		size := binary.LittleEndian.Uint64(table[i*8:])
		if size > dictionary-cursor {
			return fmt.Errorf("state frame exceeds bodies")
		}
		if i < count && descriptions[i+1].alias != nil && size != 0 {
			return fmt.Errorf("state alias has a body")
		}
		cursor += size
	}
	if cursor != dictionary {
		return fmt.Errorf("state frames do not fill bodies")
	}
	// Strings are copied into bounded immutable arenas and budgeted once, before
	// workers see the dictionary. No restored string retains a view into the
	// input payload.
	d.at = int(dictionary)
	n, err := d.count()
	if err != nil {
		return err
	}
	if err := d.readFramedStringDictionary(n); err != nil {
		return err
	}
	payload := d.data
	budget := &atomic.Uint64{}
	budget.Store(d.budget)
	d.sharedBudget = budget
	workers := 1
	if d.registry.parallelDecode && count >= 4096 {
		workers = min(6, runtime.GOMAXPROCS(0))
	}
	// Bound the worker cells, channels and scheduling metadata as well as graph storage.
	if err := d.reserve(uint64(workers), 1024); err != nil {
		return err
	}
	var next atomic.Uint64
	var cancelled atomic.Bool
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	run := func() {
		defer wg.Done()
		worker := decoder{registry: d.registry, objects: d.objects, wideObjects: d.wideObjects, strings: d.strings, framed: true, sharedBudget: budget}
		for {
			start := int(next.Add(framedDecodeBatchSize)) - framedDecodeBatchSize
			if start >= count || cancelled.Load() {
				return
			}
			frameStart := frameStarts[start/framedDecodeBatchSize]
			for i := start; i < min(start+framedDecodeBatchSize, count); i++ {
				frameEnd := frameStart + int(binary.LittleEndian.Uint64(table[i*8:]))
				if descriptions[i+1].alias == nil {
					worker.data = payload[frameStart:frameEnd]
					worker.at = 0
					if err := worker.fillObject(i+1, descriptions[i+1]); err != nil {
						cancelled.Store(true)
						errors <- err
						return
					}
					if worker.at != len(worker.data) {
						cancelled.Store(true)
						errors <- fmt.Errorf("trailing state object frame")
						return
					}
				}
				frameStart = frameEnd
			}
		}
	}
	wg.Add(workers)
	if workers == 1 {
		run()
	} else {
		for i := 0; i < workers; i++ {
			go run()
		}
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		return err
	}
	d.data = payload[:int(dictionary)]
	d.at = rootStart
	return nil
}

func (d *decoder) readFramedStringDictionary(n int) error {
	if err := d.reserve(uint64(n+1), reflect.TypeFor[string]().Size()); err != nil {
		return err
	}
	d.strings = make([]string, n+1)
	for first := 1; first <= n; {
		scan := *d
		chunkCount, chunkBytes := 0, 0
		for first+chunkCount <= n {
			size, err := scan.uint()
			if err != nil {
				return err
			}
			if size == 0 {
				return fmt.Errorf("empty state dictionary entry")
			}
			data, err := scan.take(size)
			if err != nil {
				return err
			}
			if len(data) > framedStringArenaChunkBytes {
				if chunkCount != 0 {
					break
				}
				// A single huge string already requires one contiguous allocation.
				// Keep it separate rather than letting it expand an arena.
				size, err = d.uint()
				if err != nil {
					return err
				}
				data, err = d.take(size)
				if err != nil {
					return err
				}
				if err := d.reserve(size, 1); err != nil {
					return err
				}
				d.strings[first] = string(data)
				first++
				break
			}
			if chunkCount != 0 && len(data) > framedStringArenaChunkBytes-chunkBytes {
				break
			}
			chunkCount++
			chunkBytes += len(data)
			if chunkBytes == framedStringArenaChunkBytes {
				break
			}
		}
		if chunkCount == 0 {
			continue
		}

		if err := d.reserve(uint64(chunkBytes), 1); err != nil {
			return err
		}
		var arena strings.Builder
		arena.Grow(chunkBytes)
		for i := 0; i < chunkCount; i++ {
			size, err := d.uint()
			if err != nil {
				return err
			}
			data, err := d.take(size)
			if err != nil {
				return err
			}
			start := arena.Len()
			_, _ = arena.Write(data)
			// String exposes an immutable view of the builder's current prefix.
			// Later writes append after this range, and Grow reserved the whole
			// chunk, so each substring remains stable without an offsets table.
			d.strings[first+i] = arena.String()[start:]
		}
		first += chunkCount
	}
	if d.at != len(d.data) {
		return fmt.Errorf("trailing state dictionary data")
	}
	return nil
}
