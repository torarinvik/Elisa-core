package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `by simd` on a list-map emits the body under full fast-math FP (the map analogue of the per-fold
// marker): the plain map's body carries only `contract` flags, the `by simd` map's body carries
// `fast`.
func TestBySimdMapAppliesFastMath(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "by_simd_map_probe.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", "-O3", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("emit llvm failed (%d):\n%s", code, stderr.String())
	}
	out := stdout.String()
	body := func(fn string) string {
		return regexp.MustCompile(`(?s)define[^\n]*@` + fn + `\(.*?\n\}`).FindString(out)
	}
	if strings.Contains(body("probe_plain"), "fmul fast") {
		t.Fatalf("plain map body must not carry `fast` FP flags:\n%s", body("probe_plain"))
	}
	if !strings.Contains(body("probe_simd"), "fmul fast") {
		t.Fatalf("`by simd` map body must carry `fast` FP flags:\n%s", body("probe_simd"))
	}
}

// by_simd_map_smoke pins that `by simd` maps compute correct values (indexed-store + range source).
func TestRunCLIBySimdMapSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "by_simd_map_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("by simd map smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] by_simd_map_smoke") {
		t.Fatalf("expected by_simd_map_smoke to pass, got:\n%s", stdout.String())
	}
}

// `by par` on a map comprehension is reserved but not yet implemented; it must produce a clear,
// actionable error (not silently parse as a sequential map).
func TestByParOnMapIsRejected(t *testing.T) {
	dir := t.TempDir()
	src := "include \"" + filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa") + "\"\n" +
		"def t(a: darray[i64]&) -> usize:\n\tcan Memory.Allocate, Abort.Panic:\n\t\tout: darray[i64] = [x * 2 for x in a by par]\n\t\treturn out.count\n"
	path := filepath.Join(dir, "bymap.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "`by par` is not yet supported on map comprehensions") {
		t.Fatalf("expected a clear `by par` on map error, got stderr:\n%s", stderr.String())
	}
}
