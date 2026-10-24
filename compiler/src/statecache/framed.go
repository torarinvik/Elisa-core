package statecache

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
)

// Frames are contiguous, so the previous end also supplies the next start.
// Keeping only ends avoids a redundant machine word for every graph object.
type stateFrame struct{ end int }

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
	if err := d.reserve(uint64(count+1), reflect.TypeFor[stateFrame]().Size()); err != nil {
		return err
	}
	frames := make([]stateFrame, count+1)
	bodiesStart := d.at
	cursor := uint64(d.at)
	for i := range frames {
		size := binary.LittleEndian.Uint64(table[i*8:])
		if size > dictionary-cursor {
			return fmt.Errorf("state frame exceeds bodies")
		}
		if i < count && descriptions[i+1].alias != nil && size != 0 {
			return fmt.Errorf("state alias has a body")
		}
		frames[i] = stateFrame{int(cursor + size)}
		cursor += size
	}
	if cursor != dictionary {
		return fmt.Errorf("state frames do not fill bodies")
	}
	// Strings are copied and budgeted once, before workers see the immutable
	// dictionary. No restored string retains a view into the input payload.
	d.at = int(dictionary)
	n, err := d.count()
	if err != nil {
		return err
	}
	if err := d.reserve(uint64(n+1), reflect.TypeFor[string]().Size()); err != nil {
		return err
	}
	d.strings = make([]string, n+1)
	for i := 1; i <= n; i++ {
		size, err := d.uint()
		if err != nil {
			return err
		}
		if size == 0 {
			return fmt.Errorf("empty state dictionary entry")
		}
		data, err := d.take(size)
		if err != nil {
			return err
		}
		if err := d.reserve(size, 1); err != nil {
			return err
		}
		d.strings[i] = string(data)
	}
	if d.at != len(d.data) {
		return fmt.Errorf("trailing state dictionary data")
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
		worker := decoder{registry: d.registry, objects: d.objects, strings: d.strings, framed: true, sharedBudget: budget}
		for {
			start := int(next.Add(64)) - 64
			if start >= count || cancelled.Load() {
				return
			}
			for i := start; i < min(start+64, count); i++ {
				if descriptions[i+1].alias != nil {
					continue
				}
				frame := frames[i]
				frameStart := bodiesStart
				if i > 0 {
					frameStart = frames[i-1].end
				}
				worker.data = payload[frameStart:frame.end]
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
	root := frames[count]
	d.data = payload[:root.end]
	d.at = bodiesStart
	if count > 0 {
		d.at = frames[count-1].end
	}
	return nil
}
