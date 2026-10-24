package statecache

import (
	"encoding/binary"
	"runtime"
	"sync"
	"sync/atomic"
)

// parallelFramedEncodeMin is the object count below which framed bodies are
// written serially; small graphs gain nothing from workers.
const parallelFramedEncodeMin = 4096

// framedEncodeChunk is the number of consecutive objects one worker encodes into
// one buffer.
const framedEncodeChunk = 256

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
	sizes := make([]uint64, count)
	interner := &stringInterner{}
	var next atomic.Int64
	var failed atomic.Bool
	var firstErr error
	var errOnce sync.Once
	var wg sync.WaitGroup
	workers := min(8, runtime.GOMAXPROCS(0))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker := &encoder{registry: e.registry, objects: e.objects, framed: true, interner: interner}
			for !failed.Load() {
				k := int(next.Add(1)) - 1
				if k >= chunks {
					return
				}
				worker.data.Reset()
				for i := k * framedEncodeChunk; i < min((k+1)*framedEncodeChunk, count); i++ {
					before := worker.data.Len()
					node := e.nodes.at(i)
					if node.alias == nil {
						if err := worker.body(node.value); err != nil {
							errOnce.Do(func() { firstErr = err })
							failed.Store(true)
							return
						}
					}
					sizes[i] = uint64(worker.data.Len() - before)
				}
				buffers[k] = append([]byte(nil), worker.data.Bytes()...)
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
	e.data.Grow(total)
	for _, b := range buffers {
		e.data.Write(b)
	}
	table := e.data.Bytes()
	for i, size := range sizes {
		binary.LittleEndian.PutUint64(table[framing+8*(i+1):], size)
	}
	// The root and later strings continue the shared dictionary.
	e.interner = interner
	return nil
}
