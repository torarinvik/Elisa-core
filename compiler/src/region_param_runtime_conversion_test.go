package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIInferredRegionBuildersUseActiveRegion(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inferred_region_builders.elisa")
	src := `@test
def inferred_region_builders_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            src: mutable darray[u8] @a = []
            src.push([0, 1, 2, 3])
            comp = [item + 1 for item in src if item > 0]
            query = item + 2 for each item in src where item > 1
            if comp.count != 3 or comp[0] != 2 or comp[2] != 4:
                panic("unexpected list comprehension result")
            if query.count != 2 or query[0] != 4 or query[1] != 5:
                panic("unexpected each query result")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write inferred-region builders fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected inferred-region builders test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] inferred_region_builders_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected inferred-region builders output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// End-to-end: the "any allocator" interface. A single generic function written
// against the `Allocator` protocol allocates + fills a buffer through BOTH a
// bump (Arena) backend and a malloc-backed backend, proving static-dispatch
// allocator polymorphism (region = lifetime + pluggable backing allocator).
// Also exercises mark/reset-to-mark reclamation (arena_snapshot/arena_rewind).
func TestRunCLIAllocatorInterfaceBumpAndMalloc(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	stdDir := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "allocator_iface_fixture.elisa")
	src := `include "` + filepath.Join(stdDir, "elisacore_runtime.elisa") + `"
include "` + filepath.Join(stdDir, "allocator.elisa") + `"

def fill_and_sum[A: Allocator](s: mutable A.State&, n: usize) -> u64 can[Memory.Allocate, Abort.Panic, Atomics{Exchange, Store}, Global{Read, Write}]:
    can Memory.Allocate, Abort.Panic, Atomics{Exchange, Store}, Global{Read, Write}:
        raw: mutable heap void& = get A.allocate(s, n) else return 0
        trusted Unsafe.PointerCast:
            buf: mutable heap u8& = raw.cast[mutable heap u8&]
            sum: mutable u64 = 0
            for i in 0..<n.i64():
                buf[i] <- (i + 1).u8()
                sum <- sum + buf[i].u64()
            return sum

@test
def allocator_interface_test() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic, Atomics{Exchange, Store}, Global{Read, Write}:
        region a(4096):
            alloc: mutable Arena& = &a
            before: ArenaMark = arena_snapshot(alloc)
            bump_sum: u64 = fill_and_sum[BumpAllocator](alloc, 10)
            if bump_sum != 55:
                panic("bump: expected 55 (1..10)")
            arena_rewind(alloc, before)
            again: u64 = fill_and_sum[BumpAllocator](alloc, 10)
            if again != 55:
                panic("bump after rewind: expected 55")
        m: mutable MallocAllocator = MallocAllocator{tag: 0}
        malloc_sum: u64 = fill_and_sum[MallocAllocator](&m, 10)
        if malloc_sum != 55:
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
	t.Parallel()
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

// Region-param version of `.as_cstr()`: the helper has no ambient `in <arena>:`
// scope, so NUL-termination must grow through the hidden caller-threaded region
// arena for r. This guards the region-aware cstr path specifically.
func TestRunCLIRegionParamDarrayCstrThreadsArenaViaHiddenParam(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_darray_cstr.elisa")
	src := `def cstrlen[@r](d: mutable darray[u8] @r) -> i64:
    cs: cstr = d.as_cstr()
    n: mutable i64 = 0
    for ch in cs:
        n <- n + 1
    return n

@test
def region_param_darray_cstr_test() -> void:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            d: mutable darray[u8] @a = []
            d.push([72, 105])
            n: i64 = cstrlen(d)
            if n != 2:
                panic("expected cstr len 2")
            if d.count != 2.usize():
                panic("as_cstr must not change darray count")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param darray-cstr fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param darray-cstr test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_darray_cstr_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param darray-cstr output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIRegionParamDarrayLiteralThreadsArenaViaHiddenParam(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_darray_literal.elisa")
	src := `def build[@r](anchor: mutable darray[u8] @r) -> u64:
    xs: darray[u8] @r = [10, 20, 30]
    sum: mutable u64 = 0
    for i in 0..<xs.count.i64():
        sum <- sum + xs[i].u64()
    return sum

@test
def region_param_darray_literal_test() -> void:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            anchor: mutable darray[u8] @a = []
            sum: u64 = build(anchor)
            if sum != 60:
                panic("expected sum 60")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param darray-literal fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param darray-literal test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_darray_literal_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param darray-literal output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
