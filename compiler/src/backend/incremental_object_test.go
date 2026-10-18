package backend

import (
	"fmt"
	"sync"
	"testing"
)

func TestIncrementalObjectExactInputsAndEviction(t *testing.T) {
	s := NewIncrementalObjectSession(2, 100)
	object := []byte("012345678901234567890123456789")
	s.retainObject("first", "target", object)
	s.retainObject("second", "target", object)
	if s.lookupObject("first", "other") != nil || s.lookupObject("changed", "target") != nil {
		t.Fatal("nonmatching emission inputs reused native code")
	}
	if s.lookupObject("first", "target") == nil {
		t.Fatal("exact current inputs missed")
	}
	s.retainObject("third", "target", object)
	if s.lookupObject("second", "target") != nil || s.lookupObject("first", "target") == nil {
		t.Fatal("retention did not evict the least recently used artifact")
	}
	if s.bytes > 100 || s.stats.Evictions != 1 {
		t.Fatalf("unbounded retention: %+v", s.Stats())
	}
	// A digest collision alone never authorizes replay of different native code.
	key := incrementalObjectKey("new", "target")
	s.entries[key] = &incrementalObjectEntry{bitcode: "old", target: "target", object: object}
	if s.lookupObject("new", "target") != nil {
		t.Fatal("digest collision authorized a different partition")
	}
}

func TestIncrementalObjectEditPreservesOtherCurrentSlot(t *testing.T) {
	object := []byte("object")
	cost := len("unit-A/old") + len("target") + len(object)
	s := NewIncrementalObjectSession(2, 2*cost)
	s.retainObjectForSlot("unit-A/old", "target", object, [32]byte{1})
	s.retainObjectForSlot("unit-B/old", "target", object, [32]byte{2})
	s.retainObjectForSlot("unit-B/new", "target", object, [32]byte{2})
	if s.lookupObject("unit-A/old", "target") == nil {
		t.Fatal("one slot edit evicted unrelated current partition")
	}
	if s.lookupObject("unit-B/new", "target") == nil {
		t.Fatal("new revision missing")
	}
	if s.bytes > s.maxBytes {
		t.Fatal("cache byte limit exceeded")
	}
}

func TestIncrementalObjectKeepsHistoryWhenBudgetFits(t *testing.T) {
	object := []byte("object")
	cost := len("unit-A/old") + len("target") + len(object)
	s := NewIncrementalObjectSession(2, 3*cost)
	s.retainObjectForSlot("unit-A/old", "target", object, [32]byte{1})
	s.retainObjectForSlot("unit-A/new", "target", object, [32]byte{1})
	s.retainObjectForSlot("unit-B/old", "target", object, [32]byte{2})
	if s.lookupObject("unit-A/old", "target") == nil || s.lookupObject("unit-A/new", "target") == nil {
		t.Fatal("spare capacity discarded exact revert history")
	}
	if s.lookupObject("unit-A/bad", "target") != nil {
		t.Fatal("slot hint authorized nonmatching input")
	}
	s.retainObjectForSlot("unit-C/new", "target", object, [32]byte{3})
	if s.lookupObject("unit-A/old", "target") != nil || s.lookupObject("unit-A/new", "target") == nil || s.lookupObject("unit-B/old", "target") == nil {
		t.Fatal("eviction did not prefer history over older unique slot")
	}
}

func TestIncrementalObjectConcurrentSlotEvictionAccounting(t *testing.T) {
	s := NewIncrementalObjectSession(4, 512)
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for revision := 0; revision < 128; revision++ {
				body := fmt.Sprintf("unit%d/revision%03d", worker, revision)
				s.retainObjectForSlot(body, "target", []byte("object"), [32]byte{byte(worker + 1)})
				_ = s.lookupObject(body, "target")
			}
		}(worker)
	}
	workers.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := map[[32]byte]int{}
	bytes := 0
	for _, entry := range s.entries {
		bytes += len(entry.bitcode) + len(entry.target) + len(entry.object)
		if entry.slot != ([32]byte{}) {
			counts[entry.slot]++
		}
	}
	groups := 0
	for _, count := range counts {
		if count > 1 {
			groups++
		}
	}
	if bytes != s.bytes || bytes > s.maxBytes || groups != s.historyGroups || len(counts) != len(s.slotCounts) {
		t.Fatal("slot accounting diverged from retained entries")
	}
	for slot, count := range counts {
		if s.slotCounts[slot] != count {
			t.Fatal("slot count mismatch")
		}
	}
}
