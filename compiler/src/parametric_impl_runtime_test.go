package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Parametric (blanket) static-interface impls: one `impl[T] Builder for BoxTag[T]` covers
// every instantiation, and a concrete `BoxTag[i64]` is matched by unifying the receiver
// against the impl's `BoxTag[T]` pattern. This is the prerequisite for the Store/Handle
// unification (docs/69), where `impl[T] Store for darray[T]` must cover all element types.
func TestRunCLIParametricImplDispatch(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "parametric_impl_fixture.elisa")
	src := `protocol Builder:
    type Node
    def make(value: i64) -> Node

struct BoxTag[T]:
    tag: T

impl[T] Builder for BoxTag[T]:
    type Node = i64
    def make(value: i64) -> i64:
        return value

def build[B: Builder](value: i64) -> B.Node:
    return B.make(value)

@test
def parametric_impl_test() -> void:
    can Abort.Panic:
        if build[BoxTag[i64]](7) != 7i64:
            panic("parametric impl dispatch wrong")
        if build[BoxTag[bool]](9) != 9i64:
            panic("second instantiation of the blanket impl wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write parametric impl fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected parametric impl test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] parametric_impl_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected parametric impl output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// A parametric impl method whose BODY references the impl type param T (`def wrap(value: T)
// -> T`) is monomorphized per concrete receiver at the call site rather than emitted
// standalone with an unbound T. This is the form the Store impls need (`store_get -> Elem&`
// with `Elem = T`).
func TestRunCLIParametricImplMonomorphizesTReferencingMethod(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "parametric_impl_tref_fixture.elisa")
	src := `protocol Holder:
    type Item
    def wrap(value: Item) -> Item

struct BoxTag[T]:
    tag: T

impl[T] Holder for BoxTag[T]:
    type Item = T
    def wrap(value: T) -> T:
        return value

def use_holder[H: Holder](value: H.Item) -> H.Item:
    return H.wrap(value)

@test
def holder_test() -> void:
    can Abort.Panic:
        if use_holder[BoxTag[i64]](42i64) != 42i64:
            panic("T-referencing parametric method (i64) wrong")
        if use_holder[BoxTag[u8]](5u8) != 5u8:
            panic("T-referencing parametric method (u8) wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write T-referencing fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected T-referencing parametric method test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] holder_test") {
		t.Fatalf("expected holder_test to pass, got:\n%s", stdout.String())
	}
}
