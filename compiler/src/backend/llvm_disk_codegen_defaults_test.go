//go:build cgo

package backend

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskCodegenDefaultsMatchExplicitCacheAuthority(t *testing.T) {
	unsetDiskPresetFlags(t)
	t.Setenv("ELISACORE_TEST_NATIVE_WORKERS", "1")
	previous := partitionWorkersAvailable
	RunPartitionWorkerIfRequested()
	t.Cleanup(func() { partitionWorkersAvailable = previous })
	t.Setenv("ELISACORE_OBJECT_PARTITIONS", "4")
	source := incrementalObjectFixture(8)
	options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}
	ordinary := NewIncrementalObjectSession(4, 32<<20)
	ordinary.minWeight = 0
	probe := parseAndAnalyzeBackendTest(t, "disk-defaults.elisa", source)
	if ordinary.partitionedIRApplies(probe, options) || parallelOptEnabled() {
		t.Fatal("ordinary backend entry point changed")
	}
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	profile := NewIncrementalObjectSession(4, 32<<20)
	profile.minWeight = 0
	profile.UseDiskIncrementalDefaults()
	profile.EnableDiskCache(cache, "same-emitter", 64<<20)
	if !profile.partitionedIRApplies(probe, options) {
		t.Fatal("disk profile did not select independent units")
	}
	profileObject := filepath.Join(dir, "profile.o")
	if err := profile.WriteObject(probe, profileObject, options); err != nil {
		t.Fatal(err)
	}
	if profile.Stats().NativeFragmentsEmitted == 0 {
		t.Fatal("default fragment path was not exercised")
	}
	if got := incrementalObjectNativeStatus(t, profileObject); got != 30 {
		t.Fatal("profile result", got)
	}
	for _, name := range []string{"ELISACORE_PARALLEL_IR", "ELISACORE_PARALLEL_OPT", "ELISACORE_IR_IMPORTS", "ELISACORE_NATIVE_FRAGMENTS"} {
		t.Setenv(name, "1")
	}
	explicit := NewIncrementalObjectSession(4, 32<<20)
	explicit.minWeight = 0
	explicit.EnableDiskCache(cache, "same-emitter", 64<<20)
	explicitObject := filepath.Join(dir, "explicit.o")
	if err := explicit.WriteObject(parseAndAnalyzeBackendTest(t, "disk-defaults.elisa", source), explicitObject, options); err != nil {
		t.Fatal(err)
	}
	if explicit.Stats().DiskPartitionsReused != 4 || explicit.Stats().PartitionsEmitted != 0 {
		t.Fatalf("effective pipeline authority diverged: %+v", explicit.Stats())
	}
	a, err := os.ReadFile(profileObject)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(explicitObject)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("preset/explicit native bytes differ")
	}
}
