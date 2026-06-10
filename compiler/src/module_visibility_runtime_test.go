package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end native coverage for the module-system smoothing work:
// qualified construction (`Geo::Point(3, 4)` and `Geo::Point{x: …}`),
// namespaced enum constructors + match arms under `using`, and the
// public-section re-export inside a `private module`.
func TestRunCLIModuleQualifiedConstructionAndVisibility(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "module_visibility_fixture.elisa")
	src := `module Geo:
    struct Point:
        x: i64
        y: i64
    def dist2(p: Point) -> i64:
        return p.x * p.x + p.y * p.y

module Shapes:
    enum Form:
        Circle(r: i64)
        Square(s: i64)

private module Vault:
    public:
        def check() -> i64:
            return KEY + 1
    const KEY: i64 = 41

using Shapes

def metric(f: Form) -> i64:
    match f:
        Form.Circle(r: r):
            return r + 100
        Form.Square(s: q):
            return q + 200

@test
def module_visibility_runtime_test() -> void:
    can Abort.Panic:
        p: Geo::Point = Geo::Point(3, 4)
        if Geo::dist2(p) != 25:
            panic("qualified positional construction wrong")
        q: Geo::Point = Geo::Point{x: 6, y: 8}
        if Geo::dist2(q) != 100:
            panic("qualified brace construction wrong")
        c: Form = Shapes::Form.Circle(r: 2)
        if metric(c) != 102:
            panic("qualified enum constructor wrong")
        s: Form = Form.Square(s: 3)
        if metric(s) != 203:
            panic("using-resolved enum constructor or match arm wrong")
        if Vault::check() != 42:
            panic("public section inside private module not re-exported")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write module visibility fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected module visibility runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] module_visibility_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// The private member of a private module stays inaccessible from outside even
// when a sibling public section exists, and the diagnostic names the privacy.
func TestRunCLIPrivateModuleMemberDiagnostic(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "module_private_diag_fixture.elisa")
	src := `private module Vault:
    public:
        def check() -> i64:
            return KEY
    const KEY: i64 = 41

def main() -> i64:
    return Vault::KEY
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected private access to fail, stdout:\n%s", stdout.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "\"Vault.KEY\" is private to module \"Vault\"") {
		t.Fatalf("expected privacy diagnostic, got:\n%s", combined)
	}
}
