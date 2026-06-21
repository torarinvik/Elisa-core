package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dataRaceMarker = "cannot be shared across threads"

func compileConcurrencyFixture(t *testing.T, name, body string) string {
	t.Helper()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	dir := t.TempDir()
	rel := func(n string) string {
		p, err := filepath.Rel(dir, filepath.Join(std, n))
		if err != nil {
			t.Fatalf("rel include %s: %v", n, err)
		}
		return filepath.ToSlash(p)
	}
	src := fmt.Sprintf("# include %q\n# include %q\n", rel("test.elisa"), rel("elisacore_runtime.elisa")) + body
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr)
	return stderr.String()
}

// Fearless concurrency: a spawned closure that value-captures a mutable reference shares its
// referent across threads (closures capture by value, so the captured pointer still aliases
// the original) — a data race. It is rejected, both inline and when bound to a local.
func TestRunCLISpawnRejectsClosureCapturingMutableRef(t *testing.T) {
	t.Parallel()
	inline := compileConcurrencyFixture(t, "race_inline.elisa", `@test
def race() -> void:
    can Thread.Spawn, Thread.Join, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange:
        x: mutable i64 = 0
        r: mutable i64& = &x
        t: Thread[i64, Joinable] = spawn1(lambda(a: i64) => r[0] + a, 5)
        r[0] <- 99
        _ = join(move t)
`)
	if !strings.Contains(inline, dataRaceMarker) {
		t.Fatalf("expected an inline captured-ref spawn to be rejected, got:\n%s", inline)
	}

	bound := compileConcurrencyFixture(t, "race_bound.elisa", `@test
def race() -> void:
    can Thread.Spawn, Thread.Join, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange:
        x: mutable i64 = 0
        r: mutable i64& = &x
        worker: func(i64) -> i64 = lambda(a: i64) => r[0] + a
        t: Thread[i64, Joinable] = spawn1(worker, 5)
        r[0] <- 99
        _ = join(move t)
`)
	if !strings.Contains(bound, dataRaceMarker) {
		t.Fatalf("expected a variable-bound captured-ref spawn to be rejected, got:\n%s", bound)
	}
}

// A spawned closure that captures only plain VALUES is safe — value-capture snapshots the
// data, so there is no sharing. It must not be flagged.
func TestRunCLISpawnAllowsValueCapturingClosure(t *testing.T) {
	t.Parallel()
	out := compileConcurrencyFixture(t, "value_capture.elisa", `@test
def ok() -> void:
    can Thread.Spawn, Thread.Join, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange:
        seed: i64 = 7
        t: Thread[i64, Joinable] = spawn1(lambda(a: i64) => seed + a, 5)
        _ = join(move t)
`)
	if strings.Contains(out, dataRaceMarker) {
		t.Fatalf("expected a value-capturing closure spawn to be allowed, got:\n%s", out)
	}
}
