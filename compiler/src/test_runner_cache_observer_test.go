package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestTestRunnerCachesBypassRequestObservers(t *testing.T) {
	t.Setenv("ELISACORE_TEST_CACHE", "1")
	for _, name := range []string{
		"ELISA_SMT_RLIMIT_LOG", "ELISA_SMT_TRACE", "ELISA_SEMANTIC_INTERNAL_PARITY_OUT",
		"ELISA_DUMP_DEATHTIME", "ELISA_DUMP_REGION_STACKS", "ELISA_REGION_DEBUG",
		"ELISA_ELEMSTATE_TRACE", "ELISA_STORE_PAYLOAD_TRACE", "ELISA_STORESCAN_TRACE",
		"ELISA_STORE_TRACE", "ELISA_RECLAIM_TRACE", "ELISA_PARALLEL_GRAPH_REPORT",
		"ELISA_PARALLEL_SHARING_REPORT", "ELISA_PARALLEL_DECLINE_LOG", "ELISA_POST_TIMING",
		"ELISA_DECL_TIMING", "ELISA_REGION_FIXPOINT_SHADOW", "ELISA_TRACKED_SHARE_CHECK",
		"ELISA_SMT_PREAMBLE_SHADOW", "ELISA_BACKEND_PARITY_OUT", "ELISACORE_ANALYSIS_CACHE_DEBUG",
		"ELISACORE_BUILD_TIMING", "ELISACORE_CPUPROFILE", "ELISACORE_MEMPROFILE",
		"ELISACORE_EMIT_LL", "ELISACORE_LEXER_CAPACITY_REPORT", "ELISACORE_RETAINED_DEBUG_SPANS",
		"ELISACORE_RETAINED_FAST_DEBUG", "ELISACORE_TEST_TIMING", "ELISACORE_TEST_PHASE_DEBUG",
		"ELISACORE_KEEP_TEST_TEMP", "ELISACORE_TEST_CACHE_DEBUG", "ELISACORE_LLVM_PASS_DEBUG",
		"ELISACORE_DUMP_MODULE_ON_VERIFY_FAILURE",
	} {
		t.Setenv(name, "")
	}
	if !testRunnerCacheEnabled() || !earlyTestCacheEnabled() {
		t.Fatal("test-runner caches unexpectedly disabled without observers")
	}

	for _, observer := range []struct{ name, value string }{
		{"ELISA_SMT_TRACE", "1"},
		{"ELISACORE_EMIT_LL", "1"},
		{"ELISA_BACKEND_PARITY_OUT", "parity.tsv"},
		{"ELISACORE_TEST_TIMING", "json"},
		{"ELISACORE_TEST_PHASE_DEBUG", "1"},
		{"ELISACORE_KEEP_TEST_TEMP", "1"},
		{"ELISACORE_LLVM_PASS_DEBUG", "1"},
		{"ELISACORE_DUMP_MODULE_ON_VERIFY_FAILURE", "1"},
	} {
		t.Run(observer.name, func(t *testing.T) {
			t.Setenv(observer.name, observer.value)
			if testRunnerCacheEnabled() || earlyTestCacheEnabled() {
				t.Fatal("test-runner cache replay skipped a requested observer")
			}
		})
	}

	t.Run("cache_debug_keeps_inner_cache", func(t *testing.T) {
		t.Setenv("ELISACORE_TEST_CACHE_DEBUG", "1")
		if !testRunnerCacheEnabled() {
			t.Fatal("cache-debug should still report inner-cache hit/miss")
		}
		if earlyTestCacheEnabled() {
			t.Fatal("early cache skipped the inner-cache debug report")
		}
	})
}

func TestTestRunnerCacheKeysSolverContents(t *testing.T) {
	if !nativeToolFixturesSupported() {
		t.Skip("cache selector fixtures require ELF or Mach-O images")
	}
	solver := filepath.Join(t.TempDir(), "z3")
	writeSolver := func(path string, source string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(source), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key := func() string {
		t.Helper()
		h := sha256.New()
		if err := writeTestRunnerSMTCacheInputs(h, true); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	t.Setenv("ELISA_Z3", solver)
	writeSolver(solver, string(nativeToolTestFixture("solver version one")))
	first := key()

	replacement := filepath.Join(filepath.Dir(solver), "z3.new")
	writeSolver(replacement, string(nativeToolTestFixture("solver version two")))
	if err := os.Rename(replacement, solver); err != nil {
		t.Fatal(err)
	}
	if second := key(); second == first {
		t.Fatal("replacing solver at the same path reused the test-runner cache key")
	}
}

func TestTestRunnerCacheKeysMachONMContents(t *testing.T) {
	if !nativeToolFixturesSupported() {
		t.Skip("cache selector fixtures require ELF or Mach-O images")
	}
	tools := t.TempDir()
	nm := filepath.Join(tools, "nm")
	writeNM := func(path string, source string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(source), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key := func() string {
		t.Helper()
		h := sha256.New()
		if err := writeTestRunnerLinkAuditInputs(h, "arm64-apple-darwin"); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	t.Setenv("PATH", tools)
	writeNM(nm, string(nativeToolTestFixture("nm version one")))
	first := key()

	replacement := filepath.Join(tools, "nm.new")
	writeNM(replacement, string(nativeToolTestFixture("nm version two")))
	if err := os.Rename(replacement, nm); err != nil {
		t.Fatal(err)
	}
	if second := key(); second == first {
		t.Fatal("replacing nm at the same path reused the test-runner cache key")
	}
}
