package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// std `sort` (collections.elisa: introsort with a branchless Lomuto partition, a duplicate-run
// skip, a heapsort depth fallback and a final insertion pass). Every size around the 16-element
// insertion cutoff and every input shape that stresses a quicksort -- random, sorted, reverse,
// all equal, organ pipe, four distinct keys -- must come out ordered and a permutation of the
// input (sum + multiplicative-xor checksum).
func TestRunCLIStdSortIntrosortShapes(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixturePath := filepath.Join(t.TempDir(), "std_sort_runtime_fixture.elisa")
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std runtime path: %v", err)
	}
	src := `def sort_fx_less(a: i64, b: i64) -> bool:
    return a < b

def sort_fx_check(xs: mutable darray[i64]&, label: cstr) -> void can[Abort.Panic]:
    sum_before: mutable i64 = 0
    xor_before: mutable i64 = 0
    for x in xs |sum_before, xor_before|:
        sum_before <- sum_before + x
        xor_before <- xor_before ^ (x * 2654435761)
    sort[i64](xs, sort_fx_less)
    sum_after: mutable i64 = 0
    xor_after: mutable i64 = 0
    for x in xs |sum_after, xor_after|:
        sum_after <- sum_after + x
        xor_after <- xor_after ^ (x * 2654435761)
    panic(label) if sum_before != sum_after or xor_before != xor_after
    i: mutable usize = 1
    while i < xs.count |i|:
        panic(label) if xs[i] < xs[i - 1]
        i <- i + 1

@test
def std_sort_introsort_shapes() -> void:
    can Memory.Allocate, Abort.Panic:
        sizes: darray[usize] = [0, 1, 2, 3, 15, 16, 17, 18, 33, 100, 257, 1000, 100000]
        for n in sizes:
            for shape in 0..<6:
                xs: mutable darray[i64] = []
                seed: mutable i64 = 12345
                for k in 0..<n |seed, xs|:
                    seed <- (seed * 1103515245 + 12345) % 2147483648
                    ki: i64 = k.i64()
                    ni: i64 = n.i64()
                    v: i64 = seed if shape == 0 else (ki if shape == 1 else (ni - ki if shape == 2 else (7 if shape == 3 else (ki if ki < ni / 2 else ni - ki if shape == 4 else seed % 4))))
                    xs.push(v)
                sort_fx_check(&xs, "std sort: wrong result")
`
	full := "include \"" + std + "\"\n" + src
	if err := os.WriteFile(fixturePath, []byte(full), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("std sort runtime test failed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] std_sort_introsort_shapes", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
