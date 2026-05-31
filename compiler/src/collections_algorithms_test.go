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
