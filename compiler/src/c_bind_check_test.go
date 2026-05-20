package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIEmitsCBindLayoutCheck(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("cc not available")
	}
	tmpDir := t.TempDir()
	headerPath := filepath.Join(tmpDir, "fixture.h")
	if err := os.WriteFile(headerPath, []byte(`#include <stddef.h>
#include <stdint.h>

struct Header {
	uint8_t tag;
	uint32_t count;
	size_t total;
};
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(tmpDir, "fixture.elisa")
	source := `@c_bind("` + headerPath + `", "struct Header")
struct Header layout c:
	tag: u8
	count: u32
	total: usize
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "c-bind-check", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "c-bind-check: Header matches struct Header") {
		t.Fatalf("expected c-bind success, got:\n%s", stdout.String())
	}
}

func TestRunCLIEmitsCBindLayoutMismatch(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("cc not available")
	}
	tmpDir := t.TempDir()
	headerPath := filepath.Join(tmpDir, "fixture.h")
	if err := os.WriteFile(headerPath, []byte(`#include <stdint.h>

struct Header {
	uint32_t count;
	uint8_t tag;
};
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(tmpDir, "fixture.elisa")
	source := `@c_bind("` + headerPath + `", "struct Header")
struct Header layout c:
	tag: u8
	count: u32
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "c-bind-check", sourcePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected mismatch failure\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "offset mismatch") {
		t.Fatalf("expected offset mismatch diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIEmitsCBindPrefixLayoutCheck(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("cc not available")
	}
	tmpDir := t.TempDir()
	headerPath := filepath.Join(tmpDir, "fixture.h")
	if err := os.WriteFile(headerPath, []byte(`#include <stdint.h>

struct Header {
	void *owner;
	uint32_t count;
	uint32_t flags;
	uint64_t tail;
};
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(tmpDir, "fixture.elisa")
	source := `@c_bind_prefix("` + headerPath + `", "struct Header")
struct HeaderPrefix layout c:
	owner: void&?
	count: u32
	flags: u32
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "c-bind-check", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "c-bind-check: HeaderPrefix prefix matches struct Header") {
		t.Fatalf("expected c-bind prefix success, got:\n%s", stdout.String())
	}
}
