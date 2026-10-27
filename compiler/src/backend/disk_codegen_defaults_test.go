package backend

import (
	"os"
	"testing"
)

var diskPresetFlags = []string{"ELISACORE_PARALLEL_IR", "ELISACORE_PARALLEL_OPT", "ELISACORE_IR_IMPORTS", "ELISACORE_NATIVE_FRAGMENTS", "ELISACORE_OBJECT_PARTITIONS", "ELISACORE_PARALLEL_OPT_IMPORT"}

func unsetDiskPresetFlags(t *testing.T) {
	t.Helper()
	for _, name := range diskPresetFlags {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiskCodegenDefaultsKeepExplicitOverrides(t *testing.T) {
	unsetDiskPresetFlags(t)
	ordinary := NewIncrementalObjectSession(4, 1<<20)
	if ordinary.diskCodegenFlag("ELISACORE_PARALLEL_OPT") {
		t.Fatal("ordinary session gained default parallel optimization")
	}
	profile := NewIncrementalObjectSession(4, 1<<20)
	profile.minWeight = 0
	profile.UseDiskIncrementalDefaults()
	if profile.partitions != 32 {
		t.Fatal("disk profile did not select 32 post-O3 cache units")
	}
	for _, name := range diskPresetFlags[:2] {
		if profile.diskCodegenFlag(name) {
			t.Fatal("disk O3 profile implicitly enabled partitioned optimization", name)
		}
	}
	for _, name := range diskPresetFlags[2:4] {
		if !profile.diskCodegenFlag(name) {
			t.Fatal("missing disk reuse preset", name)
		}
	}
	result := parseAndAnalyzeBackendTest(t, "disk-defaults-o0.elisa", "def main() -> i64:\n    return 0\n")
	if !profile.partitionedIRApplies(result, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}) {
		t.Fatal("O0 disk profile lost the independent IR path")
	}
	if profile.partitionedIRApplies(result, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}) {
		t.Fatal("O3 disk profile enabled partitioned optimization by default")
	}
	for _, primary := range []string{"ELISACORE_PARALLEL_IR", "ELISACORE_PARALLEL_OPT"} {
		t.Run(primary, func(t *testing.T) {
			t.Setenv(primary, "0")
			s := NewIncrementalObjectSession(4, 1<<20)
			s.UseDiskIncrementalDefaults()
			if s.diskDefaultsActive() || s.partitions != 4 || s.diskCodegenFlag("ELISACORE_IR_IMPORTS") {
				t.Fatal("explicit primary disable did not opt out")
			}
		})
	}
	for _, primary := range []string{"ELISACORE_PARALLEL_IR", "ELISACORE_PARALLEL_OPT"} {
		t.Run(primary+"-partial-opt-in", func(t *testing.T) {
			t.Setenv(primary, "1")
			s := NewIncrementalObjectSession(4, 1<<20)
			s.UseDiskIncrementalDefaults()
			if s.diskDefaultsActive() || s.partitions != 4 || s.diskCodegenFlag("ELISACORE_IR_IMPORTS") || s.diskCodegenFlag("ELISACORE_NATIVE_FRAGMENTS") {
				t.Fatal("partial explicit opt-in gained automatic preset features")
			}
			for _, name := range diskPresetFlags[:2] {
				if s.diskCodegenFlag(name) != (name == primary) {
					t.Fatal("partial explicit opt-in changed legacy mode", name)
				}
			}
		})
	}
	t.Run("paired-explicit-opt-in", func(t *testing.T) {
		t.Setenv("ELISACORE_PARALLEL_IR", "1")
		t.Setenv("ELISACORE_PARALLEL_OPT", "1")
		s := NewIncrementalObjectSession(4, 1<<20)
		s.UseDiskIncrementalDefaults()
		if s.diskDefaultsActive() || s.partitions != 4 || s.diskCodegenFlag("ELISACORE_IR_IMPORTS") || s.diskCodegenFlag("ELISACORE_NATIVE_FRAGMENTS") {
			t.Fatal("explicit legacy pipeline gained preset features")
		}
	})
	for _, name := range []string{"ELISACORE_IR_IMPORTS", "ELISACORE_NATIVE_FRAGMENTS"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "0")
			if profile.diskCodegenFlag(name) {
				t.Fatal("explicit feature disable ignored")
			}
		})
	}
	t.Run("units", func(t *testing.T) {
		t.Setenv("ELISACORE_OBJECT_PARTITIONS", "4")
		s := NewIncrementalObjectSession(NativeObjectPartitions(), 1<<20)
		s.UseDiskIncrementalDefaults()
		if s.partitions != 4 {
			t.Fatal("explicit units ignored")
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Setenv("ELISACORE_PARALLEL_OPT", "")
		if profile.diskDefaultsActive() {
			t.Fatal("explicit empty flag was overwritten")
		}
	})
	t.Run("legacy-import", func(t *testing.T) {
		t.Setenv("ELISACORE_PARALLEL_OPT_IMPORT", "40")
		if profile.diskDefaultsActive() {
			t.Fatal("legacy import experiment was combined with preset")
		}
	})
}
