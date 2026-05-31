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
            assert_eq(xs.min_element(asc) else 0, 1)
            assert_eq(xs.max_element(asc) else 0, 9)
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
