package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestLLCObjectKeyKeepsUserTemporaryPathStrings(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "llc")
	if err := os.WriteFile(tool, []byte("test llc identity"), 0700); err != nil {
		t.Fatal(err)
	}
	ir := filepath.Join(dir, "program.ll")
	key := func(value string) string {
		t.Helper()
		source := fmt.Sprintf("@user_data = constant [%d x i8] c\"%s\\00\"\n", len(value)+1, value)
		if err := os.WriteFile(ir, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		key, ok := llcObjectCacheKey(ir, "-O0", "", tool)
		if !ok {
			t.Fatal("cache key unavailable")
		}
		return key
	}
	for _, kind := range []string{"test", "native"} {
		first := key("elisacore-" + kind + "-run-111")
		second := key("elisacore-" + kind + "-run-222")
		if first == second {
			t.Fatal("different user data normalized into one object key")
		}
		if second != key("elisacore-"+kind+"-run-222") {
			t.Fatal("exact IR key not deterministic")
		}
	}
}

func TestBuildObjectCacheCompilesTheKeyedSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	filename := filepath.Join(dir, "program.elisa")
	a := []byte("def work() -> i64:\n    return 41\n")
	b := []byte("def work() -> i64:\n    return 42\n")
	if err := os.WriteFile(filename, a, 0600); err != nil {
		t.Fatal(err)
	}
	expectedA, err := readSourceWithIncludes(filename, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	options := cliOptions{filename: filename, emit: emitObject, output: filepath.Join(dir, "result.o")}
	loads, builds := 0, 0
	var snapshot *loadedProgram
	load := func(path string, stderr io.Writer) (*loadedProgram, bool) {
		loads++
		program, ok := loadProgramInput(path, stderr)
		if !ok {
			return nil, false
		}
		snapshot = program
		// A save after loading must not affect this build's identity or compiler input.
		if err := os.WriteFile(filename, b, 0600); err != nil {
			t.Fatal(err)
		}
		return program, true
	}
	build := func(opts cliOptions, program *loadedProgram, stdout, stderr io.Writer) int {
		builds++
		if program != snapshot || !bytes.Equal(program.source, expectedA) {
			t.Fatal("compiler received a second source revision")
		}
		if err := os.WriteFile(opts.output, []byte("compiled snapshot A"), 0600); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	var stderr bytes.Buffer
	rc, handled := tryBuildObjectCacheWith(options, io.Discard, &stderr, load, build)
	if !handled || rc != 0 || loads != 1 || builds != 1 {
		t.Fatalf("unexpected cache flow: %d %v loads=%d builds=%d %s", rc, handled, loads, builds, stderr.String())
	}
	artifactA, ok := buildCacheObjectArtifactForProgram(options, snapshot)
	if !ok {
		t.Fatal("snapshot key unavailable")
	}
	artifactB, ok := buildCacheObjectArtifactFor(options)
	if !ok {
		t.Fatal("changed file key unavailable")
	}
	if artifactA.key == artifactB.key {
		t.Fatal("different source revisions shared a key")
	}
	stored, err := os.ReadFile(artifactA.object)
	if err != nil || string(stored) != "compiled snapshot A" {
		t.Fatal("snapshot artifact not published", err)
	}
	// Restoring A must serve the artifact built from A, even though B was on disk
	// by the time the first cache build derived its key and invoked codegen.
	if err := os.WriteFile(filename, a, 0600); err != nil {
		t.Fatal(err)
	}
	rc, handled = tryBuildObjectCacheWith(options, io.Discard, &stderr, loadProgramInput, func(cliOptions, *loadedProgram, io.Writer, io.Writer) int {
		t.Fatal("restored snapshot missed its own artifact")
		return 1
	})
	if rc != 0 || !handled {
		t.Fatal("restored snapshot did not hit", stderr.String())
	}
}

func TestBuildObjectSnapshotKeysSourceAuthorityAndDeclinesBareIR(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	options := cliOptions{filename: filepath.Join(dir, "program.elisa"), emit: emitObject}
	first := &loadedProgram{filename: options.filename, source: []byte("same expanded bytes"), sourceFiles: []string{"/trusted/a", "/user/b"}}
	other := *first
	other.sourceFiles = []string{"/user/b", "/trusted/a"}
	a, ok := buildCacheObjectArtifactForProgram(options, first)
	if !ok {
		t.Fatal("key unavailable")
	}
	b, ok := buildCacheObjectArtifactForProgram(options, &other)
	if !ok || a.key != b.key {
		t.Fatal("authority set order changed key")
	}
	other.sourceFiles = []string{"/untrusted/a", "/user/b"}
	c, ok := buildCacheObjectArtifactForProgram(options, &other)
	if !ok || c.key == a.key {
		t.Fatal("source authority omitted from key")
	}
	first.file = &ast.File{Filename: strings.TrimSuffix(options.filename, ".elisa") + ".elisair"}
	if _, ok := buildCacheObjectArtifactForProgram(options, first); ok {
		t.Fatal("AST input keyed only by resolved text")
	}
}

func TestBuildObjectCacheNeverPublishesReloadUnderEarlierKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	filename := filepath.Join(dir, "program.elisa")
	options := cliOptions{filename: filename, emit: emitObject, output: filepath.Join(dir, "out.o")}
	a := []byte("def work() -> i64:\n    return 41\n")
	b := []byte("def work() -> i64:\n    return 42\n")
	if err := os.WriteFile(filename, a, 0600); err != nil {
		t.Fatal(err)
	}
	earlier, ok := buildCacheObjectArtifactFor(options)
	if !ok {
		t.Fatal("A key unavailable")
	}
	loads := 0
	load := func(path string, stderr io.Writer) (*loadedProgram, bool) {
		loads++
		// This is the original race: an earlier independent key read would see A,
		// while the subsequent load/codegen observes B.
		if err := os.WriteFile(filename, b, 0600); err != nil {
			t.Fatal(err)
		}
		return loadProgramInput(path, stderr)
	}
	build := func(opts cliOptions, program *loadedProgram, _, _ io.Writer) int {
		if !bytes.Contains(program.source, []byte("return 42")) {
			t.Fatal("fixture did not load B")
		}
		if err := os.WriteFile(opts.output, []byte("compiled B"), 0600); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	rc, handled := tryBuildObjectCacheWith(options, io.Discard, io.Discard, load, build)
	if rc != 0 || !handled || loads != 1 {
		t.Fatal("build did not complete")
	}
	if _, err := os.Stat(earlier.object); !os.IsNotExist(err) {
		t.Fatal("B was published under A's earlier key", err)
	}
	if err := os.WriteFile(filename, a, 0600); err != nil {
		t.Fatal(err)
	}
	rebuilt := false
	rc, handled = tryBuildObjectCacheWith(options, io.Discard, io.Discard, loadProgramInput, func(opts cliOptions, program *loadedProgram, _, _ io.Writer) int {
		rebuilt = true
		if !bytes.Contains(program.source, []byte("return 41")) {
			t.Fatal("A was not restored")
		}
		if err := os.WriteFile(opts.output, []byte("compiled A"), 0600); err != nil {
			t.Fatal(err)
		}
		return 0
	})
	if rc != 0 || !handled || !rebuilt {
		t.Fatal("A incorrectly reused B's artifact")
	}
}
