package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/backend"
)

// TestDisjointParamVectorizationBitIdentical is the docs/84 §4 soundness gate for
// Increment 3b: the per-parameter alias.scope/noalias stamp on proven-distinct
// container-ref params must NEVER change results. The same program is built at -O3
// once with the noalias stamp OFF (env unset) and once ON (env set), run, and the
// emitted f64-checksum BIT PATTERNS must be identical.
//
// The program exercises BOTH polarities (the part that makes this a real soundness
// test, not a tautology):
//   - axpy_distinct: only ever called with two distinct private-fresh darray locals,
//     so the whole-program FuncDisjointParams predicate proves the pair distinct and
//     the backend stamps the disjoint alias scopes. This is the path under test.
//   - axpy_aliased:  called with the SAME darray twice (y == x). The predicate proves
//     NOTHING (self-alias fails closed), so no stamp is emitted; the aliased aliasing
//     semantics (y[i] += y[i] doubling) must be preserved with and without the flag.
//
// Bit-identical checksums across both kernels and both flag states is the proof.
func TestDisjointParamVectorizationBitIdentical(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	if _, err := os.Stat(std); err != nil {
		t.Skipf("std runtime not found: %v", err)
	}
	fixtureDir := t.TempDir()
	rel, err := filepath.Rel(fixtureDir, std)
	if err != nil {
		t.Fatalf("rel include: %v", err)
	}

	// The checksums are printed both as a decimal f64 AND as a large integer scaling
	// (round(sum*1e6)) so a low-bit divergence still shows up as an integer mismatch.
	// No fast-math is enabled, so the vectorizer does not reassociate the FP fold and
	// the values must be exactly equal across flag states.
	src := fmt.Sprintf(`# include %q

def axpy_distinct(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]

def axpy_aliased(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]

def build_distinct() -> f64:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    for i in 0..<2048:
        a.push(i.f64())
        b.push(i.f64() * 1.5)
    axpy_distinct(&a, &b)
    s: mutable f64 = 0.0
    for v in a:
        s <- s + v
    return s

def build_aliased() -> f64:
    c: mutable darray[f64] = []
    for i in 0..<2048:
        c.push(i.f64() + 0.25)
    axpy_aliased(&c, &c)
    s: mutable f64 = 0.0
    for v in c:
        s <- s + v
    return s

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    d: f64 = build_distinct()
    al: f64 = build_aliased()
    print(d) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print((d * 1000000.0).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print(al) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print((al * 1000000.0).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`, filepath.ToSlash(rel))

	fixturePath := filepath.Join(fixtureDir, "disjoint_axpy.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	buildAndRun := func(t *testing.T, noalias bool) string {
		t.Helper()
		const envKey = "ELISACORE_NOALIAS_MUTABLE_REFS"
		prev, had := os.LookupEnv(envKey)
		if noalias {
			_ = os.Setenv(envKey, "1")
		} else {
			_ = os.Unsetenv(envKey)
		}
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(envKey, prev)
			} else {
				_ = os.Unsetenv(envKey)
			}
		})

		var stderr bytes.Buffer
		expanded, err := readSourceWithIncludes(fixturePath, map[string]bool{})
		if err != nil {
			t.Fatalf("expand includes: %v", err)
		}
		_, result, ok := analyzeProgram(fixturePath, expanded, &stderr)
		if !ok {
			t.Fatalf("analyze fixture failed:\n%s", stderr.String())
		}
		exePath, cleanup, err := buildNativeExecutable(result, nil, nil, "", backend.OptimizationLevel3, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
		if err != nil {
			t.Fatalf("build native (noalias=%v) failed: %v\n%s", noalias, err, stderr.String())
		}
		defer cleanup()
		output, err := exec.Command(exePath).CombinedOutput()
		if err != nil {
			t.Fatalf("run native (noalias=%v) failed: %v\n%s", noalias, err, string(output))
		}
		return strings.TrimSpace(string(output))
	}

	off := buildAndRun(t, false)
	on := buildAndRun(t, true)
	if off == "" {
		t.Fatalf("empty checksum output (flag off)")
	}
	if off != on {
		t.Fatalf("CHECKSUM DIVERGENCE: noalias stamp changed results.\n off=%q\n  on=%q", off, on)
	}
	t.Logf("bit-identical checksum (off==on): %s", on)
}
