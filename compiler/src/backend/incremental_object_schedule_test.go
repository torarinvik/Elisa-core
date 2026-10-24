package backend

import (
	"os"
	"reflect"
	"testing"
)

func TestPartitionSchedulePersistsAndRejectsBadHints(t *testing.T) {
	root := t.TempDir()
	s := NewIncrementalObjectSession(2, 1<<20)
	s.EnableDiskCache(root, "compiler-a", 1<<20)
	unit, target := [32]byte{1}, [32]byte{2}
	s.disk.storeSchedule(unit, target, [][]string{{"generic", "generic", "plain"}, {"other"}})
	fresh := NewIncrementalObjectSession(2, 1<<20)
	fresh.EnableDiskCache(root, "compiler-a", 1<<20)
	want := [][]string{{"generic", "plain"}, {"other"}}
	if got := fresh.disk.loadSchedule(unit, target, 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("separate session: %v", got)
	}
	for _, bad := range []struct {
		unit, target [32]byte
		parts        int
	}{{[32]byte{3}, target, 2}, {unit, [32]byte{3}, 2}, {unit, target, 3}} {
		if got := fresh.disk.loadSchedule(bad.unit, bad.target, bad.parts); got != nil {
			t.Fatal("different scheduling configuration reused hints")
		}
	}
	fresh.EnableDiskCache(root, "compiler-b", 1<<20)
	if fresh.disk.loadSchedule(unit, target, 2) != nil {
		t.Fatal("cross-compiler hints")
	}
	path := s.disk.schedulePath(unit, target, 2)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if s.disk.loadSchedule(unit, target, 2) != nil {
		t.Fatal("corrupt hints accepted")
	}
	if err := os.WriteFile(path, make([]byte, partitionScheduleLimit+1), 0600); err != nil {
		t.Fatal(err)
	}
	if s.disk.loadSchedule(unit, target, 2) != nil {
		t.Fatal("oversized hints accepted")
	}
	var disabled *incrementalObjectDisk
	disabled.storeSchedule(unit, target, want)
	if disabled.loadSchedule(unit, target, 2) != nil {
		t.Fatal("disabled persistence")
	}
}
