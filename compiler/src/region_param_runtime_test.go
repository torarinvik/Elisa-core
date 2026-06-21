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
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_push_fixture.elisa")
	src := `def fill[@r](out: mutable darray[u8] @r) -> u64:
    for i in 0..<10:
        out.push((65 + i).u8())
    sum: mutable u64 = 0
    for i in 0..<out.count.i64():
        sum <- sum + out[i].u64()
    return sum

@test
def region_param_push_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            v: mutable darray[u8] @a = []
            s: u64 = fill(v)
            if s != 695:
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

func TestRunCLIDArrayPushArrayLiteralBulkAppend(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "darray_push_array_literal_fixture.elisa")
	src := `def fill[@r](out: mutable darray[u8] @r) -> u64:
    out.push([10, 20, 30])
    out.extend([40, 50])
    sum: mutable u64 = 0
    for i in 0..<out.count.i64():
        sum <- sum + out[i].u64()
    return sum

@test
def darray_push_array_literal_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            v: mutable darray[u8] @a = []
            s: u64 = fill(v)
            if s != 150:
                panic("expected sum 150")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write darray push array literal fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected darray push array literal test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] darray_push_array_literal_test") {
		t.Fatalf("expected darray push array literal output to pass, got:\n%s", stdout.String())
	}
}

