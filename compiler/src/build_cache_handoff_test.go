package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildObjectCachePrefixHandsOffSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	source := filepath.Join(dir, "main.elisa")
	before := []byte("def main() -> i64:\n    return 1\n")
	after := []byte("def main() -> i64:\n    return 2\n")
	if err := os.WriteFile(source, before, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-emit", "obj", "-o", filepath.Join(dir, "result.o"), source}
	loads := 0
	load := func(filename string, stderr io.Writer) (*loadedProgram, bool) {
		loads++
		return loadProgramInput(filename, stderr)
	}
	code, hit, preloaded := tryBuildObjectCacheHitUsing(args, io.Discard, io.Discard, load)
	if code != 0 || hit || preloaded == nil || loads != 1 {
		t.Fatalf("prefix miss: code=%d hit=%v snapshot=%v loads=%d", code, hit, preloaded != nil, loads)
	}
	want := append([]byte(nil), preloaded.source...)
	if err := os.WriteFile(source, after, 0600); err != nil {
		t.Fatal(err)
	}
	options, err := parseArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	builds := 0
	build := func(options cliOptions, program *loadedProgram, stdout, stderr io.Writer) int {
		builds++
		if program != preloaded || !bytes.Equal(program.source, want) {
			t.Fatal("local build lost the prefix lookup's source snapshot")
		}
		if err := os.WriteFile(options.output, []byte("captured-object"), 0600); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	if rc, handled := tryBuildObjectCacheWith(options, io.Discard, io.Discard, loadedProgramLoader(preloaded, load), build); rc != 0 || !handled || builds != 1 || loads != 1 {
		t.Fatalf("handoff: rc=%d handled=%v builds=%d loads=%d", rc, handled, builds, loads)
	}
	// The new on-disk revision cannot reuse an object indexed by the captured
	// older snapshot. Reverting must recover that older artifact before forwarding.
	if _, hit, current := tryBuildObjectCacheHitUsing(args, io.Discard, io.Discard, load); hit || current == nil || bytes.Equal(current.source, want) {
		t.Fatal("source changed after prefix lookup but reused the captured object's key")
	}
	if err := os.WriteFile(source, before, 0600); err != nil {
		t.Fatal(err)
	}
	if _, hit, current := tryBuildObjectCacheHitUsing(args, io.Discard, io.Discard, load); !hit || current != nil {
		t.Fatal("reverted source did not hit before forwarding, or retained an unused snapshot")
	}
}

func TestLoadedProgramHandoffRequiresMatchingFilename(t *testing.T) {
	preloaded := &loadedProgram{filename: "first.elisa", source: []byte("first")}
	other := &loadedProgram{filename: "second.elisa", source: []byte("second")}
	loads := 0
	load := loadedProgramLoader(preloaded, func(filename string, _ io.Writer) (*loadedProgram, bool) {
		loads++
		if filename != other.filename {
			t.Fatal("unexpected fallback path", filename)
		}
		return other, true
	})
	if program, ok := load(preloaded.filename, io.Discard); !ok || program != preloaded || loads != 0 {
		t.Fatal("matching source was reread")
	}
	if program, ok := load(other.filename, io.Discard); !ok || program != other || loads != 1 {
		t.Fatal("different filename reused the wrong snapshot")
	}
}

func TestBuildObjectCachePrefixObserverDoesNotPreload(t *testing.T) {
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_CPUPROFILE", "observer")
	load := func(string, io.Writer) (*loadedProgram, bool) {
		t.Fatal("observer request performed a prefix cache load")
		return nil, false
	}
	if _, hit, preloaded := tryBuildObjectCacheHitUsing([]string{"-emit", "obj", "main.elisa"}, io.Discard, io.Discard, load); hit || preloaded != nil {
		t.Fatal("observer request admitted a cache hit/snapshot")
	}
}

func TestBuildObjectCacheHandoffCompilesCapturedSource(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_RETAINED_SERVER", "")
	source := filepath.Join(dir, "library.elisa")
	before := "def answer() -> i64:\n    return 1\n\nexport fn answer() -> i64 = answer\n"
	if err := os.WriteFile(source, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(dir, "library.o")
	args := []string{"-emit", "obj", "-o", object, source}
	if _, hit, preloaded := tryBuildObjectCacheHitWithProgram(args, io.Discard, io.Discard); hit || preloaded == nil {
		t.Fatal("expected a prefix miss with a captured program")
	} else {
		if err := os.WriteFile(source, bytes.ReplaceAll([]byte(before), []byte("return 1"), []byte("return 2")), 0600); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		if rc := runCLIWithProgram(args, io.Discard, &stderr, preloaded); rc != 0 {
			t.Fatalf("captured build: rc=%d %s", rc, &stderr)
		}
	}
	client := filepath.Join(dir, "client.c")
	if err := os.WriteFile(client, []byte("extern long long answer(void);\nint main(void) { return answer() == 1 ? 0 : 91; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	executable := filepath.Join(dir, "client")
	if out, err := exec.CommandContext(ctx, "clang", client, object, "-o", executable).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil {
		t.Fatalf("compiled a different source revision: %v\n%s", err, out)
	}
}
