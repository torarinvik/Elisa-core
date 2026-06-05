package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A scoped region (`in auto:` / `region`) is freed at SCOPE exit, so a region inside a loop
// reclaims its arena every iteration instead of leaking it until function return. Before the
// fix this leaked ~10-16 KB/iteration (1M iterations ≈ 10 GB); after, peak RSS is a few MB
// regardless of iteration count. This execs the built binary and asserts peak RSS stays bounded.
func TestRegionInLoopFreesPerIterationBoundsMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("heavy memory test; skipped under -short")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	const iterations = 1000000
	src := "include \"" + std + "\"\n" + `
@test
def region_loop() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        acc: mutable i64 = 0
        for i in 0..<1000000:
            in auto:
                xs: mutable darray[i64] = []
                xs.push(7)
                xs.push(11)
                acc <- acc + xs[0] + xs[1]
        if acc != 18000000:
            panic("region loop produced wrong result")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "region_loop.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Build (and keep) the native test binary; capture its path from stderr.
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	exePath := ""
	for _, line := range strings.Split(stderr.String(), "\n") {
		if idx := strings.Index(line, "test binary: "); idx >= 0 {
			exePath = strings.TrimSpace(line[idx+len("test binary: "):])
			break
		}
	}
	if exePath == "" {
		t.Skipf("could not locate kept test binary in stderr:\n%s", stderr.String())
	}
	defer os.Remove(exePath)
	defer os.RemoveAll(exePath + ".dSYM")

	// Run the binary in isolation and read its peak RSS.
	cmd := exec.Command(exePath, "region_loop")
	if err := cmd.Run(); err != nil {
		t.Fatalf("region_loop run failed: %v (iterations=%d)", err, iterations)
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		t.Skip("rusage unavailable on this platform")
	}
	// ru_maxrss is bytes on darwin, kilobytes on linux.
	maxRSSBytes := int64(ru.Maxrss)
	if runtime.GOOS == "linux" {
		maxRSSBytes *= 1024
	}
	const limitBytes = 512 * 1024 * 1024 // 512 MB — fix keeps it at a few MB; leak would be ~10 GB.
	if maxRSSBytes > limitBytes {
		t.Fatalf("region-in-loop peak RSS = %d MB over %d iterations — expected bounded (<512 MB); regions are not freed per iteration (leak regression)",
			maxRSSBytes/(1024*1024), iterations)
	}
	t.Logf("region-in-loop peak RSS = %d MB over %d iterations (bounded)", maxRSSBytes/(1024*1024), iterations)
}

// Automatic loop-body region tightening: a loop-local allocation with NO manual `in auto:` is
// auto-wrapped so it reclaims per iteration. Without the auto-wrap it would accumulate in the
// function region for the whole loop (~GBs at 1M iters); with it, peak RSS stays a few MB.
func TestRegionAutoWrapLoopBoundsMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("heavy memory test; skipped under -short")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
@test
def auto_loop() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        acc: mutable i64 = 0
        for i in 0..<1000000:
            xs: mutable darray[i64] = []
            xs.push(7)
            xs.push(11)
            acc <- acc + xs[0] + xs[1]
        if acc != 18000000:
            panic("auto loop produced wrong result")
`
	path := filepath.Join(t.TempDir(), "auto_loop.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\n%s\n%s", code, stdout.String(), stderr.String())
	}
	exePath := ""
	for _, line := range strings.Split(stderr.String(), "\n") {
		if idx := strings.Index(line, "test binary: "); idx >= 0 {
			exePath = strings.TrimSpace(line[idx+len("test binary: "):])
			break
		}
	}
	if exePath == "" {
		t.Skipf("could not locate kept test binary:\n%s", stderr.String())
	}
	defer os.Remove(exePath)
	defer os.RemoveAll(exePath + ".dSYM")
	cmd := exec.Command(exePath, "auto_loop")
	if err := cmd.Run(); err != nil {
		t.Fatalf("auto_loop run failed: %v", err)
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		t.Skip("rusage unavailable")
	}
	maxRSSBytes := int64(ru.Maxrss)
	if runtime.GOOS == "linux" {
		maxRSSBytes *= 1024
	}
	if maxRSSBytes > 512*1024*1024 {
		t.Fatalf("auto-wrapped loop peak RSS = %d MB over 1M iterations — expected bounded (<512 MB); loop-body auto-tightening did not apply",
			maxRSSBytes/(1024*1024))
	}
	t.Logf("auto-wrapped loop peak RSS = %d MB over 1M iterations (bounded, no manual `in auto:`)", maxRSSBytes/(1024*1024))
}

