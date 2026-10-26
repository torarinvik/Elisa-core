//go:build cgo

package backend

import (
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
	if profile.partitionedIRApplies(probe, options) || profile.diskCodegenFlag("ELISACORE_PARALLEL_OPT") {
		t.Fatal("disk profile selected a pre-O3 partition path")
	}
	profileObject := filepath.Join(dir, "profile.o")
	if err := profile.WriteObject(probe, profileObject, options); err != nil {
		t.Fatal(err)
	}
	if stats := profile.Stats(); stats.NativeFragmentsEmitted != 0 || stats.PartitionsEmitted == 0 || stats.SerialFallbacks != 0 {
		t.Fatalf("disk O3 did not use post-optimization native partitions: %+v", stats)
	}
	if got := incrementalObjectNativeStatus(t, profileObject); got != 30 {
		t.Fatal("profile result", got)
	}
	repeat := NewIncrementalObjectSession(4, 32<<20)
	repeat.minWeight = 0
	repeat.UseDiskIncrementalDefaults()
	repeat.EnableDiskCache(cache, "same-emitter", 64<<20)
	repeatObject := filepath.Join(dir, "repeat.o")
	if err := repeat.WriteObject(parseAndAnalyzeBackendTest(t, "disk-defaults.elisa", source), repeatObject, options); err != nil {
		t.Fatal(err)
	}
	if stats := repeat.Stats(); stats.OptimizedModulesReused != 1 || stats.DiskPartitionsReused == 0 || stats.PartitionsEmitted != 0 {
		t.Fatalf("fresh disk-profile session did not reuse post-O3 artifacts: %+v", stats)
	}
	if got := incrementalObjectNativeStatus(t, repeatObject); got != 30 {
		t.Fatal("reused profile result", got)
	}

	for _, name := range []string{"ELISACORE_PARALLEL_IR", "ELISACORE_PARALLEL_OPT", "ELISACORE_IR_IMPORTS", "ELISACORE_NATIVE_FRAGMENTS"} {
		t.Setenv(name, "1")
	}
	explicit := NewIncrementalObjectSession(4, 32<<20)
	explicit.minWeight = 0
	explicit.EnableDiskCache(cache, "same-emitter", 64<<20)
	if !explicit.partitionedIRApplies(probe, options) {
		t.Fatal("explicit parallel IR/O3 experiment is no longer available")
	}
	explicitObject := filepath.Join(dir, "explicit.o")
	if err := explicit.WriteObject(parseAndAnalyzeBackendTest(t, "disk-defaults.elisa", source), explicitObject, options); err != nil {
		t.Fatal(err)
	}
	if got := incrementalObjectNativeStatus(t, explicitObject); got != 30 {
		t.Fatal("explicit experiment result", got)
	}
}
