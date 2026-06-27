package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// regionPoolFixturePreamble returns the three includes a region-pool fixture needs: test.elisa
// (assert_eq), elisacore_runtime.elisa (so the module self-defines the full runtime and the
// auto-linked runtime object is skipped — avoiding duplicate symbols), and heap.elisa (RegionPool
// / Pooled). The `# include` form is the active include directive.
func regionPoolFixturePreamble(t *testing.T, fixtureDir string) string {
	t.Helper()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("failed to compute include path for %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	return fmt.Sprintf("# include %q\n# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"), rel("heap.elisa"))
}

// TestRunCLIRegionPoolAcquireReuseRelease exercises the region-anchored object pool with its
// affine handle end to end: acquire a Pooled[T], mutate through the borrow, release it (move),
// then re-acquire and confirm the freed slot is reused and zeroed.
func TestRunCLIRegionPoolAcquireReuseRelease(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_reuse_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64
    right: mutable i64

@test
def region_pool_acquire_reuse_release() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = pool.acquire()
            h.ptr.left <- 11
            h.ptr.right <- 12
            assert_eq(h.ptr.left + h.ptr.right, 23)
            addr1: uintptr = h.ptr.cast[u8&].uintptr()
            pool.release(move h)
            h2: Pooled[PoolNode] = pool.acquire()
            assert_eq(h2.ptr.cast[u8&].uintptr(), addr1)
            assert_eq(h2.ptr.left, 0)
            pool.release(move h2)
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region pool fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected region pool test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_pool_acquire_reuse_release",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region pool output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// TestRunCLIRegionPoolRejectsUseAfterRelease confirms the affine handle makes use-after-release a
// compile error (the core safety guarantee).
func TestRunCLIRegionPoolRejectsUseAfterRelease(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_uaf_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

def use_after_release() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = pool.acquire()
            pool.release(move h)
            h.ptr.left <- 5
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write use-after-release fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected use-after-release of a pool handle to be rejected, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "cannot be used") {
		t.Fatalf("expected a use-after-consume diagnostic for the released handle, got:\n%s", combined)
	}
}

// TestRunCLIRegionPoolRejectsDoubleRelease confirms releasing the same handle twice is a compile
// error.
func TestRunCLIRegionPoolRejectsDoubleRelease(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_double_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

def double_release() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = pool.acquire()
            pool.release(move h)
            pool.release(move h)
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write double-release fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected double-release of a pool handle to be rejected, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "cannot be used") {
		t.Fatalf("expected a use-after-consume diagnostic for the double-released handle, got:\n%s", combined)
	}
}

// expectRegionPoolInteriorBorrowRejected compiles a fixture whose body stashes a raw
// interior borrow of a Pooled handle and uses it after release; it must be rejected with
// a use-after-consume diagnostic. These guard the "stashed interior borrow" hole that
// affine consumption of the handle alone does not close (the alias is separately rooted).
func expectRegionPoolInteriorBorrowRejected(t *testing.T, fixtureName, body string) {
	t.Helper()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, fixtureName)
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

