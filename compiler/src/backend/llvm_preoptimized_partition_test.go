//go:build cgo

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreoptimizedPartitionsPersistAcrossProcesses(t *testing.T) {
	if os.Getenv("ELISA_TEST_PREOPT_CHILD") == "1" {
		t.Setenv("ELISACORE_PARALLEL_OPT", "1")
		t.Setenv("ELISACORE_PARALLEL_IR", "0")
		if os.Getenv("ELISA_TEST_PREOPT_IR") == "1" {
			t.Setenv("ELISACORE_PARALLEL_IR", "1")
		}
		source := incrementalObjectFixture(32)
		revision := os.Getenv("ELISA_TEST_PREOPT_REVISION")
		want := 54
		if revision == "body" {
			source = strings.Replace(source, "return value + 1\n", "return value + 2\n", 1)
			want = 55
		}
		if revision == "lines" {
			source = "\n\n" + source
		}
		result := parseAndAnalyzeBackendTest(t, "preoptimized.elisa", source)
		session := NewIncrementalObjectSession(4, 32<<20)
		session.minWeight = 0
		session.EnableDiskCache(os.Getenv("ELISA_TEST_PREOPT_ROOT"), "test-executable", 64<<20)
		out := filepath.Join(t.TempDir(), "partitioned.o")
		options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}
		if revision == "O2" {
			options.OptLevel = OptimizationLevel2
		}
		if err := session.WriteObject(result, out, options); err != nil {
			t.Fatal(err)
		}
		stats := session.Stats()
		if os.Getenv("ELISA_TEST_PREOPT_IR") == "1" && stats.OwnerMapBuilds != 0 {
			t.Fatalf("partitioned IR unexpectedly declined: %+v", stats)
		}
		expected := os.Getenv("ELISA_TEST_PREOPT_EMIT")
		if expected == "4" && stats.PartitionsEmitted != 4 || expected == "1" && stats.PartitionsEmitted != 1 || expected == "0" && stats.PartitionsEmitted != 0 {
			t.Fatalf("expected %s newly optimized partitions: %+v", expected, stats)
		}
		if stats.OptimizedModulesReused != 0 || stats.SerialFallbacks != 0 || stats.PartitionsEmitted+stats.DiskPartitionsReused != 4 {
			t.Fatalf("did not exercise pre-O3 disk reuse: %+v", stats)
		}
		if got := incrementalObjectNativeStatus(t, out); got != want {
			t.Fatalf("partitioned native=%d want=%d", got, want)
		}
		cold := filepath.Join(t.TempDir(), "cold.o")
		options.Serial = true
		if err := WriteLLVMObjectFileWithOptions(result, cold, options); err != nil {
			t.Fatal(err)
		}
		if got := incrementalObjectNativeStatus(t, cold); got != want {
			t.Fatalf("cold native=%d want=%d", got, want)
		}
		return
	}
	root := t.TempDir()
	for _, revision := range []struct{ name, emitted string }{{"base", "4"}, {"repeat", "0"}, {"body", "1"}, {"body", "0"}, {"lines", "0"}, {"base", "0"}, {"O2", "4"}, {"O2", "0"}, {"base", "0"}} {
		t.Run(revision.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestPreoptimizedPartitionsPersistAcrossProcesses$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "ELISACORE_TEST_NATIVE_WORKERS=1", "ELISA_TEST_PREOPT_CHILD=1", "ELISA_TEST_PREOPT_ROOT="+root, "ELISA_TEST_PREOPT_REVISION="+revision.name, "ELISA_TEST_PREOPT_EMIT="+revision.emitted)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}

func TestOptimizedPartitionedIRAcrossProcesses(t *testing.T) {
	t.Setenv("ELISA_TEST_PREOPT_IR", "1")
	TestPreoptimizedPartitionsPersistAcrossProcesses(t)
}

func TestOptimizedPartitionedIRAdmission(t *testing.T) {
	previous := partitionWorkersAvailable
	defer func() { partitionWorkersAvailable = previous }()
	result := parseAndAnalyzeBackendTest(t, "ir_admission.elisa", "def main() -> i64:\n    return 0\n")
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	for _, tc := range []struct {
		name, ir, opt, imports string
		level                  OptimizationLevel
		workers, want          bool
	}{
		{"default-O3", "", "1", "0", OptimizationLevel3, true, false},
		{"explicit-O3", "1", "1", "0", OptimizationLevel3, true, true},
		{"no-optimizer-workers", "1", "0", "0", OptimizationLevel3, true, false},
		{"imports-use-module-split", "1", "1", "10", OptimizationLevel3, true, false},
		{"missing-worker-entry", "1", "1", "0", OptimizationLevel3, false, false},
		{"disabled", "0", "1", "0", OptimizationLevel0, true, false},
		{"ordinary-O0", "", "0", "0", OptimizationLevel0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ELISACORE_PARALLEL_IR", tc.ir)
			t.Setenv("ELISACORE_PARALLEL_OPT", tc.opt)
			t.Setenv("ELISACORE_PARALLEL_OPT_IMPORT", tc.imports)
			partitionWorkersAvailable = tc.workers
			if got := session.partitionedIRApplies(result, LLVMObjectEmitOptions{OptLevel: tc.level}); got != tc.want {
				t.Fatalf("partitioned generation admitted=%v, want %v", got, tc.want)
			}
		})
	}
}
