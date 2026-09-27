package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Reclaiming a span inside an arena must not consume the region cache's parking
// budget. The budget used to be charged by every reclaim and refunded only by a
// cache hit, so after 128 reclaims the cache could never park again and every
// freed region went back to munmap (with a fresh mmap and page faults on the next
// scratch region). Each loop iteration below reclaims one non-tail darray backing.
func TestRunCLIRegionCacheSurvivesArenaReclaims(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixturePath := filepath.Join(t.TempDir(), "region_cache_reclaim_runtime_fixture.elisa")
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std runtime path: %v", err)
	}
	src := `@test
def region_cache_survives_reclaims_test() -> void:
    can Abort.Panic, Memory.Allocate, Memory.Release:
        arena: mutable Arena = zeroed
        in arena:
            for i in 0..<400:
                a: mutable darray[i64] @arena = []
                b: mutable darray[i64] @arena = []
                a.push(i.i64())
                b.push(i.i64())
                for j in 0..<16:
                    a.push(j.i64())
                if a.count != 17 or b.count != 1:
                    panic("darray growth corrupted")
        arena_free(&arena)
        # new_region may itself reuse a parked region, so sample the count after it.
        r: mutable heap Region& = new_region(64)
        before: mutable usize = 0
        trusted [Unsafe.MutableGlobal, Global.Read]:
            before <- __elisa_region_cache.reclaimed_spans
        free_region(r)
        after: mutable usize = 0
        trusted [Unsafe.MutableGlobal, Global.Read]:
            after <- __elisa_region_cache.reclaimed_spans
        if before >= 128:
            panic("region cache budget exhausted by reclaimed spans")
        if after != before + 1:
            panic("freed scratch region was not parked in the region cache")
`
	full := "include \"" + std + "\"\n" + src
	if err := os.WriteFile(fixturePath, []byte(full), 0o644); err != nil {
		t.Fatalf("failed to write region cache fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected region cache runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_cache_survives_reclaims_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
