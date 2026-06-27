package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunCLICollectionsRangeAlgorithms exercises the predicate-based range algorithms
// added to collections.elisa (any_of / all_of / count_if / find_if) end to end via UFCS,
// confirming they transliterate the C++ std algorithms and run correctly.
func TestRunCLICollectionsRangeAlgorithms(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
def is_big(v: i64) -> bool:
    return v > 7

def never(v: i64) -> bool:
    return false

@test
def range_algorithms() -> void:
    can Memory.Allocate, Abort.Panic:
        region scratch(64):
            xs: mutable darray[i64] = []
            in scratch:
                _ = xs.push(2)
                _ = xs.push(8)
                _ = xs.push(9)
            assert_eq(xs.any_of(is_big), true)
            assert_eq(xs.all_of(is_big), false)
            assert_eq(xs.count_if(is_big), 2)
            found: i64? = xs.find_if(is_big)
            assert_eq(found != null, true)
            miss: i64? = xs.find_if(never)
            assert_eq(miss == null, true)
`
	fixturePath := filepath.Join(fixtureDir, "range_algorithms.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected range-algorithms test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"[       OK ] range_algorithms",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

// TestRunCLICollectionsSortAndOrdering exercises the comparator-based ordering algorithms
// (sort / reverse / is_sorted / min_element / max_element) end to end, including ascending
// and descending comparators and the empty-range edge case.
func TestRunCLICollectionsSortAndOrdering(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
def asc(a: i64, b: i64) -> bool:
    return a < b

def desc(a: i64, b: i64) -> bool:
    return a > b

@test
def sort_and_ordering() -> void:
    can Memory.Allocate, Abort.Panic:
        region scratch(256):
            xs: mutable darray[i64] = []
            in scratch:
                _ = xs.push(5)
                _ = xs.push(2)
                _ = xs.push(9)
                _ = xs.push(2)
                _ = xs.push(1)
            xs.sort(asc)
            assert_eq(xs.is_sorted(asc), true)
            assert_eq(xs[0], 1)
            assert_eq(xs[4], 9)
            assert_eq(get xs.min_element(asc) else 0, 1)
            assert_eq(get xs.max_element(asc) else 0, 9)
            xs.sort(desc)
            assert_eq(xs[0], 9)
            assert_eq(xs[4], 1)
            xs.reverse()
            assert_eq(xs[0], 1)
            empty: mutable darray[i64] = []
            assert_eq(empty.min_element(asc) == null, true)
            assert_eq(empty.is_sorted(asc), true)
`
	fixturePath := filepath.Join(fixtureDir, "sort_and_ordering.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected sort/ordering test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"[       OK ] sort_and_ordering",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

// TestRunCLICollectionsBinarySearch exercises lower_bound / binary_search over a sorted
// range, covering hits, misses, and the below-first / past-last boundary positions.
func TestRunCLICollectionsBinarySearch(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
def asc(a: i64, b: i64) -> bool:
    return a < b

@test
def binary_search_lookups() -> void:
    can Memory.Allocate, Abort.Panic:
        region scratch(128):
            xs: mutable darray[i64] = []
            in scratch:
                _ = xs.push(1)
                _ = xs.push(3)
                _ = xs.push(5)
                _ = xs.push(7)
            assert_eq(xs.lower_bound(5, asc), 2)
            assert_eq(xs.lower_bound(4, asc), 2)
            assert_eq(xs.lower_bound(0, asc), 0)
            assert_eq(xs.lower_bound(99, asc), 4)
            assert_eq(xs.binary_search(5, asc), true)
            assert_eq(xs.binary_search(4, asc), false)
`
	fixturePath := filepath.Join(fixtureDir, "binary_search.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected binary-search test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"[       OK ] binary_search_lookups",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

// TestRunCLICollectionsFold exercises fold/reduce, including the no-expected-type-context
// position (a fold call passed directly to a generic assert_eq) that previously failed to
// infer the accumulator type param. Regression guard for the collectTypeBindings fix.
func TestRunCLICollectionsFold(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
@test
def fold_reduce() -> void:
    can Memory.Allocate, Abort.Panic:
        region scratch(64):
            xs: mutable darray[i64] = []
            in scratch:
                _ = xs.push(3)
                _ = xs.push(7)
                _ = xs.push(2)
            z: i64 = 0
            sum: i64 = (acc + x for x in xs with acc = z)
            max_value: i64 = (x if x > acc else acc for x in xs with acc = z)
            assert_eq(sum, 12)
            assert_eq(max_value, 7)
`
	fixturePath := filepath.Join(fixtureDir, "fold_reduce.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected fold test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"[       OK ] fold_reduce",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

// TestRunCLIStringsHashSview verifies the FNV-1a hash_sview (std::hash<string_view>):
// determinism, sensitivity to a one-byte change, and the empty-string FNV offset basis.
func TestRunCLIStringsHashSview(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
@test
def hash_sview_fnv1a() -> void:
    can Abort.Panic:
        a: u64 = hash_sview(sview("abc", 0, -1))
        b: u64 = hash_sview(sview("abc", 0, -1))
        c: u64 = hash_sview(sview("abd", 0, -1))
        e: u64 = hash_sview(sview("", 0, 0))
        assert_eq(a == b, true)
        assert_eq(a == c, false)
        assert_eq(a == 0, false)
        assert_eq(e == 0xcbf29ce484222325, true)
`
	fixturePath := filepath.Join(fixtureDir, "hash_sview.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected hash_sview test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"[       OK ] hash_sview_fnv1a",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

// TestRunCLIFsPathOps verifies the Fs:: path-decomposition module (filename / parent /
// extension / stem) over normal, bare, hidden-file, and dot-in-parent paths.
func TestRunCLIFsPathOps(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
def eq(v: sview, lit: static u8&) -> bool:
    return sview_eq(v, sview(lit, 0, -1))

@test
def fs_path_ops() -> void:
    can Abort.Panic:
        p: sview = sview("dir/sub/file.txt", 0, -1)
        assert_eq(eq(Fs::filename(p), "file.txt"), true)
        assert_eq(eq(Fs::parent(p), "dir/sub"), true)
        assert_eq(eq(Fs::extension(p), ".txt"), true)
        assert_eq(eq(Fs::stem(p), "file"), true)
        assert_eq(eq(Fs::filename(sview("name", 0, -1)), "name"), true)
        assert_eq(eq(Fs::extension(sview("name", 0, -1)), ""), true)
        assert_eq(eq(Fs::extension(sview("dir/.gitignore", 0, -1)), ""), true)
        assert_eq(eq(Fs::stem(sview("dir/.gitignore", 0, -1)), ".gitignore"), true)
        assert_eq(eq(Fs::extension(sview("a.b/c", 0, -1)), ""), true)
`
	fixturePath := filepath.Join(fixtureDir, "fs_path_ops.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected Fs path-ops test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"[       OK ] fs_path_ops",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

// TestRunCLIFsJoin verifies Fs::join (path concatenation into an arena): separator
// insertion, no doubled slash when base ends with one, and absolute-leaf replacement.
func TestRunCLIFsJoin(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
def eqd(d: dstr, lit: static u8&) -> bool:
    return sview_eq(bytes_view(d), sview(lit, 0, -1))

@test
def fs_join() -> void:
    can Memory.Allocate, Abort.Panic:
        region scratch(256):
            o: mutable Arena& = &scratch
            assert_eq(eqd(Fs::join(o, sview("a/b", 0, -1), sview("c", 0, -1)), "a/b/c"), true)
            assert_eq(eqd(Fs::join(o, sview("a/b/", 0, -1), sview("c", 0, -1)), "a/b/c"), true)
            assert_eq(eqd(Fs::join(o, sview("a", 0, -1), sview("/abs", 0, -1)), "/abs"), true)
            assert_eq(eqd(Fs::join(o, sview("", 0, -1), sview("c", 0, -1)), "c"), true)
`
	fixturePath := filepath.Join(fixtureDir, "fs_join.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected Fs::join test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"[       OK ] fs_join",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}
