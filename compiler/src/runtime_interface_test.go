package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertRuntimeInterfaceFresh checks one checked-in `.elisai` against a fresh generation
// and confirms the checked-in file still parses and analyzes warning-free. Shared by every
// generated interface under runtime/elisacore_std, because a `.elisai` is CONSUMED (see
// program_input.go's interfaceExtension) -- a program that includes the interface instead of
// the implementation sees exactly what is checked in, so a stale one silently hides
// declarations. Nothing but this test notices: the stage1 parity suite passes 330/330 with a
// stale interface, and the drift check only compares the two vendored copies to each other.
func assertRuntimeInterfaceFresh(t *testing.T, stem string) string {
	t.Helper()
	repoRoot := repoRootFromMainTest(t)
	implPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", stem+".elisa")
	ifacePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", stem+".elisai")

	wantBytes, err := os.ReadFile(ifacePath)
	if err != nil {
		t.Fatalf("expected checked-in %s interface: %v", stem, err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "iface", implPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected %s interface generation to succeed, stderr:\n%s", stem, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected warning-free interface generation, got:\n%s", stderr.String())
	}
	if got, want := stdout.String(), string(wantBytes); got != want {
		t.Fatalf("checked-in %s.elisai is stale; regenerate with `go run ./src -emit interface -o runtime/elisacore_std/%s.elisai runtime/elisacore_std/%s.elisa`", stem, stem, stem)
	}

	for _, emit := range []string{"ast", "semantic"} {
		stdout.Reset()
		stderr.Reset()
		if code := runCLI([]string{"-emit", emit, ifacePath}, &stdout, &stderr); code != 0 {
			t.Fatalf("expected checked-in %s interface to %s, stderr:\n%s", stem, emit, stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected checked-in %s interface to %s warning-free, got:\n%s", stem, emit, stderr.String())
		}
	}
	return string(wantBytes)
}

// The debug referee's interface went stale unnoticed: its implementation grew the trace and
// fault machinery (ElisaTraceEntry, ElisaSigaction, the signal/sigaction externs) and the
// checked-in interface never followed, so anyone including it saw a referee with no tracing.
// Only collections had a freshness guard; now both do.
func TestRuntimeDebugRefereeInterfaceMatchesImplementation(t *testing.T) {
	t.Parallel()
	source := assertRuntimeInterfaceFresh(t, "debug_referee")
	for _, check := range []string{
		"struct ElisaTraceEntry:",
		"struct ElisaSigaction:",
		"extern elisa_trace_ring: ElisaTraceEntry[ELISA_TRACE_CAP]",
	} {
		if !strings.Contains(source, check) {
			t.Fatalf("expected debug referee interface to contain %q", check)
		}
	}
}

func TestRuntimeCollectionsInterfaceMatchesImplementation(t *testing.T) {
	t.Parallel()
	source := assertRuntimeInterfaceFresh(t, "collections")
	for _, check := range []string{
		"struct InlineVec[T, N: usize]:",
		"extern inlinevec: InlineVecNamespace",
		"extern inline_vec[T, N: usize](owner: mutable Arena&) -> InlineVec[T, N]",
		"extern push[T, N: usize](vec: mutable InlineVec[T, N]&, item: T)",
	} {
		if !strings.Contains(source, check) {
			t.Fatalf("expected collections interface to contain %q", check)
		}
	}

}
