package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeCollectionsInterfaceMatchesImplementation(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	implPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "collections.elisa")
	ifacePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "collections.elisai")

	wantBytes, err := os.ReadFile(ifacePath)
	if err != nil {
		t.Fatalf("expected checked-in collections interface: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "iface", implPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected collections interface generation to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected warning-free interface generation, got:\n%s", stderr.String())
	}
	if got, want := stdout.String(), string(wantBytes); got != want {
		t.Fatalf("checked-in collections.elisai is stale; regenerate with `go run ./src -emit interface -o runtime/elisacore_std/collections.elisai runtime/elisacore_std/collections.elisa`")
	}

	source := string(wantBytes)
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

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "ast", ifacePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected checked-in collections interface to parse, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected checked-in collections interface to parse warning-free, got:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "semantic", ifacePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected checked-in collections interface to analyze, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected checked-in collections interface to analyze warning-free, got:\n%s", stderr.String())
	}
}
