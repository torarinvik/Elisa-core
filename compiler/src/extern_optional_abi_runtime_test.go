package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An extern declared `-> T?` used to lower Elisa's {tag, payload} optional struct straight
// into the native declaration, so the call read its tag out of whichever register the C
// function left its pointer in — every such call bound as null, with no diagnostic. These
// tests pin the C representation (a plain nullable pointer) in both directions, and pin
// the rejection of optionals that have no such representation.
func TestRunCLIProjectTestBindsExternOptionalHandleReturns(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	projectRoot := t.TempDir()
	for _, dir := range []string{
		filepath.Join(projectRoot, "native"),
		filepath.Join(projectRoot, "test"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "foreign": ["native/optional_probe.c"],
  "targets": {
    "tests": {
      "entry": "test/optional_tests.elisa",
      "emit": "llvm",
      "output": "build/optional_tests.ll",
      "opt": "O0"
    }
  }
}
`)
	// A C module in the shape the bug was found in: an opaque handle looked up by name,
	// returning NULL when absent. `elisa_opt_handle_is` reads a handle back so the
	// argument direction is checked against a real callee too.
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "optional_probe.c"), `#include <stdint.h>
#include <string.h>

struct elisa_opt_handle { int64_t value; };
static struct elisa_opt_handle elisa_opt_known = { 42 };

/* Returns a real, non-NULL handle for "known"; NULL for anything else. */
struct elisa_opt_handle *elisa_opt_lookup(const char *name) {
    if (name && strcmp(name, "known") == 0) {
        return &elisa_opt_known;
    }
    return 0;
}

/* Reads the handle back: -1 marks the NULL (absent) case. */
int64_t elisa_opt_handle_value(struct elisa_opt_handle *h) {
    return h ? h->value : -1;
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "test", "optional_tests.elisa"), `extern OptHandle
extern elisa_opt_lookup(name: u8&) -> OptHandle?
extern elisa_opt_handle_value(h: OptHandle?) -> i64

@test
def extern_optional_return_binds_non_null_handle() -> void:
    can Abort.Panic:
        found: OptHandle? = elisa_opt_lookup("known")
        if not (found is handle):
            panic("extern -> T? bound null for a handle C returned non-NULL")

@test
def extern_optional_return_is_null_for_null_handle() -> void:
    can Abort.Panic:
        missing: OptHandle? = elisa_opt_lookup("absent")
        if missing is handle:
            panic("extern -> T? bound a value for a handle C returned as NULL")

@test
def extern_optional_return_round_trips_payload() -> void:
    can Abort.Panic:
        found: OptHandle? = elisa_opt_lookup("known")
        if found is handle:
            if elisa_opt_handle_value(found) != 42:
                panic("extern -> T? payload did not survive the C boundary")
        else:
            panic("extern -> T? bound null for a handle C returned non-NULL")

@test
def extern_optional_argument_passes_payload_and_null() -> void:
    can Abort.Panic:
        present: OptHandle? = elisa_opt_lookup("known")
        if elisa_opt_handle_value(present) != 42:
            panic("a bound T? argument did not reach C as its payload pointer")
        absent: OptHandle? = elisa_opt_lookup("absent")
        if elisa_opt_handle_value(absent) != -1:
            panic("an absent T? argument did not reach C as NULL")
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"test", "tests", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected extern optional project test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] extern_optional_return_binds_non_null_handle",
		"[       OK ] extern_optional_return_is_null_for_null_handle",
		"[       OK ] extern_optional_return_round_trips_payload",
		"[       OK ] extern_optional_argument_passes_payload_and_null",
		"[ SUMMARY  ] 4 test(s) selected; passed=4 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// `extern` does not mean "native C" — a module interface (.elisai) declares its own Elisa
// functions with `extern` too, and those use Elisa's ABI, where `T?` is an ordinary
// optional. C has no generics, so a GENERIC extern is necessarily such a declaration and
// must be left alone: neither rejected for having a non-niche optional nor lowered as a
// nullable C pointer. This is the shape the whole elisacore_std interface is written in
// (`extern find_if[T](da: darray[T], pred: fn(T) -> bool) -> T?`).
func TestRunCLIAcceptsGenericExternOptionalAsElisaInterface(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fixture := filepath.Join(dir, "generic_extern_optional.elisa")
	writeFixtureFile(t, fixture, `extern find_if[T](da: darray[T], pred: fn(T) -> bool) -> T?
extern first_index[K, T](m: darray[K], key: K) -> usize?

def find_if[T](da: darray[T], pred: fn(T) -> bool) -> T?:
    for v in da:
        if pred(v):
            return v
    return null

def main() -> i64:
    xs: darray[i64] = [1, 2, 3]
    found: i64? = find_if[i64](xs, fn(v: i64) => v > 1)
    if found is hit:
        return hit
    return 0
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", "-o", filepath.Join(dir, "out.ll"), fixture}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected a generic extern returning T? to be accepted as an Elisa interface declaration, stderr:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "cannot be the optional type") {
		t.Fatalf("generic extern was wrongly treated as a C ABI boundary, stderr:\n%s", stderr.String())
	}
}

// An optional whose payload has no spare null cannot be encoded at a C boundary at all.
// It must be a hard error rather than silently mis-lowered.
func TestRunCLIRejectsExternOptionalWithoutNullNiche(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fixture := filepath.Join(dir, "extern_optional_no_niche.elisa")
	writeFixtureFile(t, fixture, `extern maybe_count() -> i64?
extern take_count(x: i32?) -> void

def main() -> i64:
    return 0
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", "-o", filepath.Join(dir, "out.ll"), fixture}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected an extern optional without a null niche to be rejected, stdout:\n%s", stdout.String())
	}
	for _, check := range []string{
		`return type of extern "maybe_count" cannot be the optional type i64?`,
		`parameter "x" of extern "take_count" cannot be the optional type i32?`,
	} {
		if !strings.Contains(stderr.String(), check) {
			t.Fatalf("expected diagnostic to contain %q, got:\n%s", check, stderr.String())
		}
	}
}
