package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Heavy end-to-end stress tests. Each fixture is compiled to a NATIVE executable
// (`-emit test`, real pthreads) and run; a memory bug (UAF / buffer overflow /
// leak-corruption / lost atomic increment) shows up as a wrong computed result
// (panic -> non-zero exit) or a watchdog trap. The allocation suite additionally
// runs under `-fbounds-check` so every dynamic index/deref is bounds- and
// null-guarded at runtime.
//
// These are intentionally large (50k-element darrays, 8k-key dicts, 800k concurrent
// atomic increments, hundreds of thread spawn/join cycles). Skipped under -short.

func stressStdInclude(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std include: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("std runtime not found at %s: %v", abs, err)
	}
	return abs
}

// runStressProgram writes `include "<std>"` + body to a temp fixture, compiles and
// runs it via `-emit test`, and returns the exit code plus captured streams.
func runStressProgram(t *testing.T, name, body string, extraArgs ...string) (int, string, string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std := stressStdInclude(t)
	src := "include \"" + std + "\"\n\n" + body
	path := filepath.Join(t.TempDir(), name+".elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	args := append([]string{"-emit", "test"}, extraArgs...)
	args = append(args, path)
	var stdout, stderr bytes.Buffer
	exit := runCLI(args, &stdout, &stderr)
	return exit, stdout.String(), stderr.String()
}

// assertAllPassed asserts a clean run: exit 0, no failures, and every named test OK.
func assertAllPassed(t *testing.T, exit int, stdout, stderr string, names ...string) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("stress program exited %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "failed=0") {
		t.Fatalf("expected failed=0, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	for _, n := range names {
		if !strings.Contains(stdout, "[       OK ] "+n) {
			t.Fatalf("expected %q to pass, got:\n%s", n, stdout)
		}
	}
}

const allocStressBody = `
@test
def darray_growth_realloc() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region r(2097152):
            xs: mutable darray[i64] @r = []
            for i in 0..<50000:
                xs.push(i.i64() * 2)
            if xs.count != 50000:
                panic("darray count wrong after growth")
            sum: mutable i64 = 0
            for v in xs:
                sum <- sum + v
            if sum != 2499950000:
                panic("darray sum corrupted by realloc")
            if xs[0] != 0 or xs[49999] != 99998:
                panic("darray boundary element corrupted")

@test
def dict_scale() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region r(4194304):
            m: mutable dict[i64, i64] = arena_dict_new[i64, i64](r.ref[mutable Arena&], 16)
            for i in 0..<8000:
                _ = arena_dict_put_checked[i64, i64](r.ref[mutable Arena&], m.ref[mutable dict[i64, i64]&], i.i64(), i.i64() * 3)
            for i in 0..<8000:
                v: mutable i64&? = arena_dict_get[i64, i64](m.ref[dict[i64, i64]&], i.i64())
                if v == null:
                    panic("dict lost a key")
                elif v[0] != i.i64() * 3:
                    panic("dict value corrupted")

@test
def nested_region_churn() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        total: mutable i64 = 0
        for round in 0..<300:
            region outer(65536):
                xs: mutable darray[i64] @outer = []
                xs.push(round.i64())
                region inner(8192):
                    ys: mutable darray[i64] @inner = []
                    for j in 0..<100:
                        ys.push(j.i64())
                    s: mutable i64 = 0
                    for v in ys:
                        s <- s + v
                    total <- total + s
                total <- total + xs[0]
        if total != 300 * 4950 + 44850:
            panic("nested region churn corrupted")
`

// Allocation/region stress: darray realloc, dict at scale, nested-region churn.
// Run under -fbounds-check so every dynamic index/deref is runtime-guarded.
func TestMemoryStressAllocation(t *testing.T) {
	if testing.Short() {
		t.Skip("heavy stress test; skipped under -short")
	}
	exit, stdout, stderr := runStressProgram(t, "alloc_stress", allocStressBody, "-fbounds-check")
	assertAllPassed(t, exit, stdout, stderr,
		"darray_growth_realloc", "dict_scale", "nested_region_churn")
}

const concurrencyStressBody = `
global STRESS_SHARED: atomic[i64] = zeroed

def stress_bump(n: i64) -> i64:
    can Atomics.Rmw:
        for i in 0..<n:
            _ = fetch_add(STRESS_SHARED.ref[mutable atomic[i64]&], 1, MemoryOrder.SeqCst)
        return 0

def stress_sq(x: i64) -> i64:
    return x * x

@test
def atomic_contention() -> void:
    can Thread.Spawn, Thread.Join, Memory.Allocate, Memory.Release, Atomics.Rmw, Atomics.Load, Atomics.Store, Atomics.CompareExchange, Abort.Panic:
        store(STRESS_SHARED.ref[mutable atomic[i64]&], 0, MemoryOrder.SeqCst)
        t0: Thread[i64, Joinable] = spawn1(stress_bump, 100000)
        t1: Thread[i64, Joinable] = spawn1(stress_bump, 100000)
        t2: Thread[i64, Joinable] = spawn1(stress_bump, 100000)
        t3: Thread[i64, Joinable] = spawn1(stress_bump, 100000)
        t4: Thread[i64, Joinable] = spawn1(stress_bump, 100000)
        t5: Thread[i64, Joinable] = spawn1(stress_bump, 100000)
        t6: Thread[i64, Joinable] = spawn1(stress_bump, 100000)
        t7: Thread[i64, Joinable] = spawn1(stress_bump, 100000)
        _ = join(move t0)
        _ = join(move t1)
        _ = join(move t2)
        _ = join(move t3)
        _ = join(move t4)
        _ = join(move t5)
        _ = join(move t6)
        _ = join(move t7)
        total: i64 = load(STRESS_SHARED.ref[atomic[i64]&], MemoryOrder.SeqCst)
        if total != 800000:
            panic("atomic contention lost increments (race / non-atomic RMW)")

@test
def spawn_churn() -> void:
    can Thread.Spawn, Thread.Join, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange:
        acc: mutable i64 = 0
        for i in 0..<500:
            t: Thread[i64, Joinable] = spawn1(stress_sq, i.i64())
            acc <- acc + join(move t)
        if acc != 41541750:
            panic("spawn churn sum wrong")

@test
def pool_scale() -> void:
    can Pool.Create, Pool.Submit, Pool.Await, Pool.Shutdown, Thread.Spawn, Thread.Join, Memory.Allocate, Memory.Release, Atomics.Load, Atomics.CompareExchange, Abort.Panic:
        pool: ThreadPool = pool_new(8)
        acc: mutable i64 = 0
        for i in 0..<500:
            task: Task[i64, Pending] = pool_submit1(pool.ref[mutable ThreadPool&], stress_sq, i.i64())
            acc <- acc + pool_await(move task)
        if acc != 41541750:
            panic("pool scale sum wrong")
        pool_shutdown(pool.ref[mutable ThreadPool&])

@test
def mutex_lock_churn() -> void:
    can Memory.Allocate, Memory.Release, Sync.Lock, Sync.Unlock, Abort.Panic:
        mu: Mutex = mutex()
        for i in 0..<10000:
            guard: MutexGuard[Held] = mutex_lock(mu.ref[mutable Mutex&])
            mutex_unlock(move guard)
        mutex_dispose(mu.ref[mutable Mutex&])
`

// Concurrency stress: 800k concurrent atomic increments across 8 threads (atomicity),
// hundreds of thread spawn/join cycles, a thread pool at scale, and mutex lock churn.
func TestConcurrencyStress(t *testing.T) {
	if testing.Short() {
		t.Skip("heavy stress test; skipped under -short")
	}
	exit, stdout, stderr := runStressProgram(t, "concurrency_stress", concurrencyStressBody)
	assertAllPassed(t, exit, stdout, stderr,
		"atomic_contention", "spawn_churn", "pool_scale", "mutex_lock_churn")
}

// Best-effort: run the malloc-heavy concurrency fixture under AddressSanitizer. Link-time
// ASan instruments the libc malloc/free used by per-thread/task state (catching double-free,
// heap use-after-free, and heap-buffer-overflow there). Region memory is mmap-based, so ASan
// does not cover it; leak detection is disabled because the bump/arena allocators and runtime
// globals intentionally retain memory and would false-positive (and LSan is unavailable on
// macOS anyway). The test fails only on an explicit ASan error and skips if ASan is not
// available in the toolchain.
func TestConcurrencyStressUnderASan(t *testing.T) {
	if testing.Short() {
		t.Skip("heavy stress test; skipped under -short")
	}
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "concurrency_asan", concurrencyStressBody, "-link", "-fsanitize=address")
	combined := stdout + "\n" + stderr
	if strings.Contains(combined, "ERROR: AddressSanitizer") || strings.Contains(combined, "runtime error:") {
		t.Fatalf("AddressSanitizer reported a memory error:\n%s", combined)
	}
	if !strings.Contains(stdout, "[ SUMMARY") {
		t.Skipf("ASan build/link unavailable; skipping. stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if exit != 0 || !strings.Contains(stdout, "failed=0") {
		t.Fatalf("concurrency stress under ASan did not pass cleanly (exit %d):\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
}

// Meta-test: the stress harness must actually DETECT a failing program (a panic on a
// path that does execute), otherwise a green run proves nothing.
func TestStressHarnessDetectsFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("native build; skipped under -short")
	}
	body := `
@test
def deliberately_wrong() -> void:
    can Abort.Panic:
        if 1 + 1 == 2:
            panic("intentional failure")
`
	exit, stdout, _ := runStressProgram(t, "neg_control", body)
	if exit == 0 {
		t.Fatalf("expected non-zero exit for a failing test, got 0\n%s", stdout)
	}
	if !strings.Contains(stdout, "failed=1") {
		t.Fatalf("expected failed=1 in summary, got:\n%s", stdout)
	}
}

// Meta-test: the runtime index/deref watchdog must trap an out-of-bounds access
// (buffer-overflow detection), not read past the allocation.
func TestStressBoundsWatchdogTrapsOOB(t *testing.T) {
	if testing.Short() {
		t.Skip("native build; skipped under -short")
	}
	body := `
@test
def out_of_bounds() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region r(4096):
            xs: mutable darray[i64] @r = []
            xs.push(1)
            xs.push(2)
            trusted Unsafe.UncheckedIndex:
                idx: usize = 100000
                sink: mutable i64 = xs[idx]
                if sink == 12345:
                    panic("unreachable")
`
	exit, stdout, _ := runStressProgram(t, "oob", body, "-fbounds-check")
	if exit == 0 {
		t.Fatalf("expected out-of-bounds access to trap (non-zero exit), got 0\n%s", stdout)
	}
}
