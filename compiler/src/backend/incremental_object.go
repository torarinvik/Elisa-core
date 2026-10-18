package backend

import (
	"crypto/sha256"
	"sync"
	"time"
	"unsafe"
)

// IncrementalObjectSession retains native object partitions across analyzed
// revisions. It owns bytes, never LLVM or semantic handles. This is an in-process
// cache; it does not trust externally supplied native artifacts.
type IncrementalObjectSession struct {
	mu            sync.Mutex // protects the bounded partition map and LRU metadata
	statsMu       sync.Mutex
	mergerOnce    sync.Once
	defaultMerger string
	partitions    int
	minWeight     int64
	maxBytes      int
	bytes         int
	clock         uint64
	entries       map[[32]byte]*incrementalObjectEntry
	stats         IncrementalObjectStats
}

type incrementalObjectEntry struct {
	bitcode, target string
	object          []byte
	lastUse         uint64
}

type IncrementalObjectStats struct {
	Builds, PartitionsEmitted, PartitionsReused, SerialFallbacks, Evictions uint64
	OwnerMapBuilds                                                          uint64
	IRTime, OwnerMapTime, PartitionTime, NativeTime, MergeTime              time.Duration
	// CloneTime includes restriction, dead-prototype pruning and verification.
	// BitcodeTime includes serialization and exact-key hashing, plus an owned-key
	// copy on misses.
	CloneTime, BitcodeTime            time.Duration
	RetainedBytes, RetainedPartitions int
}

// NewIncrementalObjectSession bounds the complete retained bitcode and objects.
// Zero values select eight partitions and a 256 MiB budget. Modules below
// 2,000 partitionable instructions use direct emission to avoid merge overhead.
// On macOS, the default merger toolchain is resolved once per session; explicit
// ELISA_LD overrides are read for each merge.
func NewIncrementalObjectSession(partitions, maxBytes int) *IncrementalObjectSession {
	if partitions <= 0 {
		partitions = 8
	}
	if partitions > 64 {
		partitions = 64
	}
	if maxBytes <= 0 {
		maxBytes = 256 << 20
	}
	return &IncrementalObjectSession{partitions: partitions, minWeight: 2000, maxBytes: maxBytes, entries: make(map[[32]byte]*incrementalObjectEntry)}
}

func (s *IncrementalObjectSession) Stats() IncrementalObjectStats {
	s.statsMu.Lock()
	stats := s.stats
	s.statsMu.Unlock()
	s.mu.Lock()
	stats.RetainedBytes, stats.RetainedPartitions = s.bytes, len(s.entries)
	s.mu.Unlock()
	return stats
}

func (s *IncrementalObjectSession) recordStats(update func(*IncrementalObjectStats)) {
	s.statsMu.Lock()
	update(&s.stats)
	s.statsMu.Unlock()
}

func incrementalObjectKey(bitcode, target string) [32]byte {
	return incrementalObjectKeyBytes(unsafe.Slice(unsafe.StringData(bitcode), len(bitcode)), target)
}

func incrementalObjectKeyBytes(bitcode []byte, target string) [32]byte {
	h := sha256.New()
	_, _ = h.Write(unsafe.Slice(unsafe.StringData(target), len(target)))
	separator := [1]byte{0}
	_, _ = h.Write(separator[:])
	_, _ = h.Write(bitcode)
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

func (s *IncrementalObjectSession) lookupObject(bitcode, target string) []byte {
	return s.lookupObjectBytes(unsafe.Slice(unsafe.StringData(bitcode), len(bitcode)), target)
}

// lookupObjectBytes borrows bitcode only for this call. In particular, callers may
// pass a view of an LLVM-owned memory buffer; the cache retains only its own copies.
func (s *IncrementalObjectSession) lookupObjectBytes(bitcode []byte, target string) []byte {
	key := incrementalObjectKeyBytes(bitcode, target)
	input := unsafe.String(unsafe.SliceData(bitcode), len(bitcode))
	s.mu.Lock()
	entry := s.entries[key]
	// Exact input comparison makes the digest an index, not reuse authority.
	if entry == nil || entry.bitcode != input || entry.target != target {
		s.mu.Unlock()
		return nil
	}
	s.clock++
	entry.lastUse = s.clock
	object := entry.object
	s.mu.Unlock()
	s.recordStats(func(stats *IncrementalObjectStats) { stats.PartitionsReused++ })
	return object
}

func (s *IncrementalObjectSession) retainObject(bitcode, target string, object []byte) {
	cost := len(bitcode) + len(target) + len(object)
	if cost > s.maxBytes {
		return
	}
	key := incrementalObjectKey(bitcode, target)
	s.mu.Lock()
	if old := s.entries[key]; old != nil {
		s.bytes -= len(old.bitcode) + len(old.target) + len(old.object)
		delete(s.entries, key)
	}
	var evictions uint64
	for s.bytes+cost > s.maxBytes {
		var oldest [32]byte
		oldestUse := ^uint64(0)
		for key, entry := range s.entries {
			if entry.lastUse < oldestUse {
				oldest, oldestUse = key, entry.lastUse
			}
		}
		old := s.entries[oldest]
		s.bytes -= len(old.bitcode) + len(old.target) + len(old.object)
		delete(s.entries, oldest)
		evictions++
	}
	s.clock++
	s.entries[key] = &incrementalObjectEntry{bitcode: bitcode, target: target, object: object, lastUse: s.clock}
	s.bytes += cost
	s.mu.Unlock()
	if evictions > 0 {
		s.recordStats(func(stats *IncrementalObjectStats) { stats.Evictions += evictions })
	}
}
