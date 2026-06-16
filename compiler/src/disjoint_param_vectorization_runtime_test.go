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
// Increment 3b/4: the per-parameter alias.scope/noalias stamp on proven-distinct
// container-ref params must NEVER change results. The same program is built with
// the noalias stamp OFF and ON, run, and the emitted f64-checksum BIT PATTERNS must
// be identical. It also compares O0/no-stamp against O3/stamped, matching the
// default-on safety bar in docs/84 §4.
//
// The program exercises BOTH polarities (the part that makes this a real soundness
// test, not a tautology):
//   - AXPY: the minimal whole-array two-param kernel.
//   - Jacobi/stencil: separate read/write grids with awkward non-power-of-two sizes.
//   - Aliased stencil: called with the SAME darray twice; the predicate proves nothing,
//     so no stamp is emitted and aliasing semantics must be preserved.
//   - Fluid-frame-style multi-field update: several distinct mutable fields in one
//     kernel, covering noalias groups larger than two params.
//   - Stable-Fluids-style 2D Jacobi pressure/diffusion sweeps, adapted from the
//     real fluid_parallel_for_bench serial oracle but scaled down for a focused gate.
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

def jacobi_sweep(dst: mutable darray[f64]&, src: mutable darray[f64]&) -> void:
    for i in 1..<src.count - 1:
        dst[i] <- (src[i - 1] + src[i] + src[i + 1]) * 0.3333333333333333

def aliased_stencil(dst: mutable darray[f64]&, src: mutable darray[f64]&) -> void:
    for i in 1..<src.count - 1:
        dst[i] <- dst[i] + src[i - 1] * 0.125 + src[i + 1] * 0.25

def fluid_frame(u: mutable darray[f64]&, v: mutable darray[f64]&, u0: mutable darray[f64]&, v0: mutable darray[f64]&) -> void:
    for i in 0..<u.count:
        u[i] <- u[i] + u0[i] * 0.1 + v0[i] * 0.025
        v[i] <- v[i] + v0[i] * 0.1 - u0[i] * 0.015

def ix2(i: i64, j: i64, n: i64) -> usize:
    return (i + (n + 2) * j).usize()

def stable_fluid_jacobi_sweep(n: i64, xn: mutable darray[f64]&, x: mutable darray[f64]&, x0: mutable darray[f64]&, a: f64, c: f64) -> void:
    for j in 1..<(n + 1):
        for i in 1..<(n + 1):
            gi: usize = ix2(i, j, n)
            s: f64 = x[ix2(i - 1, j, n)] + x[ix2(i + 1, j, n)] + x[ix2(i, j - 1, n)] + x[ix2(i, j + 1, n)]
            xn[gi] <- (x0[gi] + a * s) / c

def stable_fluid_init_source(x0: mutable darray[f64]&, n: i64, size: i64) -> void:
    for k in 0..<size:
        x0[k.usize()] <- 0.0
    for j in 1..<(n + 1):
        for i in 1..<(n + 1):
            x0[ix2(i, j, n)] <- ((i * 7 + j * 13) %% 17).f64() * 0.01

def checksum(xs: darray[f64]) -> f64:
    s: mutable f64 = 0.0
    weight: mutable f64 = 1.0
    for v in xs:
        s <- s + v * weight
        weight <- weight + 0.0001
    return s

def build_distinct() -> f64:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    for i in 0..<2051:
        a.push(i.f64())
        b.push(i.f64() * 1.5)
    axpy_distinct(&a, &b)
    return checksum(a)

def build_aliased() -> f64:
    c: mutable darray[f64] = []
    for i in 0..<2051:
        c.push(i.f64() + 0.25)
    axpy_aliased(&c, &c)
    return checksum(c)

def build_jacobi() -> f64:
    src: mutable darray[f64] = []
    dst: mutable darray[f64] = []
    for i in 0..<4099:
        src.push((i.f64() * 0.5) + 1.0)
        dst.push(0.0)
    jacobi_sweep(&dst, &src)
    jacobi_sweep(&src, &dst)
    return checksum(src) + checksum(dst)