// A region inside a hot loop reuses its arena across iterations (reset, not free+realloc), so it
// does NOT pay an mmap/munmap per iteration. Measured: 20M iterations runs in ~0.17 s with reuse
// vs ~28 s (mostly syscalls) with per-iteration free. The 10 s bound has a huge margin yet still
// catches a reuse regression.
func TestRegionLoopReclaimReusesArenaIsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test; skipped under -short")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
@test
def hot_loop() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        acc: mutable i64 = 0
        for i in 0..<20000000:
            xs: mutable darray[i64] = []
            xs.push(i.i64())
            acc <- acc + xs[0]
        if acc < 0:
            panic("hot loop wrong")
`
	path := filepath.Join(t.TempDir(), "hot_loop.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\n%s\n%s", code, stdout.String(), stderr.String())
	}
	exePath := ""
	for _, line := range strings.Split(stderr.String(), "\n") {
		if idx := strings.Index(line, "test binary: "); idx >= 0 {
			exePath = strings.TrimSpace(line[idx+len("test binary: "):])
			break
		}
	}
	if exePath == "" {
		t.Skipf("could not locate kept test binary:\n%s", stderr.String())
	}
	defer os.Remove(exePath)
	defer os.RemoveAll(exePath + ".dSYM")
	start := time.Now()
	if err := exec.Command(exePath, "hot_loop").Run(); err != nil {
		t.Fatalf("hot_loop run failed: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 10*time.Second {
		t.Fatalf("hot region loop took %s for 20M iterations — expected fast arena reuse (~0.2s); per-iteration free+realloc regression", elapsed)
	}
	t.Logf("hot region loop: 20M iterations in %s (arena reused per iteration)", elapsed)
}

// A `break`/`continue` must only run cleanups registered INSIDE the loop body, never a cleanup
// from a scope that ENCLOSES the loop. The function body here is wrapped in an inferred region
// that owns `kinds` (a darray that accumulates across iterations); the outer `while` has a
// `continue` reached through a nested inner `while`. The continue stays inside the loop, so it
// must NOT free the enclosing region. Before the fix, emitActiveScopedCleanup fired ALL active
// cleanups on continue — freeing `kinds`'s arena mid-loop, so the next push reallocated freed
// memory and the program segfaulted. The fix records a per-loop cleanup floor so continue/break
// only reclaim cleanups at or above the floor. This builds and runs the program; a fault (the
// regression) makes the child exit non-zero.
func TestContinueDoesNotFreeEnclosingRegion(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	// scan tokenizes a string with quoted runs: the quote branch advances past the string via a
	// nested inner `while` (reading the param across the push), then `continue`s. `kinds` lives
	// in the function region and accumulates one entry per quote.
	src := `
def scan(src: static u8&, n: usize) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        kinds: mutable darray[i32] = []
        i: mutable usize = 0u
        while i < n:
            c: u8 = src[i]
            if c == 34u8:
                kinds.push(7)
                i <- i + 1u
                while i < n and src[i] != 34u8:
                    i <- i + 1u
                i <- i + 1u
                continue
            i <- i + 1u
        return kinds.count.i64()
@test
def continue_region() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        s: static u8& = "x\"hello\"y\"world\"z\"three\"q"
        if scan(s, 24u) != 3:
            panic("continue freed the enclosing region mid-loop")
`
	runSingleTestProgramExpectOK(t, "continue_region", src,
		"continue freed an enclosing region (use-after-free regression)")
}

// `break` out of an inner loop is the same shape as the continue bug: the break exits only the
// inner loop and stays inside the enclosing function region, so it must not free that region.
// Confirmed to segfault without the per-loop cleanup floor.
func TestBreakDoesNotFreeEnclosingRegion(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	// The quote branch advances past the string in a nested inner `while`, `break`ing at the
	// closing quote; `kinds` accumulates in the function region across the outer loop.
	src := `
def scan(src: static u8&, n: usize) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        kinds: mutable darray[i32] = []
        i: mutable usize = 0u
        while i < n:
            c: u8 = src[i]
            if c == 34u8:
                kinds.push(7)
                i <- i + 1u
                while i < n:
                    if src[i] == 34u8:
                        break
                    i <- i + 1u
                i <- i + 1u
                continue
            i <- i + 1u
        return kinds.count.i64()
@test
def break_region() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        s: static u8& = "x\"hello\"y\"world\"z\"three\"q"
        if scan(s, 24u) != 3:
            panic("break freed the enclosing region mid-loop")
