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
		importing := os.Getenv("ELISA_TEST_PREOPT_IMPORT") == "1"
		if importing {
			t.Setenv("ELISACORE_PARALLEL_OPT_IMPORT", "100")
		}
		source := incrementalObjectFixture(32)
		revision := os.Getenv("ELISA_TEST_PREOPT_REVISION")
		want := 54
		if revision == "body" {
			source = strings.Replace(source, "return value + 1\n", "return value + 2\n", 1)
			want = 55
		}
		if revision == "locals" || revision == "locals-debug" {
			source = strings.ReplaceAll(source, "value", "renamed")
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
		options.DebugInfo = strings.Contains(revision, "debug")
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
		// An imported body also lives in its importers, so a body edit may
		// re-optimize them too; a repeated revision still reuses everything.
		oneEdit := stats.PartitionsEmitted == 1 || importing && stats.PartitionsEmitted >= 1
		if expected == "4" && stats.PartitionsEmitted != 4 || expected == "1" && !oneEdit || expected == "0" && stats.PartitionsEmitted != 0 {
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
	for _, revision := range []struct{ name, emitted string }{{"base", "4"}, {"repeat", "0"}, {"body", "1"}, {"body", "0"}, {"lines", "0"}, {"locals", "0"}, {"base", "0"}, {"O2", "4"}, {"O2", "0"}, {"debug", "4"}, {"debug", "0"}, {"locals-debug", "4"}, {"locals-debug", "0"}, {"debug", "0"}, {"base", "0"}} {
		t.Run(revision.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestPreoptimizedPartitionsPersistAcrossProcesses$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "ELISACORE_TEST_NATIVE_WORKERS=1", "ELISA_TEST_PREOPT_CHILD=1", "ELISA_TEST_PREOPT_ROOT="+root, "ELISA_TEST_PREOPT_REVISION="+revision.name, "ELISA_TEST_PREOPT_EMIT="+revision.emitted)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}

// Small foreign callees imported as available_externally copies keep native
// results identical to a serial build and stay cache-exact across processes.
func TestPreoptimizedPartitionsWithImportsAcrossProcesses(t *testing.T) {
	t.Setenv("ELISA_TEST_PREOPT_IMPORT", "1")
	TestPreoptimizedPartitionsPersistAcrossProcesses(t)
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
		{"imports-keep-partitioned-ir", "1", "1", "10", OptimizationLevel3, true, true},
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

// A unit larger than the session budget still needs its checked bitcode as
// worker input even though no cache key/object can be retained.
func TestPreoptimizedPartitionUncachedInput(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_OPT", "1")
	t.Setenv("ELISACORE_TEST_NATIVE_WORKERS", "1")
	previous := partitionWorkersAvailable
	RunPartitionWorkerIfRequested()
	defer func() { partitionWorkersAvailable = previous }()
	for _, ir := range []string{"0", "1"} {
		t.Run("IR"+ir, func(t *testing.T) {
			t.Setenv("ELISACORE_PARALLEL_IR", ir)
			result := parseAndAnalyzeBackendTest(t, "uncached.elisa", incrementalObjectFixture(32))
			session := NewIncrementalObjectSession(4, 1)
			session.minWeight = 0
			for attempt := 0; attempt < 2; attempt++ {
				out := filepath.Join(t.TempDir(), "uncached.o")
				if err := session.WriteObject(result, out, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}); err != nil {
					t.Fatal(err)
				}
				if got := incrementalObjectNativeStatus(t, out); got != 54 {
					t.Fatalf("uncached native=%d want=54", got)
				}
			}
			stats := session.Stats()
			if stats.PartitionsEmitted != 8 || stats.RetainedBytes != 0 || stats.RetainedPartitions != 0 || stats.SerialFallbacks != 0 {
				t.Fatalf("unexpected uncached path: %+v", stats)
			}
		})
	}
}
