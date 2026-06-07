package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: a region-parameterized function pushes through a `darray[T] @r`
// parameter with NO ambient `in <arena>:` scope of its own — the growth arena
// is threaded by the compiler as a hidden Arena& param sourced from the
// caller's region. Verifies correct byte values (no arena corruption) across
// multiple capacity growths.
func TestRunCLIRegionParamContainerPushThreadsArenaViaHiddenParam(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_push_fixture.elisa")
	src := `def fill[region r](out: mutable darray[u8] @r) -> u64:
    for i in 0..<10:
        out.push((65 + i).u8())
    sum: mutable u64 = 0u64
    for i in 0..<out.count.i64():
        sum <- sum + out[i].u64()
    return sum

@test
def region_param_push_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            v: mutable darray[u8] @a = []
            s: u64 = fill(v)
            if s != 695u64:
                panic("expected sum 695 (65..74)")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param push test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_push_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// End-to-end: a list comprehension whose result is explicitly `@r` can allocate
// through the region-param hidden arena, with no ambient `in <arena>:` scope in
// the helper. This is the comprehension analogue of darray.push threading: the
// generated result darray carries region r, so its synthesized pushes source
// the caller's arena.
func TestRunCLIRegionParamListComprehensionThreadsArenaViaHiddenParam(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_list_comp_fixture.elisa")
	src := `def bumped[region r](items: darray[u8] @r) -> u64:
    out: darray[u8] @r = [item + 1 for item in items]
    sum: mutable u64 = 0u64
    for i in 0..<out.count.i64():
        sum <- sum + out[i].u64()
    return sum

@test
def region_param_list_comp_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            v: mutable darray[u8] @a = []
            v.push(10)
            v.push(20)
            v.push(30)
            s: u64 = bumped(v)
            if s != 63u64:
                panic("expected sum 63 ((10+1)+(20+1)+(30+1))")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param list-comprehension fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param list-comprehension test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_list_comp_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param list-comprehension output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// End-to-end: a region-parameterized function inserts into a `dict[cstr,i64] @r`
// parameter via the entry API (`d.entry(k).insert(v)`) with NO ambient
// `in <arena>:` scope of its own — the dict's growth arena is threaded by the
// compiler as a hidden Arena& param sourced from the caller's region, exactly
// mirroring the darray push ABI. Verifies the inserted values read back
// correctly (no arena corruption) across several insertions + a rehash.
func TestRunCLIRegionParamDictInsertThreadsArenaViaHiddenParam(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	stdDir := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_dict_fixture.elisa")
	// Include the full runtime + heap (which pulls in collections' dict ops) via
	// absolute include paths, so the fixture self-defines the default runtime
	// (skipping the auto-linked runtime object — no duplicate symbols).
	src := `include "` + filepath.Join(stdDir, "elisacore_runtime.elisa") + `"
include "` + filepath.Join(stdDir, "heap.elisa") + `"

def fill[region r](d: mutable dict[cstr, i64] @r) -> i64:
    d.entry("alpha").insert(10)
    d.entry("beta").insert(20)
    d.entry("gamma").insert(30)
    d.entry("delta").insert(40)
    sum: mutable i64 = 0
    sa: i64& = get d.get("alpha") else return -1
    sum <- sum + sa[0]
    sb: i64& = get d.entry("beta").get_or_insert(0) else return -2
    sum <- sum + sb[0]
    sc: i64& = get d.get("gamma") else return -3
    sum <- sum + sc[0]
    sd: i64& = get d.entry("delta").get_or_insert(0) else return -4
    sum <- sum + sd[0]
    return sum

@test
def region_param_dict_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            d: mutable dict[cstr, i64] @a = zeroed
            s: i64 = fill(d)
            if s != 100:
                panic("expected sum 100 (10+20+30+40)")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param dict fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param dict test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_dict_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param dict output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// End-to-end: the "any allocator" interface. A single generic function written
// against the `Allocator` protocol allocates + fills a buffer through BOTH a
// bump (Arena) backend and a malloc-backed backend, proving static-dispatch
// allocator polymorphism (region = lifetime + pluggable backing allocator).
// Also exercises mark/reset-to-mark reclamation (arena_snapshot/arena_rewind).
func TestRunCLIAllocatorInterfaceBumpAndMalloc(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	stdDir := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "allocator_iface_fixture.elisa")
	src := `include "` + filepath.Join(stdDir, "elisacore_runtime.elisa") + `"
include "` + filepath.Join(stdDir, "allocator.elisa") + `"

def fill_and_sum[A: Allocator](s: mutable A.State&, n: usize) -> u64 can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        raw: mutable heap void& = get A.allocate(s, n) else return 0u64
        trusted Unsafe.PointerCast:
            buf: mutable heap u8& = raw.cast[mutable heap u8&]
            sum: mutable u64 = 0u64
            for i in 0..<n.i64():
                buf[i] <- (i + 1).u8()
                sum <- sum + buf[i].u64()
            return sum

@test
def allocator_interface_test() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region a(4096):
            before: ArenaMark = arena_snapshot(a.ref[Arena&])
            bump_sum: u64 = fill_and_sum[BumpAllocator](a.ref[mutable Arena&], 10)
            if bump_sum != 55u64:
                panic("bump: expected 55 (1..10)")
            arena_rewind(a.ref[mutable Arena&], before)
            again: u64 = fill_and_sum[BumpAllocator](a.ref[mutable Arena&], 10)
            if again != 55u64:
                panic("bump after rewind: expected 55")
        m: mutable MallocAllocator = MallocAllocator(0)
        malloc_sum: u64 = fill_and_sum[MallocAllocator](m.ref[mutable MallocAllocator&], 10)
        if malloc_sum != 55u64:
            panic("malloc: expected 55 (1..10)")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write allocator-interface fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected allocator-interface test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] allocator_interface_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected allocator-interface output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// End-to-end: the safe `darray[u8] -> sview/cstr` conversions (design §5),
// replacing the unsafe `out[0].ref[static u8&]` idiom. `.as_sview()` is a bounded
// {data,len} view; `.as_cstr()` writes a NUL sentinel at items[count] (c_str()
// semantics) so the result is a valid NUL-terminated C-string. Byte-correct.
func TestRunCLIDarraySviewAndCstrConversions(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "darray_views.elisa")
	src := `@test
def darray_views_test() -> void:
    can Memory.Allocate, Abort.Panic:
        arena: mutable Arena = zeroed
        in arena:
            d: mutable darray[u8] = []
            d.push(72)
            d.push(105)
            sv: sview = d.as_sview()
            sum: mutable i64 = 0
            count: mutable i64 = 0
            for ch in sv:
                sum <- sum + ch.i64()
                count <- count + 1
            if count != 2:
                panic("sview len must be 2")
            if sum != 177:
                panic("sview byte sum must be 177 (72+105)")
            cs: cstr = d.as_cstr()
            clen: mutable i64 = 0
            for ch in cs:
                clen <- clen + 1
            if clen != 2:
                panic("cstr must be NUL-terminated after 2 bytes")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write darray-views fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected darray-views test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] darray_views_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected darray-views output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
