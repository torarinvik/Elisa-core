package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runLawProgramNoSMT compiles+runs a protocol-law program with SMT discharge DISABLED (-nosmt), so
// every law is auto-lowered to a @property fuzz harness (rather than statically proven). This exercises
// the fuzz path for a faithful impl: the harnesses must run and pass.
func runLawProgramNoSMT(t *testing.T, name, body string) (int, string, string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	path := filepath.Join(t.TempDir(), name+".elisa")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	exit := runCLI([]string{"-emit", "test", "-O0", "-nosmt", path}, &stdout, &stderr)
	return exit, stdout.String(), stderr.String()
}

// Protocol laws (docs/85 P3) that the analyzer cannot prove are auto-lowered to @property fuzz
// harnesses named `__law_<Protocol>_<law>_<Type>`. These runtime tests compile a program with a
// protocol carrying laws and assert the generated harnesses run — passing for a faithful impl and
// FAILING (counterexample) for a broken one, so a broken impl is never silently accepted.

const ordLawProgram = `
protocol Ord:
    def le(self: Self, other: Self) -> bool
    law total(a: Self, b: Self) = a.le(b) or b.le(a)
    law transitive(a: Self, b: Self, c: Self) = (a.le(b) and b.le(c)) implies a.le(c)
`

func TestProtocolLawHarnessPassesForFaithfulImpl(t *testing.T) {
	t.Parallel()
	body := ordLawProgram + `
struct Version:
    major: i64
    minor: i64

impl Ord for Version:
    def le(self: Version, other: Version) -> bool:
        return self.major < other.major or (self.major == other.major and self.minor <= other.minor)
`
	exit, stdout, stderr := runLawProgramNoSMT(t, "law_ok", body)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "failed=0") {
		t.Fatalf("expected failed=0 for faithful impl, got:\n%s", stdout)
	}
	// The generated law harnesses must be present and passing.
	for _, n := range []string{"__property___law_Ord_total_Version", "__property___law_Ord_transitive_Version"} {
		if !strings.Contains(stdout, "[       OK ] "+n) {
			t.Fatalf("expected law harness %q to run and pass, got:\n%s", n, stdout)
		}
	}
}

func TestProtocolLawHarnessFailsForBrokenImpl(t *testing.T) {
	t.Parallel()
	body := ordLawProgram + `
struct Bad:
    v: i64

impl Ord for Bad:
    def le(self: Bad, other: Bad) -> bool:
        return false
`
	_, stdout, stderr := runPropertyProgram(t, "law_broken", body)
	combined := stdout + stderr
	// le=false ⇒ total (a.le(b) or b.le(a)) is always false ⇒ the fuzz harness finds a counterexample.
	if !strings.Contains(stdout, "failed=") || strings.Contains(stdout, "failed=0") {
		t.Fatalf("expected the broken-impl law harness to FAIL, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(combined, "__law_Ord_total_Bad") {
		t.Fatalf("expected the counterexample report to name the broken total-law harness, got:\n%s", combined)
	}
}
