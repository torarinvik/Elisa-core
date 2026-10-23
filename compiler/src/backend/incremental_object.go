package backend

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// IncrementalObjectSession retains native object partitions across analyzed
// revisions. It owns bytes, never LLVM or semantic handles. This is an in-process
// cache, with optional compiler-namespaced disk persistence.
type IncrementalObjectSession struct {
	disk          *incrementalObjectDisk
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
	slotCounts    map[[32]byte]int
	historyGroups int
	stats         IncrementalObjectStats
}

type incrementalObjectEntry struct {
	bitcode, target string
	object          []byte
	lastUse         uint64
	slot            [32]byte
}

type IncrementalObjectStats struct {
	Builds, PartitionsEmitted, PartitionsReused, SerialFallbacks, Evictions uint64
	OptimizedModulesReused                                                  uint64
	OwnerMapBuilds                                                          uint64
	DiskPartitionsReused                                                    uint64
	IRTime, OwnerMapTime, PartitionTime, NativeTime, MergeTime              time.Duration
	// CloneTime includes restriction, dead-prototype pruning and verification.
	// BitcodeTime includes serialization and exact-key hashing, plus an owned-key
	// copy on misses.
	CloneTime, BitcodeTime            time.Duration
	RetainedBytes, RetainedPartitions int
}

// NativeObjectPartitions selects stable codegen units independently of worker
// concurrency. More units can reduce the amount of O3 work invalidated by an edit.
// The ordinary default stays one unit per CPU pending full-input qualification.
func NativeObjectPartitions() int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("ELISACORE_OBJECT_PARTITIONS"))); err == nil {
		return min(64, max(1, n))
	}
	return min(64, max(1, runtime.NumCPU()))
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
		object := s.disk.load(bitcode, target)
		if object != nil {
			s.retainObjectInMemory(string(bitcode), target, object, [32]byte{})
			s.recordStats(func(stats *IncrementalObjectStats) { stats.PartitionsReused++; stats.DiskPartitionsReused++ })
		}
		return object
	}
	s.clock++
	entry.lastUse = s.clock
	object := entry.object
	s.mu.Unlock()
	s.recordStats(func(stats *IncrementalObjectStats) { stats.PartitionsReused++ })
	return object
}

// Slot identity influences eviction only. Exact optimized bitcode and target
// remain the complete authority for a hit.
func incrementalObjectSlot(unit, target [32]byte, part int) [32]byte {
	var input [72]byte
	copy(input[:32], unit[:])
	binary.LittleEndian.PutUint64(input[32:40], uint64(part))
	copy(input[40:], target[:])
	return sha256.Sum256(input[:])
}

func (s *IncrementalObjectSession) retainObject(bitcode, target string, object []byte) {
	s.retainObjectForSlot(bitcode, target, object, [32]byte{})
}

func (s *IncrementalObjectSession) removeObjectLocked(key [32]byte) {
	old := s.entries[key]
	s.bytes -= len(old.bitcode) + len(old.target) + len(old.object)
	delete(s.entries, key)
	if old.slot != ([32]byte{}) {
		count := s.slotCounts[old.slot]
		if count == 2 {
			s.historyGroups--
		}
		if count <= 1 {
			delete(s.slotCounts, old.slot)
		} else {
			s.slotCounts[old.slot] = count - 1
		}
	}
}

func (s *IncrementalObjectSession) retainObjectForSlot(bitcode, target string, object []byte, slot [32]byte) {
	s.disk.store(bitcode, target, object)
	s.retainObjectInMemory(bitcode, target, object, slot)
}

func (s *IncrementalObjectSession) retainObjectInMemory(bitcode, target string, object []byte, slot [32]byte) {
	cost := len(bitcode) + len(target) + len(object)
	if cost > s.maxBytes {
		return
	}
	key := incrementalObjectKey(bitcode, target)
	s.mu.Lock()
	if s.entries[key] != nil {
		s.removeObjectLocked(key)
	}
	var evictions uint64
	// Preserve unique current slots before retaining extra revisions of a slot.
	// Spare capacity still keeps history for edit/revert reuse.
	for s.bytes+cost > s.maxBytes {
		var oldest, history [32]byte
		oldestUse, historyUse := ^uint64(0), ^uint64(0)
		foundHistory := false
		preferHistory := s.historyGroups > 0 || s.slotCounts[slot] > 0
		for candidate, entry := range s.entries {
			if entry.lastUse < oldestUse {
				oldest, oldestUse = candidate, entry.lastUse
			}
			if preferHistory && entry.slot != ([32]byte{}) && (entry.slot == slot || s.slotCounts[entry.slot] > 1) && entry.lastUse < historyUse {
				history, historyUse, foundHistory = candidate, entry.lastUse, true
			}
		}
		if foundHistory {
			oldest = history
		}
		s.removeObjectLocked(oldest)
		evictions++
	}
	s.clock++
	s.entries[key] = &incrementalObjectEntry{bitcode: bitcode, target: target, object: object, lastUse: s.clock, slot: slot}
	s.bytes += cost
	if slot != ([32]byte{}) {
		if s.slotCounts == nil {
			s.slotCounts = make(map[[32]byte]int)
		}
		if s.slotCounts[slot] == 1 {
			s.historyGroups++
		}
		s.slotCounts[slot]++
	}
	s.mu.Unlock()
	if evictions > 0 {
		s.recordStats(func(stats *IncrementalObjectStats) { stats.Evictions += evictions })
	}
}
