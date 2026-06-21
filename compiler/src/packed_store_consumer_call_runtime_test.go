package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Regression for the storeless cross-function match soundness hole: a helper that takes a
// region-backed packed enum VALUE and matches it storeless (`expect e as E.Leaf(...)`, no
// `in E.Store`) must read the store the handle was built against. Before the fix, calling such a
// consumer under a different active region (`in owner:`) than the one the value was built under
// resolved the implicit `__packed_store_E` argument via getOrCreateRegionPackedStore, which minted
// a FRESH EMPTY region-local store — the call compiled cleanly and segfaulted at runtime (the
// shape of the former assert_perl_expr_* helpers in the Perl frontend tests). Consumer calls now
// thread `__packed_store_use_E`, which reuses the active binding.
func TestPackedStoreConsumerCallAcrossRegions(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
struct Span:
    lo: u32
    hi: u32

enum Base:
    common(span: Span)

enum E is Base:
        Invalid
        Leaf(value: i64)
        Pair(left: E, right: E)

def check_leaf(e: E, expected: i64) -> void:
    can Abort.Panic:
        expect e as E.Leaf(v)
        if v != expected:
            panic("consumer call read the wrong store")

def build() -> E:
    can Memory.Allocate:
        return new E.Pair(span: Span(lo: 0, hi: 9), left: new E.Leaf(span: Span(lo: 0, hi: 1), value: 42), right: new E.Leaf(span: Span(lo: 2, hi: 3), value: 7))

@test
def consumer_across_regions() -> void:
    can Abort.Panic, Memory.Allocate:
        with arena scratch(4096) as owner:
            e: E = build()
            in owner:
                expect e as E.Pair(left, right)
                check_leaf(left, 42)
                check_leaf(right, 7)
`
	dir := t.TempDir()
	path := filepath.Join(dir, "consumer_across_regions.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	exePath := ""
	for _, line := range strings.Split(stderr.String(), "\n") {
		if idx := strings.Index(line, "test binary: "); idx >= 0 {
			exePath = strings.TrimSpace(line[idx+len("test binary: "):])
			break
		}
	}
	if exePath == "" {
		t.Skipf("could not locate kept test binary:\n%s", stderr.String())
	}
	defer os.Remove(exePath)
	defer os.RemoveAll(exePath + ".dSYM")
	if out, err := exec.Command(exePath, "consumer_across_regions").CombinedOutput(); err != nil {
		t.Fatalf("consumer call across regions failed: %v\noutput:\n%s", err, string(out))
	}
}