// End-to-end: a list comprehension whose result is explicitly `@r` can allocate
// through the region-param hidden arena, with no ambient `in <arena>:` scope in
// the helper. This is the comprehension analogue of darray.push threading: the
// generated result darray carries region r, so its synthesized pushes source
// the caller's arena.
func TestRunCLIRegionParamListComprehensionThreadsArenaViaHiddenParam(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_list_comp_fixture.elisa")
	src := `def bumped[@r](items: darray[u8] @r) -> u64:
    out: darray[u8] @r = [item + 1 for item in items]
    sum: mutable u64 = 0
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
            if s != 63:
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

func TestRunCLIRegionParamEachQueryThreadsArenaViaHiddenParam(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_each_query_fixture.elisa")
	src := `def bumped[@r](items: darray[u8] @r) -> u64:
    out: darray[u8] @r = item + 1 for each item in items
    sum: mutable u64 = 0
    for i in 0..<out.count.i64():
        sum <- sum + out[i].u64()
    return sum

@test
def region_param_each_query_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            v: mutable darray[u8] @a = []
            v.push([10, 20, 30])
            s: u64 = bumped(v)
            if s != 63:
                panic("expected sum 63 ((10+1)+(20+1)+(30+1))")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param each-query fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param each-query test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_each_query_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param each-query output to contain %q, got:\n%s", check, stdout.String())
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
	t.Parallel()
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

def fill[@r](d: mutable dict[cstr, i64] @r) -> i64:
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

func TestRunCLIRegionParamDictPutThreadsArenaViaHiddenParam(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	stdDir := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_dict_put_fixture.elisa")
	src := `include "` + filepath.Join(stdDir, "elisacore_runtime.elisa") + `"
include "` + filepath.Join(stdDir, "heap.elisa") + `"

def fill[@r](d: mutable dict[cstr, i64] @r) -> i64:
    d.reserve(8)
    _ = d.put("alpha", 10)
    _ = d.put("beta", 20)
    _ = d.get_or_insert("gamma", 30)
    _ = d.get_or_insert("delta", 40)
    sum: mutable i64 = 0
    sa: i64& = get d.get("alpha") else return -1
    sum <- sum + sa[0]
    sb: i64& = get d.get("beta") else return -2
    sum <- sum + sb[0]
    sc: i64& = get d.get("gamma") else return -3
    sum <- sum + sc[0]
    sd: i64& = get d.get("delta") else return -4
    sum <- sum + sd[0]
    return sum

@test
def region_param_dict_put_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            d: mutable dict[cstr, i64] @a = zeroed
            s: i64 = fill(d)
            if s != 100:
                panic("expected sum 100 (10+20+30+40)")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param dict put fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param dict put test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_dict_put_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param dict put output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIRegionScopeStoreGrowthUsesRegionArena(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_scope_store_growth.elisa")
	src := `layout soa struct RegionStoreGrowthRows:
    name_key: u32
    depth: u32

@test
def region_scope_store_growth_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            pending: mutable RegionStoreGrowthRows = zeroed
            pending.reserve(4)
            pending.push(1, 2)
            pending.push(3, 4)
            if pending.name_key.count != 2:
                panic("expected name_key count 2")
            if pending.depth.count != 2:
                panic("expected depth count 2")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-scope store-growth fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-scope store-growth test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_scope_store_growth_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-scope store-growth output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIRegionParamCloneDArrayUsesHiddenArena(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_clone_darray.elisa")
	src := `def copy[@r](items: darray[u8] @r) -> darray[u8] @r:
    return clone[darray[u8] @r](items)

@test
def region_param_clone_darray_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            src: mutable darray[u8] @a = []
            src.push([10, 20, 30])
            cloned: darray[u8] @a = copy(src)
            if cloned.count != 3:
                panic("expected clone count 3")
            if cloned[0] != 10 or cloned[1] != 20 or cloned[2] != 30:
                panic("unexpected clone contents")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param clone fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param clone test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_clone_darray_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param clone output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIRegionParamSequenceRewriteUsesHiddenArena(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_sequence_rewrite.elisa")
	src := `def keep_non_zero[@r](items: darray[u32] @r) -> darray[u32] @r:
    return rewrite items as sequence[u32]:
        item when item != 0:
            emit item

@test
def region_param_sequence_rewrite_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            src: mutable darray[u32] @a = []
            src.push([0, 7, 0, 9])
            kept: darray[u32] @a = keep_non_zero(src)
            if kept.count != 2:
                panic("expected two kept items")
            if kept[0] != 7 or kept[1] != 9:
                panic("unexpected kept contents")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param sequence rewrite fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param sequence rewrite test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_sequence_rewrite_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param sequence rewrite output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// Regression: a `rewrite ... as sequence` whose source is a by-value darray
// parameter used to spill the 24-byte darray aggregate through a 16-byte `view`
// alloca, overflowing the adjacent stack slot (the `alloc` arena pointer) at -O0.
// The result was arena_alloc() seeing a garbage arena (the source darray's
// capacity field) and segfaulting. This only manifested at -O0; mem2reg hid it
// at -O2/-O3 by deleting the dead spill. (Was the Lua frontend's nested-vararg
// RETURN_VARARG crash cluster.)
func TestRunCLISequenceRewriteOverByValueDArrayParamNoStackOverflow(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "sequence_rewrite_param_overflow.elisa")
	src := `struct Box:
    a: i64
    b: i64

def copy_list(alloc: mutable Arena&, values: darray[Box]) -> darray[Box]:
    can Abort.Panic, Memory.Allocate:
        in alloc:
            return rewrite values as sequence[Box]:
                value:
                    emit value

def run(owner: Arena) -> i64 can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        alloc: mutable Arena& = (&owner).cast[mutable Arena&]
        src: mutable darray[Box] = []
        in alloc:
            src <- []
            src.push(Box(4, 2))
            src.push(Box(1, 7))
        out: darray[Box] = copy_list(alloc, src)
        return out[0].a * 10 + out[1].b

@test
def sequence_rewrite_param_overflow_test() -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        region scratch(65536):
            if run(scratch) != 47:
                panic("rewrite over by-value darray param produced wrong result")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write sequence-rewrite overflow fixture: %v", err)
	}

	// Force -O0: the bug is a dead-store stack overflow that optimization elides.
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", "-O0", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected sequence-rewrite param test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] sequence_rewrite_param_overflow_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected sequence-rewrite param output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

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

def fill_and_sum[A: Allocator](s: mutable A.State&, n: usize) -> u64 can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
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
    can Memory.Allocate, Memory.Release, Abort.Panic:
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
        m: mutable MallocAllocator = MallocAllocator(0)
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
