package backend

import "testing"

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
