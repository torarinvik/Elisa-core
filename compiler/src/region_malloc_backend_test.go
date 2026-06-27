package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunCLIRegionUsingMallocBacksBlocksAcrossRuntimeObject exercises `region r(...) using malloc:`
// end to end. The fixture includes only test.elisa (for assert_eq), so the arena runtime — including
// new_region_backend / free_region_backend — resolves from the separately linked default runtime
// object. This is the exact cross-object path that requires those helpers to carry external linkage
// (see isDefaultNativeRuntimeSupportExport); without that, a malloc-backed region segfaults at region
// init on an unresolved symbol. region(2) is one i64-sized block, so the 4-element darray forces a
// block growth through new_region_backend(MALLOC), and destroy frees via free_region_backend(MALLOC).
func TestRunCLIRegionUsingMallocBacksBlocksAcrossRuntimeObject(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_using_malloc_fixture.elisa")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q

def sum_malloc() -> i64:
    can Memory.Allocate:
        region scratch(2) using malloc
        in scratch:
            values: darray[i64] = [11, 22, 33, 44]
            out: i64 = values[0] + values[1] + values[2] + values[3]
            destroy scratch
            return out

def sum_default() -> i64:
    can Memory.Allocate:
        region scratch(2)
        in scratch:
            values: darray[i64] = [11, 22, 33, 44]
            out: i64 = values[0] + values[1] + values[2] + values[3]
            destroy scratch
            return out

@test
def region_using_malloc_grows_and_frees() -> void:
    can Abort.Panic, Memory.Allocate:
        assert_eq(sum_malloc(), 110)

@test
def region_default_and_malloc_agree() -> void:
    can Abort.Panic, Memory.Allocate:
        assert_eq(sum_malloc(), sum_default())
`, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-using-malloc fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected malloc-backed region test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[       OK ] region_using_malloc_grows_and_frees",
		"[       OK ] region_default_and_malloc_agree",
		"[ SUMMARY  ] 2 test(s) selected; passed=2 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected malloc-backed region output to contain %q, got:\n%s", check, output)
		}
	}
}

// TestRunCLIRegionRejectsUnknownAllocator confirms the `using <allocator>` surface only accepts the
// supported allocator names; an unknown one is a semantic error rather than silently ignored.
func TestRunCLIRegionRejectsUnknownAllocator(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_unknown_allocator_fixture.elisa")
	src := `def f() -> i32:
    region scratch(8) using bogus:
        return 0
    return 1
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write unknown-allocator fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected analysis of an unknown region allocator to fail, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "unknown region backing") {
		t.Fatalf("expected an \"unknown region backing\" diagnostic, got:\n%s", combined)
	}
}
