package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A protocol method may share a name with a BUILTIN collection method.
//
// The builtin chain in analyzeCallExpr keys on the FIELD NAME alone -- push, clear,
// reserve, rows, truncate, valid, and the dict/set insert rewrite -- with no check that
// the receiver is a collection. So `S.clear(...)` on a protocol-bound type parameter was
// claimed by the builtin path, which then evaluated S as a VALUE and reported
// `undefined identifier "S"` for a name plainly in scope. callIsProtocolBoundMethod now
// lets protocol dispatch win before the chain runs.
//
// The fixture also pins the half that must NOT change: a real collection receiver keeps
// the builtin meaning inside the very function whose type parameter is bound to a
// protocol declaring the same names.
func TestRunCLIProtocolMethodNameShadowsBuiltinCollectionMethod(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "protocol_builtin_name_clash_fixture.elisa")
	src := `global mutable witness: int = 0

struct Painter:
    tag: int

protocol Surface:
    def clear(value: int) -> void
    def push(value: int) -> void
    def insert(value: int) -> void

impl Surface for Painter:
    def clear(value: int) -> void:
        witness <- witness + value

    def push(value: int) -> void:
        witness <- witness + value * 10

    def insert(value: int) -> void:
        witness <- witness + value * 100

def drive[S: Surface]() -> int:
    S.clear(7)
    S.push(3)
    S.insert(2)
    xs: mutable darray[int] = []
    xs.push(1)
    xs.push(2)
    xs.push(3)
    counted: int = xs.count.int()
    xs.clear()
    return counted + xs.count.int()

@test
def protocol_builtin_name_clash_test() -> void:
    can Abort.Panic:
        produced: int = drive[Painter]()
        if witness != 237:
            panic("protocol dispatch lost to a builtin collection method")
        if produced != 3:
            panic("a real collection receiver lost its builtin method")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected protocol/builtin name-clash test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] protocol_builtin_name_clash_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