`
	runSingleTestProgramExpectOK(t, "break_region", src,
		"break freed an enclosing region (use-after-free regression)")
}

// The per-loop cleanup floor must apply ONLY to break/continue (which stay inside enclosing
// scopes), never to `return` (which exits the whole function and must run EVERY enclosing
// cleanup). This pins the other direction: a `defer` registered before a loop must still run when
// the function returns from inside the loop. With the floor wrongly shared by the return path,
// the defer is skipped — `out` stays 0 and the test panics.
func TestReturnFromLoopRunsEnclosingDefer(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	src := `
def run(out: mutable i64&) -> i64:
    can Abort.Panic:
        defer block:
            out <- 999
        i: mutable i64 = 0
        while i < 10:
            if i == 3:
                return 5
            i <- i + 1
        return 0
@test
def return_defer() -> void:
    can Abort.Panic:
        x: mutable i64 = 0
        r: i64 = run((&x).cast[mutable i64&])
        if r != 5:
            panic("wrong return value")
        if x != 999:
            panic("enclosing defer skipped on return-from-loop")
`
	runSingleTestProgramExpectOK(t, "return_defer", src,
		"return-from-loop skipped an enclosing defer (cleanup floor wrongly applied to return)")
}

// Matrix hardening: a `return` from a DOUBLY-NESTED loop must still run the function-level defer.
// The floor at the return is the innermost loop's; return must ignore it (floor 0) at every depth.
func TestReturnFromNestedLoopRunsEnclosingDefer(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	src := `
def run(out: mutable i64&) -> i64:
    can Abort.Panic:
        defer block:
            out <- 7
        i: mutable i64 = 0
        while i < 5:
            j: mutable i64 = 0
            while j < 5:
                if i == 2 and j == 3:
                    return 1
                j <- j + 1
            i <- i + 1
        return 0
@test
def ret_nested_defer() -> void:
    can Abort.Panic:
        x: mutable i64 = 0
        if run((&x).cast[mutable i64&]) != 1:
            panic("wrong return")
        if x != 7:
            panic("nested-loop return skipped the function defer")
`
	runSingleTestProgramExpectOK(t, "ret_nested_defer", src,
		"return from a nested loop skipped the function defer")
}

// Matrix hardening: a `continue` must run the defer scoped INSIDE the loop body (it exits the
// iteration scope), but must NOT skip it — the floor includes loop-body cleanups (>= floor).
func TestContinueRunsInLoopDefer(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	src := `
def run(out: mutable i64&) -> void:
    can Abort.Panic:
        i: mutable i64 = 0
        while i < 5:
            defer block:
                out <- out + 10
            i <- i + 1
            if i % 2 == 0:
                continue
            out <- out + 1
@test
def cont_inloop_defer() -> void:
    can Abort.Panic:
        x: mutable i64 = 0
        run((&x).cast[mutable i64&])
        if x != 53:
            panic("continue dropped or duplicated the in-loop defer")
`
	runSingleTestProgramExpectOK(t, "cont_inloop_defer", src,
		"continue mishandled the in-loop-scoped defer")
}

// docs/73 soundness: under the default reserve_commit backing an interior reference into a growable
// survives growth, because the base is fixed (contiguous reservation, in-place growth) — it never
// relocates. 100k pushes force many capacity doublings; if the base moved, `anchor[0]` would read
// garbage. Under the old chained default this would have been a use-after-free (hence the compiler
// rejected it); now it is allowed and must actually be safe at runtime.
func TestDefaultReserveCommitInteriorRefSurvivesGrowth(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	src := `
def f(n: usize) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(42)
        anchor: i64& = &xs[0]
        i: mutable usize = 1u
        while i < n:
            xs.push(i.i64())
            i <- i + 1u
        return anchor[0]
@test
def iref() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if f(100000u) != 42:
            panic("interior ref invalidated under default reserve_commit (base moved)")
`
	runSingleTestProgramExpectOK(t, "iref", src,
		"interior ref into a default reserve_commit growable did not survive growth (base relocated)")
}

// runSingleTestProgramExpectOK builds `src` as a native test binary, runs the single @test named
// `testName`, and fails if the child exits non-zero (a segfault/panic from the regression).
func runSingleTestProgramExpectOK(t *testing.T, testName, src, failHint string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), testName+".elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\n%s\n%s", code, stdout.String(), stderr.String())
	}
	exePath := ""
	for _, line := range strings.Split(stderr.String(), "\n") {
		if idx := strings.Index(line, "test binary: "); idx >= 0 {
			exePath = strings.TrimSpace(line[idx+len("test binary: "):])
			break
		}
	}
	if exePath == "" {
		t.Skipf("could not locate kept test binary:\n%s", stderr.String())
	}
	defer os.Remove(exePath)
	defer os.RemoveAll(exePath + ".dSYM")
	if err := exec.Command(exePath, testName).Run(); err != nil {
		t.Fatalf("%s run failed: %v — %s", testName, err, failHint)
	}
}
