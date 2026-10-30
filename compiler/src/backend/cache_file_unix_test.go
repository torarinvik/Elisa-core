//go:build darwin || freebsd || linux

package backend

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestDiskCacheFIFOsArePromptMisses(t *testing.T) {
	for _, schedule := range []bool{false, true} {
		name := "object"
		if schedule {
			name = "schedule"
		}
		t.Run(name, func(t *testing.T) {
			s := NewIncrementalObjectSession(2, 4096)
			s.EnableDiskCache(t.TempDir(), "compiler", 8192)
			unit, target := [32]byte{1}, [32]byte{2}
			path := s.disk.path([]byte("ir"), "target")
			load := func() bool { return s.disk.load([]byte("ir"), "target") == nil }
			if schedule {
				path = s.disk.schedulePath(unit, target, 2)
				load = func() bool { return s.disk.loadSchedule(unit, target, 2) == nil }
			}
			if err := os.MkdirAll(s.disk.root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			finished := make(chan bool, 1)
			go func() { finished <- load() }()
			select {
			case miss := <-finished:
				if !miss {
					t.Fatal("FIFO accepted as cache entry")
				}
			case <-time.After(time.Second):
				// Release the old blocking implementation before reporting failure, so
				// this regression itself cannot leave a reader goroutine hanging.
				fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
				if err == nil {
					_, _ = syscall.Write(fd, make([]byte, 64))
					_ = syscall.Close(fd)
				}
				select {
				case <-finished:
				case <-time.After(time.Second):
				}
				t.Fatal("cache load blocked on a FIFO")
			}
		})
	}
}

func TestDiskCacheSymlinksAreMisses(t *testing.T) {
	s := NewIncrementalObjectSession(2, 4096)
	s.EnableDiskCache(t.TempDir(), "compiler", 8192)
	s.disk.store("ir", "target", []byte("object"))
	object := s.disk.path([]byte("ir"), "target")
	unit, target := [32]byte{1}, [32]byte{2}
	s.disk.storeSchedule(unit, target, [][]string{{"first"}, {"second"}})
	for _, path := range []string{object, s.disk.schedulePath(unit, target, 2)} {
		if err := os.Rename(path, path+".real"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path+".real", path); err != nil {
			t.Fatal(err)
		}
	}
	if s.disk.load([]byte("ir"), "target") != nil {
		t.Fatal("followed object cache symlink")
	}
	if s.disk.loadSchedule(unit, target, 2) != nil {
		t.Fatal("followed schedule cache symlink")
	}
}
