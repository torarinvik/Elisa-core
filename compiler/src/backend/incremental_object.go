package backend

import (
	"crypto/sha256"
	"sync"
	"time"
)

// IncrementalObjectSession retains native object partitions across analyzed
// revisions. It owns bytes, never LLVM or semantic handles. This is an in-process
// cache; it does not trust externally supplied native artifacts.
type IncrementalObjectSession struct {
	mu         sync.Mutex // protects the bounded partition map and LRU metadata
	statsMu    sync.Mutex
	partitions int
	minWeight  int64
	maxBytes   int
	bytes      int
	clock      uint64
	entries    map[[32]byte]*incrementalObjectEntry
	stats      IncrementalObjectStats
}

type incrementalObjectEntry struct {
	bitcode, target string
	object          []byte
	lastUse         uint64
}

type IncrementalObjectStats struct {
	Builds, PartitionsEmitted, PartitionsReused, SerialFallbacks, Evictions uint64
	IRTime, PartitionTime, NativeTime, MergeTime                            time.Duration
	// CloneTime includes restriction, dead-prototype pruning and verification.
	// BitcodeTime includes serialization and copying the owned cache key.
	CloneTime, BitcodeTime            time.Duration
	RetainedBytes, RetainedPartitions int
}

// NewIncrementalObjectSession bounds the complete retained bitcode and objects.
// Zero values select eight partitions and a 256 MiB budget. Modules below
// 2,000 partitionable instructions use direct emission to avoid merge overhead.
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
	h := sha256.New()
	h.Write([]byte(target))
	h.Write([]byte{0})
	h.Write([]byte(bitcode))
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

func (s *IncrementalObjectSession) lookupObject(bitcode, target string) []byte {
	key := incrementalObjectKey(bitcode, target)
	s.mu.Lock()
	entry := s.entries[key]
	// Exact input comparison makes the digest an index, not reuse authority.
	if entry == nil || entry.bitcode != bitcode || entry.target != target {
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
