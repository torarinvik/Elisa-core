package backend

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestObjectDiskCacheSurvivesSessionAndRejectsChanges(t *testing.T) {
	root := t.TempDir()
	session := func(identity string) *IncrementalObjectSession {
		s := NewIncrementalObjectSession(2, 4096)
		s.EnableDiskCache(root, identity, 8192)
		return s
	}
	first := session("compiler-a")
	first.retainObject("bitcode", "cpu=a", []byte("native object"))
	second := session("compiler-a")
	if got := second.lookupObject("bitcode", "cpu=a"); !bytes.Equal(got, []byte("native object")) {
		t.Fatalf("disk miss: %q", got)
	}
	if second.Stats().DiskPartitionsReused != 1 {
		t.Fatal("no disk reuse recorded")
	}
	if got := second.lookupObject("edited bitcode", "cpu=a"); got != nil {
		t.Fatal("source edit reused old native code")
	}
	if got := second.lookupObject("bitcode", "cpu=b"); got != nil {
		t.Fatal("target edit reused old native code")
	}
	if session("compiler-b").lookupObject("bitcode", "cpu=a") != nil {
		t.Fatal("compiler change reused native code")
	}
	// A disk hit must own its IR rather than retain a borrowed LLVM buffer.
	borrowed := []byte("bitcode")
	third := session("compiler-a")
	if third.lookupObjectBytes(borrowed, "cpu=a") == nil {
		t.Fatal("disk miss")
	}
	borrowed[0] = 'X'
	if third.lookupObject("bitcode", "cpu=a") == nil {
		t.Fatal("borrowed buffer corrupted memory entry")
	}
}

func TestObjectDiskCacheCorruptionAndBounds(t *testing.T) {
	s := NewIncrementalObjectSession(2, 4096)
	s.EnableDiskCache(t.TempDir(), "compiler", 1024)
	path := s.disk.path([]byte("ir"), "target")
	for _, offset := range []int{0, 8, 24, 32, 64, 66, 72} {
		s.disk.store("ir", "target", []byte("object"))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data[offset] ^= 0xff
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if s.disk.load([]byte("ir"), "target") != nil {
			t.Fatalf("corruption accepted at byte %d", offset)
		}
	}
	s.disk.store("ir", "target", []byte("object"))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("extra"))
	_ = f.Close()
	if s.disk.load([]byte("ir"), "target") != nil {
		t.Fatal("trailing data accepted")
	}
	s.disk.store("huge", "target", make([]byte, 1024))
	if s.disk.load([]byte("huge"), "target") != nil {
		t.Fatal("oversized entry published")
	}
}

func TestObjectDiskCacheConcurrentPublicationAndEviction(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := NewIncrementalObjectSession(2, 4096)
			s.EnableDiskCache(root, "compiler", 256)
			for j := 0; j < 8; j++ {
				s.disk.store("ir", "target", []byte("object"))
			}
		}()
	}
	wg.Wait()
	s := NewIncrementalObjectSession(2, 4096)
	s.EnableDiskCache(root, "compiler", 256)
	if s.disk.load([]byte("ir"), "target") == nil {
		t.Fatal("concurrent publication left incomplete entry")
	}
	for _, ir := range []string{"a", "b", "c", "d", "e"} {
		s.disk.store(ir, "target", []byte("object"))
	}
	entries, _ := os.ReadDir(s.disk.root)
	var size int64
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".part" {
			info, _ := entry.Info()
			size += info.Size()
		}
	}
	if size > 256 {
		t.Fatalf("disk budget exceeded: %d", size)
	}
}

func TestDiskLookupSuppliedKeyStillChecksExactInput(t *testing.T) {
	s := NewIncrementalObjectSession(2, 4096)
	s.EnableDiskCache(t.TempDir(), "compiler", 8192)
	s.disk.store("original", "target", []byte("object"))
	key := incrementalObjectKeyBytes([]byte("original"), "target")
	if got := s.disk.loadKey([]byte("original"), "target", key); !bytes.Equal(got, []byte("object")) {
		t.Fatal("matching disk entry lost")
	}
	if got := s.disk.loadKey([]byte("modified"), "target", key); got != nil {
		t.Fatal("digest selected an entry with different bitcode")
	}
	if got := s.disk.loadKey([]byte("original"), "targex", key); got != nil {
		t.Fatal("digest selected an entry with different target")
	}
}

func TestDiskInputComparisonAcrossChunks(t *testing.T) {
	original := bytes.Repeat([]byte("01234567"), 9000)
	chunk := make([]byte, 32*1024)
	if !diskInputEquals(bytes.NewReader(original), original, chunk) {
		t.Fatal("equal multi-chunk input rejected")
	}
	changed := append([]byte(nil), original...)
	changed[32*1024+3] ^= 1
	if diskInputEquals(bytes.NewReader(changed), original, chunk) {
		t.Fatal("mismatch in later chunk accepted")
	}
	if diskInputEquals(bytes.NewReader(original[:len(original)-1]), original, chunk) {
		t.Fatal("truncated input accepted")
	}
	if !diskInputEquals(bytes.NewReader(nil), nil, nil) {
		t.Fatal("empty input rejected")
	}
	if diskInputEquals(bytes.NewReader(original), original, nil) {
		t.Fatal("nonempty input accepted without a comparison buffer")
	}
}