def build_aliased_stencil() -> f64:
    c: mutable darray[f64] = []
    for i in 0..<1027:
        c.push((i.f64() + 3.0) * 0.75)
    aliased_stencil(&c, &c)
    return checksum(c)

def build_fluid_frame() -> f64:
    u: mutable darray[f64] = []
    v: mutable darray[f64] = []
    u0: mutable darray[f64] = []
    v0: mutable darray[f64] = []
    for i in 0..<1539:
        f: f64 = i.f64()
        u.push(f * 0.01)
        v.push(f * -0.02)
        u0.push((f + 1.0) * 0.03)
        v0.push((f + 2.0) * -0.04)
    fluid_frame(&u, &v, &u0, &v0)
    fluid_frame(&u0, &v0, &u, &v)
    return checksum(u) + checksum(v) + checksum(u0) + checksum(v0)

def build_stable_fluid_pressure() -> f64:
    n: i64 = 31
    size: i64 = (n + 2) * (n + 2)
    iters: i64 = 17
    a: f64 = 0.25
    c: f64 = 1.0 + 4.0 * a
    x0: mutable darray[f64] = []
    buf_a: mutable darray[f64] = []
    buf_b: mutable darray[f64] = []
    _ = x0.resize(size.usize())
    _ = buf_a.resize(size.usize())
    _ = buf_b.resize(size.usize())
    stable_fluid_init_source(&x0, n, size)
    for k in 0..<size:
        buf_a[k.usize()] <- 0.0
        buf_b[k.usize()] <- 0.0
    toggle: mutable bool = true
    for it in 0..<iters:
        if toggle:
            stable_fluid_jacobi_sweep(n, &buf_b, &buf_a, &x0, a, c)
        else:
            stable_fluid_jacobi_sweep(n, &buf_a, &buf_b, &x0, a, c)
        toggle <- not toggle
    if toggle:
        return checksum(buf_a)
    return checksum(buf_b)

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    d: f64 = build_distinct()
    al: f64 = build_aliased()
    j: f64 = build_jacobi()
    st: f64 = build_aliased_stencil()
    fl: f64 = build_fluid_frame()
    sf: f64 = build_stable_fluid_pressure()
    print(d) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print((d * 1000000.0).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print(al) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print((al * 1000000.0).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print(j) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print((j * 1000000.0).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print(st) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print((st * 1000000.0).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print(fl) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print((fl * 1000000.0).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print(sf) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print(" ") can Console.Write
    print((sf * 1000000.0).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`, filepath.ToSlash(rel))

	fixturePath := filepath.Join(fixtureDir, "disjoint_axpy.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	buildAndRun := func(t *testing.T, opt backend.OptimizationLevel, noalias bool) string {
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
		exePath, cleanup, err := buildNativeExecutable(result, nil, nil, "", opt, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
		if err != nil {
			t.Fatalf("build native (opt=%v noalias=%v) failed: %v\n%s", opt, noalias, err, stderr.String())
		}
		defer cleanup()
		output, err := exec.Command(exePath).CombinedOutput()
		if err != nil {
			t.Fatalf("run native (opt=%v noalias=%v) failed: %v\n%s", opt, noalias, err, string(output))
		}
		return strings.TrimSpace(string(output))
	}

	o0Off := buildAndRun(t, backend.OptimizationLevel0, false)
	o3Off := buildAndRun(t, backend.OptimizationLevel3, false)
	o3On := buildAndRun(t, backend.OptimizationLevel3, true)
	if o0Off == "" {
		t.Fatalf("empty checksum output (O0 flag off)")
	}
	if o3Off != o3On {
		t.Fatalf("CHECKSUM DIVERGENCE: noalias stamp changed O3 results.\n off=%q\n  on=%q", o3Off, o3On)
	}
	if o0Off != o3On {
		t.Fatalf("CHECKSUM DIVERGENCE: O0/no-stamp and O3/stamped differ.\n  O0 off=%q\n  O3  on=%q", o0Off, o3On)
	}
	t.Logf("bit-identical checksum (O0 off == O3 off == O3 on): %s", o3On)
}
