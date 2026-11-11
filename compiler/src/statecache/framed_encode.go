package statecache

import (
	"encoding/binary"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
)

// parallelFramedEncodeMin is the object count below which framed bodies are
// written serially; small graphs gain nothing from workers.
const parallelFramedEncodeMin = 4096

// framedEncodeChunk is the number of consecutive objects one worker encodes into
// one buffer.
const framedEncodeChunk = 256

// Workers give completed chunks their own portion of a slab so joining the
// chunks requires only the final copy into the checkpoint.
const maxFramedEncodeSlab = 256 << 10

// stringInterner assigns framed dictionary IDs concurrently. IDs stay dense and
// the table is ordered by ID, which is all the decoder requires.
type stringInterner struct {
	ids   sync.Map // string -> uint64
	mu    sync.Mutex
	table []string
}

func (s *stringInterner) id(v string) uint64 {
	if id, ok := s.ids.Load(v); ok {
		return id.(uint64)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.ids.Load(v); ok {
		return id.(uint64)
	}
	s.table = append(s.table, v)
	id := uint64(len(s.table))
	s.ids.Store(v, id)
	return id
}

// writeFramedBodies encodes every object body, one frame each, after the frame
// table at framing. Bodies only read the finished object inventory, so chunks are
// encoded in parallel into separate buffers and appended in object order.
func (e *encoder) writeFramedBodies(framing int) error {
	count := e.nodes.count
	chunks := (count + framedEncodeChunk - 1) / framedEncodeChunk
	buffers := make([][]byte, chunks)
	// The frame table is already reserved in e.data. Each body worker owns a
	// disjoint range of these slots, and e.data is not otherwise modified until
	// all workers finish, so write body lengths there directly instead of making
	// and later copying a second per-object size array.
	bodySizes := e.data[framing+8 : framing+8*(count+1)]
	interner := &stringInterner{}
	var next atomic.Int64
	var failed atomic.Bool
	var firstErr error
	var errOnce sync.Once
	var wg sync.WaitGroup
	workers := min(8, runtime.GOMAXPROCS(0))
	slabSize := min(maxFramedEncodeSlab, count/workers*8)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker := &encoder{registry: e.registry, objects: e.objects, pointerObjects: e.pointerObjects, framed: true, interner: interner}
			var remaining []byte
			var previousSize int
			for !failed.Load() {
				k := int(next.Add(1)) - 1
				if k >= chunks {
					return
				}
				if cap(remaining) < min(slabSize, max(1, previousSize)) {
					remaining = make([]byte, 0, slabSize)
				}
				worker.data = remaining
				for i := k * framedEncodeChunk; i < min((k+1)*framedEncodeChunk, count); i++ {
					before := len(worker.data)
					node := e.nodes.at(i)
					if node.alias == nil {
						if err := worker.body(node.value); err != nil {
							errOnce.Do(func() { firstErr = err })
							failed.Store(true)
							return
						}
					}
					binary.LittleEndian.PutUint64(bodySizes[8*i:], uint64(len(worker.data)-before))
				}
				previousSize = len(worker.data)
				buffers[k] = worker.data[:previousSize:previousSize]
				// The published prefix stays immutable even if the next
				// chunk appends into the tail of this same allocation.
				remaining = worker.data[previousSize:]
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	total := 0
	for _, b := range buffers {
		total += len(b)
	}
	e.data = slices.Grow(e.data, total)
	for _, b := range buffers {
		e.data = append(e.data, b...)
	}
	// The root and later strings continue the shared dictionary.
	e.interner = interner
	return nil
}
