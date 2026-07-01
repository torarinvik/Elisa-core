package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunCLIEmitsCBindLayoutCheck(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	source := `@c_bind("` + headerPath + `", "struct Header", prefix)
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

func TestRunCLIEmitsCBindLayoutManifestJSON(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("cc not available")
	}
	tmpDir := t.TempDir()
	headerPath := filepath.Join(tmpDir, "fixture.h")
	if err := os.WriteFile(headerPath, []byte(`#include <stdint.h>

struct Header {
	uint8_t tag;
	uint32_t count;
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
	targetTriple := runtime.GOARCH + "-unknown-" + runtime.GOOS
	if runtime.GOOS == "darwin" {
		targetTriple = "x86_64-apple-darwin"
	}
	exitCode := runCLI([]string{"-emit", "c-bind-check-json", "-target-triple", targetTriple, sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	var manifest struct {
		Version      int    `json:"version"`
		TargetTriple string `json:"target_triple"`
		Structs      []struct {
			ElisaName string `json:"elisa_name"`
			CName     string `json:"c_name"`
			Elisa     struct {
				Size int `json:"size"`
			} `json:"elisa"`
			C struct {
				Size int `json:"size"`
			} `json:"c"`
			Fields []struct {
				Name        string `json:"name"`
				ElisaOffset int    `json:"elisa_offset"`
				COffset     int    `json:"c_offset"`
			} `json:"fields"`
		} `json:"structs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("expected JSON manifest, got error %v\n%s", err, stdout.String())
	}
	if manifest.Version != 1 || manifest.TargetTriple != targetTriple {
		t.Fatalf("unexpected manifest header: %#v", manifest)
	}
	if len(manifest.Structs) != 1 || manifest.Structs[0].ElisaName != "Header" || manifest.Structs[0].CName != "struct Header" {
		t.Fatalf("unexpected structs: %#v", manifest.Structs)
	}
	if manifest.Structs[0].Elisa.Size != manifest.Structs[0].C.Size {
		t.Fatalf("expected Elisa/C sizes to match: %#v", manifest.Structs[0])
	}
	if len(manifest.Structs[0].Fields) != 2 || manifest.Structs[0].Fields[1].Name != "count" || manifest.Structs[0].Fields[1].ElisaOffset != manifest.Structs[0].Fields[1].COffset {
		t.Fatalf("unexpected fields: %#v", manifest.Structs[0].Fields)
	}
}

func TestRunCLIEmitsCBindLayoutCheckForTargetTriple(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("cc not available")
	}
	targetTriple := runtime.GOARCH + "-unknown-" + runtime.GOOS
	if runtime.GOOS == "darwin" {
		targetTriple = "x86_64-apple-darwin"
	}
	tmpDir := t.TempDir()
	headerPath := filepath.Join(tmpDir, "fixture.h")
	if err := os.WriteFile(headerPath, []byte(`#include <stddef.h>
#include <stdint.h>

struct Header {
	void *ptr;
	uint32_t count;
};
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(tmpDir, "fixture.elisa")
	source := `@c_bind("` + headerPath + `", "struct Header")
struct Header layout c:
	ptr: void&?
	count: u32
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "c-bind-check", "-target-triple", targetTriple, sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "c-bind-check: Header matches struct Header") {
		t.Fatalf("expected target-aware c-bind success, got:\n%s", stdout.String())
	}
}

func TestCBindShellFieldsPreserveQuotedIncludePaths(t *testing.T) {
	t.Parallel()
	got := shellFields(`-I"/tmp/path with spaces/include" -DNAME=value -iquote '/tmp/quoted path' escaped\\ value`)
	want := []string{
		"-I/tmp/path with spaces/include",
		"-DNAME=value",
		"-iquote",
		"/tmp/quoted path",
		"escaped\\",
		"value",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("field %d: expected %q, got %q (all=%#v)", i, want[i], got[i], got)
		}
	}
}