def interior_borrow_uaf(flag: bool) -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = pool.acquire()
` + body + "\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write interior-borrow fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected use-after-release of a stashed interior borrow to be rejected, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "cannot be used") {
		t.Fatalf("expected a use-after-consume diagnostic for the stashed interior borrow, got:\n%s", combined)
	}
}

// TestRunCLIRegionPoolRejectsStashedInteriorBorrowAfterRelease covers the core hole: a raw
// interior pointer copied out of the handle (`b = h.ptr`) and written through after release.
func TestRunCLIRegionPoolRejectsStashedInteriorBorrowAfterRelease(t *testing.T) {
	t.Parallel()
	expectRegionPoolInteriorBorrowRejected(t, "region_pool_stash_fixture.elisa", `            b: mutable heap PoolNode& = h.ptr
            pool.release(move h)
            b.left <- 99`)
}

// TestRunCLIRegionPoolRejectsCopyHopInteriorBorrowAfterRelease covers a copy-hop: the alias
// is copied into a second local (`c = b`) before the release, and used through `c` after.
func TestRunCLIRegionPoolRejectsCopyHopInteriorBorrowAfterRelease(t *testing.T) {
	t.Parallel()
	expectRegionPoolInteriorBorrowRejected(t, "region_pool_copyhop_fixture.elisa", `            b: mutable heap PoolNode& = h.ptr
            c: mutable heap PoolNode& = b
            pool.release(move h)
            c.left <- 99`)
}

// TestRunCLIRegionPoolRejectsInteriorBorrowAfterConditionalRelease covers control-flow: the
// handle is released on only one branch, so the alias must stay tainted at the merge point
// (conservative union, not the intersection used for genuine owner borrows).
func TestRunCLIRegionPoolRejectsInteriorBorrowAfterConditionalRelease(t *testing.T) {
	t.Parallel()
	expectRegionPoolInteriorBorrowRejected(t, "region_pool_cf_fixture.elisa", `            b: mutable heap PoolNode& = h.ptr
            if flag:
                pool.release(move h)
            b.left <- 99`)
}

// TestRunCLIRegionPoolAllowsInteriorBorrowBeforeReleaseAndAddressSnapshot confirms the check
// does not over-reject: using the interior borrow BEFORE release is fine, and a `uintptr`
// address snapshot (a copied integer, not a live reference) may be read after release.
func TestRunCLIRegionPoolAllowsInteriorBorrowBeforeReleaseAndAddressSnapshot(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_valid_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

def interior_borrow_valid() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = pool.acquire()
            b: mutable heap PoolNode& = h.ptr
            b.left <- 5
            addr: uintptr = h.ptr.cast[u8&].uintptr()
            pool.release(move h)
            snapshot: uintptr = addr
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write valid interior-borrow fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected valid interior-borrow usage to compile, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "cannot be used") {
		t.Fatalf("interior-borrow check over-rejected valid usage, got:\n%s", combined)
	}
}

// TestRunCLIRegionPoolRejectsStructStoredInteriorBorrowAfterRelease covers escape into a
// data structure: the interior borrow is stashed in a struct field (`Holder(h.ptr)`) and
// used through that field after release. This exercises the nested-Fields tracking that
// composes the alias fact through struct-literal construction and field projection.
func TestRunCLIRegionPoolRejectsStructStoredInteriorBorrowAfterRelease(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_struct_store_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

struct Holder:
    p: mutable heap PoolNode&

def struct_store_uaf() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = pool.acquire()
            holder: mutable Holder = Holder(h.ptr)
            pool.release(move h)
            holder.p.left <- 5
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write struct-store fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected struct-stored interior borrow used after release to be rejected, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if combined := stdout.String() + stderr.String(); !strings.Contains(combined, "cannot be used") {
		t.Fatalf("expected a use-after-consume diagnostic for the struct-stored borrow, got:\n%s", combined)
	}
}

// TestRunCLIRegionPoolAllowsStructStoredInteriorBorrowBeforeRelease confirms the struct-store
// tracking does not over-reject: using the field before release compiles cleanly.
func TestRunCLIRegionPoolAllowsStructStoredInteriorBorrowBeforeRelease(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_struct_valid_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

struct Holder:
    p: mutable heap PoolNode&

def struct_store_valid() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = pool.acquire()
            holder: mutable Holder = Holder{p: h.ptr}
            holder.p.left <- 5
            pool.release(move h)
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write struct-store valid fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected valid struct-stored borrow usage to compile, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if combined := stdout.String() + stderr.String(); strings.Contains(combined, "cannot be used") {
		t.Fatalf("struct-store check over-rejected valid usage, got:\n%s", combined)
	}
}

// TestRunCLIRegionPoolRejectsCrossFunctionPassthroughBorrowAfterRelease covers escape across a
// function boundary: the interior borrow is laundered through a function that returns one of
// its reference parameters (`passthrough(h.ptr)`) and used after release. The function's
// return-borrow summary ("returns param 0") is instantiated against the actual argument at the
// call site, recovering the alias. Includes a nested-call variant to confirm summaries compose.
func TestRunCLIRegionPoolRejectsCrossFunctionPassthroughBorrowAfterRelease(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		bind string
	}{
		{"single", "passthrough(h.ptr)"},
		{"nested", "passthrough(passthrough(h.ptr))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixtureDir := t.TempDir()
			fixturePath := filepath.Join(fixtureDir, "region_pool_xfn_fixture.elisa")
			src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

def passthrough(p: mutable heap PoolNode&) -> mutable heap PoolNode&:
    return p

def xfn_uaf() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = pool.acquire()
            b: mutable heap PoolNode& = ` + tc.bind + `
            pool.release(move h)
            b.left <- 5
`
			if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
				t.Fatalf("failed to write cross-fn fixture: %v", err)
			}
			var stdout, stderr bytes.Buffer
			exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
			if exitCode == 0 {
				t.Fatalf("expected cross-function passthrough borrow used after release to be rejected, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			}
			if combined := stdout.String() + stderr.String(); !strings.Contains(combined, "cannot be used") {
				t.Fatalf("expected a use-after-consume diagnostic for the laundered borrow, got:\n%s", combined)
			}
		})
	}
}

// TestRunCLIRegionPoolAllowsCrossFunctionPassthroughBorrowBeforeRelease confirms the
// cross-function tracking does not over-reject: using the laundered borrow before release
// compiles cleanly (the summary instantiates to a live alias, not a consumed one).
func TestRunCLIRegionPoolAllowsCrossFunctionPassthroughBorrowBeforeRelease(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_xfn_valid_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

def passthrough(p: mutable heap PoolNode&) -> mutable heap PoolNode&:
    return p

def xfn_valid() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = pool.acquire()
            b: mutable heap PoolNode& = passthrough(h.ptr)
            b.left <- 5
            pool.release(move h)
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write cross-fn valid fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected valid cross-function borrow usage to compile, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if combined := stdout.String() + stderr.String(); strings.Contains(combined, "cannot be used") {
		t.Fatalf("cross-function check over-rejected valid usage, got:\n%s", combined)
	}
}

// TestRunCLIRegionPoolIdiomaticUsageRunsAndReuses exercises the ergonomic surface end to end:
// UFCS `pool.acquire()` / `pool.release(move h)` (auto-borrowing the mutable pool receiver),
// an inferred handle binding, an inlined owner, and `region_pool_new` with T inferred from the
// binding (no repeated `[T]`). It confirms the freed slot is reused and zeroed, and that an
// acquire left un-released (dropped) is safe — the region reclaims the slab at teardown.
func TestRunCLIRegionPoolIdiomaticUsageRunsAndReuses(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_idiomatic_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64
    right: mutable i64

@test
def idiomatic_pool() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = &scratch
            pool: mutable RegionPool[PoolNode] = region_pool_new(owner)
            h = pool.acquire()
            h.ptr.left <- 11
            h.ptr.right <- 12
            assert_eq(h.ptr.left + h.ptr.right, 23)
            addr1: uintptr = h.ptr.cast[u8&].uintptr()
            pool.release(move h)
            h2 = pool.acquire()
            assert_eq(h2.ptr.cast[u8&].uintptr(), addr1)
            assert_eq(h2.ptr.left, 0)
            # dropped without release: safe — the region reclaims the slab at teardown
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write idiomatic pool fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected idiomatic pool test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] idiomatic_pool",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected idiomatic pool output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
