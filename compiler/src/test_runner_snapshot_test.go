package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/backend"
)

func snapshotTestKey(t *testing.T, program *loadedProgram) string {
	t.Helper()
	key, err := earlyTestCacheKeyForProgram(program, "", nil, nil, nil, backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestEarlyTestCacheUsesLoadedSnapshotInsteadOfDisk(t *testing.T) {
	t.Setenv("ELISACORE_TEST_CACHE", "1")
	path := filepath.Join(t.TempDir(), "snapshot.elisa")
	if err := os.WriteFile(path, []byte("@test\ndef original() -> void:\n    return\n"), 0600); err != nil {
		t.Fatal(err)
	}
	original, ok := loadProgramInput(path, io.Discard)
	if !ok {
		t.Fatal("load original")
	}
	if err := os.WriteFile(path, []byte("@test\ndef replacement() -> void:\n    return\n"), 0600); err != nil {
		t.Fatal(err)
	}
	replacement, ok := loadProgramInput(path, io.Discard)
	if !ok {
		t.Fatal("load replacement")
	}
	key := snapshotTestKey(t, replacement)
	// Use a harmless executable; a stale hit would execute this and report handled.
	executable := filepath.Join(t.TempDir(), "runner")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	publishEarlyTestCache(key, executable, []cachedTestCase{{Name: "replacement"}})
	meta, _ := earlyTestCacheMetaPath(key)
	t.Cleanup(func() { os.Remove(meta) })
	if _, hit := locateEarlyTestCache(key); !hit {
		t.Fatal("replacement cache fixture missing")
	}
	options := cliOptions{emit: emitTest, packedProfile: backend.DefaultPackedLoweringProfile()}
	if _, handled := tryEarlyCachedTestRun(original, options, io.Discard, io.Discard); handled {
		t.Fatal("loaded original hit replacement source cache")
	}
	// Generation must likewise retain the source selected for analysis.
	_, result, ok := analyzeLoadedProgram(original, io.Discard)
	if !ok {
		t.Fatal("analyze original")
	}
	runner, err := generateTestRunnerSource(original, result, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runner, "def original()") || strings.Contains(runner, "def replacement()") {
		t.Fatal("runner generation reread replacement source")
	}
}

func TestSelectedTestsUseSnapshotAfterSourceDeletion(t *testing.T) {
	testSelectedTestsSnapshotAfterDeletion(t, "@test\ndef snapshot_survives() -> void:\n    return\n")
}

func TestPropertyTestsPublishOriginalSnapshotIdentity(t *testing.T) {
	testSelectedTestsSnapshotAfterDeletion(t, "@property(1)\ndef snapshot_property(value: bool) -> bool:\n    return value == value\n")
}

func testSelectedTestsSnapshotAfterDeletion(t *testing.T, source string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang unavailable")
	}
	t.Setenv("ELISACORE_TEST_CACHE", "1")
	path := filepath.Join(t.TempDir(), "deleted.elisa")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	program, ok := loadProgramInput(path, io.Discard)
	if !ok {
		t.Fatal("load source")
	}
	before := append([]byte(nil), program.source...)
	var diagnostics bytes.Buffer
	_, result, ok := analyzeLoadedProgram(program, &diagnostics)
	if !ok {
		t.Fatalf("analysis: %s", diagnostics.String())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	rc := executeSelectedTests(program, result, "", nil, nil, backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", false, false, &output, &diagnostics)
	if rc != 0 {
		t.Fatalf("snapshot compilation failed: rc%d\n%s\n%s", rc, output.String(), diagnostics.String())
	}
	if !bytes.Equal(program.source, before) {
		t.Fatal("runner construction mutated loaded snapshot")
	}
	if !strings.Contains(output.String(), "passed=1") {
		t.Fatalf("test did not run: %s", output.String())
	}
	key := snapshotTestKey(t, program)
	meta, _ := earlyTestCacheMetaPath(key)
	t.Cleanup(func() { os.Remove(meta) })
	if _, hit := locateEarlyTestCache(key); !hit {
		t.Fatal("compiled snapshot was not published under original source identity")
	}
	output.Reset()
	options := cliOptions{emit: emitTest, packedProfile: backend.DefaultPackedLoweringProfile()}
	rc, handled := tryEarlyCachedTestRun(program, options, &output, &diagnostics)
	if !handled || rc != 0 || !strings.Contains(output.String(), "passed=1") {
		t.Fatalf("deleted source snapshot failed warm hit: handled%v rc%d %s %s", handled, rc, output.String(), diagnostics.String())
	}
}

func TestEarlyTestSnapshotIdentityIncludesAuthority(t *testing.T) {
	program := &loadedProgram{filename: "root.elisa", source: []byte("same source"), sourceFiles: []string{"b", "a"}}
	base := snapshotTestKey(t, program)
	program.sourceFiles = []string{"a", "b"}
	if snapshotTestKey(t, program) != base {
		t.Fatal("origin ordering changed identity")
	}
	program.sourceFiles = []string{"a", "c"}
	if snapshotTestKey(t, program) == base {
		t.Fatal("different source authority reused identity")
	}
	program.sourceFiles = []string{"a", "b"}
	program.filename = "other.elisa"
	if snapshotTestKey(t, program) == base {
		t.Fatal("different effective filename reused identity")
	}
}
