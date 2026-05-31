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
            owner: mutable Arena& = scratch.ref[mutable Arena&]
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = region_pool_acquire(pool.ref[RegionPool[PoolNode]&])
            h.ptr.left <- 11
            h.ptr.right <- 12
            assert_eq(h.ptr.left + h.ptr.right, 23)
            addr1: uintptr = h.ptr.cast[u8&].uintptr()
            region_pool_release(pool.ref[RegionPool[PoolNode]&], move h)
            h2: Pooled[PoolNode] = region_pool_acquire(pool.ref[RegionPool[PoolNode]&])
            assert_eq(h2.ptr.cast[u8&].uintptr(), addr1)
            assert_eq(h2.ptr.left, 0)
            region_pool_release(pool.ref[RegionPool[PoolNode]&], move h2)
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
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_uaf_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

def use_after_release() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = scratch.ref[mutable Arena&]
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = region_pool_acquire(pool.ref[RegionPool[PoolNode]&])
            region_pool_release(pool.ref[RegionPool[PoolNode]&], move h)
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
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_double_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

def double_release() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = scratch.ref[mutable Arena&]
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = region_pool_acquire(pool.ref[RegionPool[PoolNode]&])
            region_pool_release(pool.ref[RegionPool[PoolNode]&], move h)
            region_pool_release(pool.ref[RegionPool[PoolNode]&], move h)
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
            owner: mutable Arena& = scratch.ref[mutable Arena&]
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = region_pool_acquire(pool.ref[RegionPool[PoolNode]&])
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
	expectRegionPoolInteriorBorrowRejected(t, "region_pool_stash_fixture.elisa", `            b: mutable heap PoolNode& = h.ptr
            region_pool_release(pool.ref[RegionPool[PoolNode]&], move h)
            b.left <- 99`)
}

// TestRunCLIRegionPoolRejectsCopyHopInteriorBorrowAfterRelease covers a copy-hop: the alias
// is copied into a second local (`c = b`) before the release, and used through `c` after.
func TestRunCLIRegionPoolRejectsCopyHopInteriorBorrowAfterRelease(t *testing.T) {
	expectRegionPoolInteriorBorrowRejected(t, "region_pool_copyhop_fixture.elisa", `            b: mutable heap PoolNode& = h.ptr
            c: mutable heap PoolNode& = b
            region_pool_release(pool.ref[RegionPool[PoolNode]&], move h)
            c.left <- 99`)
}

// TestRunCLIRegionPoolRejectsInteriorBorrowAfterConditionalRelease covers control-flow: the
// handle is released on only one branch, so the alias must stay tainted at the merge point
// (conservative union, not the intersection used for genuine owner borrows).
func TestRunCLIRegionPoolRejectsInteriorBorrowAfterConditionalRelease(t *testing.T) {
	expectRegionPoolInteriorBorrowRejected(t, "region_pool_cf_fixture.elisa", `            b: mutable heap PoolNode& = h.ptr
            if flag:
                region_pool_release(pool.ref[RegionPool[PoolNode]&], move h)
            b.left <- 99`)
}

// TestRunCLIRegionPoolAllowsInteriorBorrowBeforeReleaseAndAddressSnapshot confirms the check
// does not over-reject: using the interior borrow BEFORE release is fine, and a `uintptr`
// address snapshot (a copied integer, not a live reference) may be read after release.
func TestRunCLIRegionPoolAllowsInteriorBorrowBeforeReleaseAndAddressSnapshot(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_pool_valid_fixture.elisa")
	src := regionPoolFixturePreamble(t, fixtureDir) + `
struct PoolNode:
    left: mutable i64

def interior_borrow_valid() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(64):
            owner: mutable Arena& = scratch.ref[mutable Arena&]
            pool: mutable RegionPool[PoolNode] = region_pool_new[PoolNode](owner)
            h: Pooled[PoolNode] = region_pool_acquire(pool.ref[RegionPool[PoolNode]&])
            b: mutable heap PoolNode& = h.ptr
            b.left <- 5
            addr: uintptr = h.ptr.cast[u8&].uintptr()
            region_pool_release(pool.ref[RegionPool[PoolNode]&], move h)
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
