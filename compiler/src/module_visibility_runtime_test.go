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
	t.Parallel()
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
        p: Geo::Point = Geo::Point{x: 3, y: 4}
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

// A `const module` takes `public:` / `private:` sections like any other module block.
// Without them every member was private to the const module itself, so even the PARENT
// module could not read one -- `canAccessPrivateName` grants the owner namespace and its
// descendants, never its ancestors -- and a `private:` grouping of shared constants was
// unusable.
func TestRunCLIConstModuleVisibilitySections(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "const_module_visibility_fixture.elisa")
	src := `module Geo:
    private:
        const module Scalar:
            public:
                ZERO: i64 = 0
                ONE: i64 = 1
            private:
                HIDDEN: i64 = 9

    public:
        def zero() -> i64:
            return Scalar::ZERO

        def one() -> i64:
            return Scalar::ONE

@test
def const_module_visibility_test() -> void:
    can Abort.Panic:
        if Geo::zero() != 0:
            panic("public const-module member unreadable from the parent module")
        if Geo::one() != 1:
            panic("second public const-module member wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write const module visibility fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected const module visibility runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] const_module_visibility_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// The `private:` section of a const module still hides its members from outside, and the
// diagnostic names the const module as the owner.
func TestRunCLIConstModulePrivateMemberDiagnostic(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "const_module_private_diag_fixture.elisa")
	src := `module Geo:
    const module Scalar:
        public:
            ZERO: i64 = 0
        private:
            HIDDEN: i64 = 9

def main() -> i64:
    return Geo::Scalar::HIDDEN
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected private const-module access to fail, stdout:\n%s", stdout.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "\"Geo.Scalar.HIDDEN\" is private to module \"Geo.Scalar\"") {
		t.Fatalf("expected privacy diagnostic, got:\n%s", combined)
	}
}

// A `private module` hides the module itself from everything outside its parent. Its
// members keep their own visibility, so a public one is reachable exactly as far as the
// module is -- and the diagnostic names the MODULE, which is the boundary to get past.
func TestRunCLIPrivateModuleMemberDiagnostic(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "module_private_diag_fixture.elisa")
	src := `module Host:
    private module Vault:
        public:
            def check() -> i64:
                return KEY
        const KEY: i64 = 41

    def inside() -> i64:
        return Vault::check() + Vault::KEY

def main() -> i64:
    return Host::Vault::KEY
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
	if !strings.Contains(combined, "\"Host.Vault\" is private to module \"Host\"") {
		t.Fatalf("expected privacy diagnostic, got:\n%s", combined)
	}
	if strings.Contains(combined, "Host.Vault.KEY\" is private") {
		t.Fatalf("expected the parent to read into its own private module, got:\n%s", combined)
	}
}

// Visibility is RELATIVE (docs/128): a `private:` section marks the nested module it
// wraps, not that module's members, so the parent can read its own private const module.
// Before this rule the section reached inside and made every constant private to the
// const module itself -- unreadable even by the module that declared it.
func TestRunCLIPrivateConstModuleReadableByParent(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "private_const_module_parent.elisa")
	src := `module MazeGame:
    private const module Tune:
        START_LIVES: i64 = 3
        FOG_RADIUS: i64 = 4

    def lives() -> i64:
        return Tune::START_LIVES + Tune::FOG_RADIUS

def main() -> i64:
    return MazeGame::lives()
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected the parent to read its private const module, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	outsidePath := filepath.Join(fixtureDir, "private_const_module_outside.elisa")
	outside := `module MazeGame:
    private const module Tune:
        START_LIVES: i64 = 3

def main() -> i64:
    return MazeGame::Tune::START_LIVES
`
	if err := os.WriteFile(outsidePath, []byte(outside), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if exitCode := runCLI([]string{"-emit", "semantic", outsidePath}, &stdout, &stderr); exitCode == 0 {
		t.Fatalf("expected a private const module to stay closed from outside, stdout:\n%s", stdout.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "\"MazeGame.Tune\" is private to module \"MazeGame\"") {
		t.Fatalf("expected the diagnostic to name the private module, got:\n%s", combined)
	}
}
