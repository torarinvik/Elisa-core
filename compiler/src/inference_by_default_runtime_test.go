package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Inference-by-default: a function that allocates with a bare container literal — no
// `region`, no `in auto:`, no `@r`, no threaded allocator — just works. The compiler
// wraps the body in a synthesized lazy auto region, and the allocation runs against it.
// This is the headline ergonomic: you don't have to mention regions to use them.
func TestRunCLIInfersRegionForBareAllocation(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_by_default_fixture.elisa")
	src := `@test
def inference_by_default_test() -> void:
    can Abort.Panic, Memory.Allocate:
        xs: mutable darray[i64] = []
        for i in 0..<5000:
            xs.push(i.i64())
        if xs[0] != 0 or xs[4999] != 4999:
            panic("inferred region: data wrong after growth")
        if xs.count != 5000:
            panic("inferred region: count wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write inference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected inference-by-default test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] inference_by_default_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected inference output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIInfersUntypedEmptyDArrayFromPush(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_untyped_empty_darray.elisa")
	src := `@test
def inference_untyped_empty_darray_test() -> void:
    can Abort.Panic, Memory.Allocate:
        xs = []
        xs.push([10, 20, 30])
        xs.push(40)
        if xs.count != 4:
            panic("expected four inferred darray items")
        if xs[0] != 10 or xs[3] != 40:
            panic("inferred darray contents wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write untyped darray inference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected untyped darray inference test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{"[       OK ] inference_untyped_empty_darray_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected untyped darray inference output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIInfersUntypedDArrayFromVariablePush(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_untyped_darray_variable_push.elisa")
	src := `@test
def inference_untyped_darray_variable_push_test() -> void:
    can Abort.Panic, Memory.Allocate:
        first: u8 = 7
        second: u8 = 8
        xs = []
        xs.push(first)
        xs.push(second)
        if xs.count != 2:
            panic("expected two inferred darray items")
        if xs[0] != 7 or xs[1] != 8:
            panic("inferred variable-push darray contents wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write variable-push darray inference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected variable-push darray inference test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{"[       OK ] inference_untyped_darray_variable_push_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected variable-push darray inference output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIInfersUntypedDArrayFromIndexedPush(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_untyped_darray_indexed_push.elisa")
	src := `@test
def inference_untyped_darray_indexed_push_test() -> void:
    can Abort.Panic, Memory.Allocate:
        src: mutable darray[u8] = []
        src.push([4, 5, 6])
        xs = []
        xs.push(src[0])
        xs.push(src[2])
        if xs.count != 2:
            panic("expected two inferred indexed-push items")
        if xs[0] != 4 or xs[1] != 6:
            panic("inferred indexed-push darray contents wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write indexed-push darray inference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected indexed-push darray inference test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{"[       OK ] inference_untyped_darray_indexed_push_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected indexed-push darray inference output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIInfersUntypedDArrayFromExtendSource(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_untyped_darray_extend_source.elisa")
	src := `@test
def inference_untyped_darray_extend_source_test() -> void:
    can Abort.Panic, Memory.Allocate:
        src: mutable darray[u8] = []
        src.push([1, 2, 3])
        xs = []
        xs.extend(src)
        if xs.count != 3:
            panic("expected three inferred darray items")
        if xs[0] != 1 or xs[2] != 3:
            panic("inferred extend-source darray contents wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write extend-source darray inference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected extend-source darray inference test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{"[       OK ] inference_untyped_darray_extend_source_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected extend-source darray inference output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIInfersUntypedDArrayFromExtendParam(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_untyped_darray_extend_param.elisa")
	src := `def copied_count(src: darray[u8]) -> usize:
    xs = []
    xs.extend(src)
    return xs.count

@test
def inference_untyped_darray_extend_param_test() -> void:
    can Abort.Panic, Memory.Allocate:
        src: mutable darray[u8] = []
        src.push([1, 2, 3])
        if copied_count(src) != 3:
            panic("expected extend-param inference to copy three items")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write extend-param darray inference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected extend-param darray inference test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{"[       OK ] inference_untyped_darray_extend_param_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected extend-param darray inference output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIInfersUntypedNonEmptyDArrayFromUse(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_untyped_nonempty_darray.elisa")
	src := `@test
def inference_untyped_nonempty_darray_test() -> void:
    can Abort.Panic, Memory.Allocate:
        xs = [10, 20, 30]
        xs.push(40)
        if xs.count != 4:
            panic("expected four inferred non-empty darray items")
        if xs[0] != 10 or xs[3] != 40:
            panic("inferred non-empty darray contents wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write non-empty darray inference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected non-empty darray inference test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{"[       OK ] inference_untyped_nonempty_darray_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected non-empty darray inference output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIInfersRegionForUntypedBuilders(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_by_default_builders.elisa")
	src := `@test
def inference_by_default_builders_test() -> void:
    can Abort.Panic, Memory.Allocate:
        src: mutable darray[u8] = []
        src.push([0, 1, 2, 3])
        comp = [item + 1 for item in src if item > 0]
        query = item + 2 for each item in src where item > 1
        if comp.count != 3 or comp[0] != 2 or comp[2] != 4:
            panic("inferred list comprehension result wrong")
        if query.count != 2 or query[0] != 4 or query[1] != 5:
            panic("inferred each query result wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write inference builders fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected inference-by-default builders test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{"[       OK ] inference_by_default_builders_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected inference builders output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// Inference's slack stays a diagnostic, not a leak: a function that builds a value in its
// inferred region and then returns it is rejected, because that region is freed at the
// function's exit. The fix is an explicit lifetime (a `[region r]` param and `-> ... @r`).
func TestRunCLIRejectsValueEscapingInferredFunctionRegion(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_escape_fixture.elisa")
	src := `def build() -> darray[i64]:
    xs: mutable darray[i64] = []
    xs.push(7)
    return xs
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write escape fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "escapes its `in auto:` scope") {
		t.Fatalf("expected an escape diagnostic for a value leaving the inferred region, got:\n%s", stderr.String())
	}
}
