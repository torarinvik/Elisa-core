package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A concrete receiver can implement multiple protocols with __cast__ methods that
// return different types. The postfix target must select the matching impl body.
func TestRunCLIConcreteProtocolCastSelectsByTarget(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "concrete_protocol_cast_target.elisa")
	src := `protocol ToText:
    def __cast__(self: Self) -> cstr

protocol ToNumber:
    def __cast__(self: Self) -> i64

struct Box[T]:
    marker: i64

impl[T] ToText for Box[T]:
    def __cast__(self: Box[T]) -> cstr:
        return "text result"

impl[T] ToNumber for Box[T]:
    def __cast__(self: Box[T]) -> i64:
        return 42

@test
def concrete_protocol_cast_target() -> void:
    can Abort.Panic:
        box: Box[i32] = Box[i32]{marker: 0}
        text: cstr = box.cstr()
        number: i64 = box.i64()
        if text != "text result":
            panic("cstr target selected the wrong impl")
        if number != 42:
            panic("i64 target selected the wrong impl")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write concrete protocol cast fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected concrete protocol cast test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] concrete_protocol_cast_target") {
		t.Fatalf("expected concrete protocol cast test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
