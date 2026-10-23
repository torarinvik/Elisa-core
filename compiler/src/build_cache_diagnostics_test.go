package main

import (
	"bytes"
	"elisacore/src/semantic"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildObjectCacheReplaysDiagnostics(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", "0")
	source := filepath.Join(dir, "warning.elisa")
	if err := os.WriteFile(source, []byte("@deprecated(\"cache-warning-marker\")\ndef old() -> i64:\n    return 0\n\ndef main() -> i64:\n    return old()\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	for index, stderr := range []*bytes.Buffer{&first, &second} {
		var stdout bytes.Buffer
		if rc := runCLI([]string{"-emit", "obj", "-o", filepath.Join(dir, "result.o"), source}, &stdout, stderr); rc != 0 {
			t.Fatalf("build%d: rc=%d stderr=%s", index, rc, stderr)
		}
	}
	if !strings.Contains(first.String(), "cache-warning-marker") {
		t.Fatalf("fixture did not warn: %s", first.String())
	}
	if first.String() != second.String() {
		t.Fatalf("cache changed diagnostics: cold=%q warm=%q", first.String(), second.String())
	}
	t.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", "1")
	var suppressed, output bytes.Buffer
	if rc := runCLI([]string{"-emit", "obj", "-o", filepath.Join(dir, "result.o"), source}, &output, &suppressed); rc != 0 || strings.Contains(suppressed.String(), "cache-warning-marker") {
		t.Fatalf("changed warning policy reused prior transcript: rc=%d stderr=%s", rc, &suppressed)
	}

}

func TestBuildCacheTranscriptIntegrityAndCaptureBound(t *testing.T) {
	dir := t.TempDir()
	built := filepath.Join(dir, "built.o")
	if err := os.WriteFile(built, []byte("object"), 0600); err != nil {
		t.Fatal(err)
	}
	artifact := buildCacheArtifact{key: "test-key", dir: filepath.Join(dir, "entry"), object: filepath.Join(dir, "entry", "out.o")}
	want := buildCacheTranscript{Stdout: []byte{0xff, 0, 1}, Stderr: []byte("warning\n")}
	if err := publishCachedBuildObject(artifact, built, want); err != nil {
		t.Fatal(err)
	}
	got, valid := readBuildCacheTranscript(artifact)
	if !valid || !bytes.Equal(got.Stdout, want.Stdout) || !bytes.Equal(got.Stderr, want.Stderr) {
		t.Fatal("transcript did not preserve exact bytes")
	}
	if err := os.Remove(artifact.object); err != nil {
		t.Fatal(err)
	}
	if err := publishCachedBuildObject(artifact, built, want); err != nil {
		t.Fatal(err)
	}
	if !usableCachedFile(artifact.object, false) {
		t.Fatal("valid transcript with missing object was not repaired")
	}
	got.Stderr = []byte("corrupted")
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifact.dir, "transcript.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, valid := readBuildCacheTranscript(artifact); valid {
		t.Fatal("corrupted transcript admitted")
	}
	if err := publishCachedBuildObject(artifact, built, want); err != nil {
		t.Fatal(err)
	}
	if _, valid := readBuildCacheTranscript(artifact); !valid {
		t.Fatal("corrupt entry was not repaired")
	}
	if err := os.Remove(filepath.Join(artifact.dir, "transcript.json")); err != nil {
		t.Fatal(err)
	}
	if _, valid := readBuildCacheTranscript(artifact); valid {
		t.Fatal("missing transcript admitted")
	}
	var capture buildCacheCapture
	chunk := bytes.Repeat([]byte{'x'}, buildCacheStreamLimit)
	if n, err := capture.Write(chunk); err != nil || n != len(chunk) {
		t.Fatal("capture rejected limit-sized write")
	}
	if n, err := capture.Write([]byte("overflow")); err != nil || n != 8 || !capture.overflow || capture.Len() != buildCacheStreamLimit {
		t.Fatal("capture overflow was not bounded and transparent")
	}
}

func TestBuildObjectCacheDeclinesRequestObservers(t *testing.T) {
	for _, name := range []string{"ELISACORE_BUILD_TIMING", "ELISACORE_CPUPROFILE", "ELISACORE_MEMPROFILE", "ELISACORE_EMIT_LL", "ELISA_SEMANTIC_INTERNAL_PARITY_OUT", "ELISA_BACKEND_PARITY_OUT"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "observer")
			if _, handled := tryBuildObjectCacheWith(cliOptions{emit: emitObject}, nil, nil, nil, nil); handled {
				t.Fatal("request observer satisfied by replay")
			}
		})
	}
}

func TestBuildObjectCachePathCreationRejectsPriorSuccess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	f, err := os.CreateTemp(".", "disk-diagnostic-path-*.elisa")
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(f.Name())
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(name) })
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.elisa")
	text := "#line 1 " + name + "\ndef build(owner: StringView) -> void:\n    pass\n\ndef main() -> i64:\n    return 0\n"
	if err := os.WriteFile(source, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}

	// Exercise raw loaded snapshots through the cache seam; the CLI include
	// expander canonicalizes source attribution before this API receives it.
	options := cliOptions{emit: emitObject, filename: source, output: filepath.Join(dir, "result.o")}
	load := func(string, io.Writer) (*loadedProgram, bool) {
		return &loadedProgram{filename: source, source: []byte(text)}, true
	}
	build := func(opts cliOptions, program *loadedProgram, _ io.Writer, stderr io.Writer) int {
		if _, _, ok := analyzeLoadedProgramWithOptions(program, stderr, semantic.AnalyzeOptions{}); !ok {
			return 1
		}
		if err := os.WriteFile(opts.output, []byte("analyzed object placeholder"), 0600); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	run := func() (int, string) {
		var out, stderr bytes.Buffer
		rc, handled := tryBuildObjectCacheWith(options, &out, &stderr, load, build)
		if !handled {
			t.Fatal("cache declined raw snapshot")
		}
		return rc, stderr.String()
	}

	if rc, stderr := run(); rc != 0 {
		t.Fatalf("initial build: rc=%d %s", rc, stderr)
	}
	if err := os.WriteFile(name, []byte("exists"), 0600); err != nil {
		t.Fatal(err)
	}
	if rc, stderr := run(); rc == 0 || !strings.Contains(stderr, "internal runtime carrier") {
		t.Fatalf("stale cached success after path creation: rc=%d stderr=%s", rc, stderr)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if rc, stderr := run(); rc != 0 {
		t.Fatalf("removed path failed recovery: rc=%d %s", rc, stderr)
	}
}

type buildCacheShortWriter struct{}

func (buildCacheShortWriter) Write([]byte) (int, error) { return 0, nil }

func TestBuildObjectCacheReplayIOFailureDoesNotRebuild(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	program := &loadedProgram{filename: filepath.Join(dir, "unit.elisa"), source: []byte("def main() -> i64:\n    return 0\n")}
	options := cliOptions{filename: program.filename, emit: emitObject, output: filepath.Join(dir, "result.o")}
	artifact, ok := buildCacheObjectArtifactForProgram(options, program)
	if !ok {
		t.Fatal("fixture cache identity unavailable")
	}
	built := filepath.Join(dir, "built.o")
	if err := os.WriteFile(built, []byte("object"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishCachedBuildObject(artifact, built, buildCacheTranscript{Stdout: []byte("stdout"), Stderr: []byte("stderr")}); err != nil {
		t.Fatal(err)
	}
	load := func(string, io.Writer) (*loadedProgram, bool) { return program, true }
	build := func(cliOptions, *loadedProgram, io.Writer, io.Writer) int {
		t.Fatal("replay I/O failure fell through into compilation")
		return 0
	}
	for _, stderrFails := range []bool{false, true} {
		var stdout, stderr io.Writer = io.Discard, io.Discard
		if stderrFails {
			stderr = buildCacheShortWriter{}
		} else {
			stdout = buildCacheShortWriter{}
		}
		if rc, handled := tryBuildObjectCacheWith(options, stdout, stderr, load, build); !handled || rc == 0 {
			t.Fatalf("short replay reported success: rc=%d handled=%v", rc, handled)
		}
	}
}

func TestBuildObjectCacheReplaysLargeDiagnosticTranscript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	program := &loadedProgram{filename: filepath.Join(dir, "unit.elisa"), source: []byte("def main() -> i64:\n    return 0\n")}
	options := cliOptions{filename: program.filename, emit: emitObject, output: filepath.Join(dir, "result.o")}
	load := func(string, io.Writer) (*loadedProgram, bool) { return program, true }
	// Match the real compiler's warning volume, including non-text bytes. Exercise
	// both streams near the supported bound so JSON's base64 overhead is covered.
	payload := bytes.Repeat([]byte{0xff, 0, 'x', '\n'}, (buildCacheStreamLimit-16)/4)
	builds := 0
	build := func(opts cliOptions, _ *loadedProgram, stdout, stderr io.Writer) int {
		builds++
		if _, err := stdout.Write(payload); err != nil {
			t.Fatal(err)
		}
		if _, err := stderr.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(opts.output, []byte("object"), 0600); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	for i := 0; i < 2; i++ {
		var stdout, stderr bytes.Buffer
		if rc, handled := tryBuildObjectCacheWith(options, &stdout, &stderr, load, build); !handled || rc != 0 || !bytes.Equal(stdout.Bytes(), payload) || !bytes.Equal(stderr.Bytes(), payload) {
			t.Fatal("large transcript changed output")
		}
	}
	if builds != 1 {
		t.Fatalf("large transcript missed cache: %d builds", builds)
	}
}

func TestBuildObjectCacheOversizedTranscriptNeverPublishes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	program := &loadedProgram{filename: filepath.Join(dir, "unit.elisa"), source: []byte("def main() -> i64:\n    return 0\n")}
	options := cliOptions{filename: program.filename, emit: emitObject, output: filepath.Join(dir, "result.o")}
	load := func(string, io.Writer) (*loadedProgram, bool) { return program, true }
	payload := strings.Repeat("x", buildCacheStreamLimit+1)
	builds := 0
	build := func(opts cliOptions, _ *loadedProgram, _ io.Writer, stderr io.Writer) int {
		builds++
		if _, err := io.WriteString(stderr, payload); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(opts.output, []byte("object"), 0600); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	for i := 0; i < 2; i++ {
		var stderr bytes.Buffer
		if rc, handled := tryBuildObjectCacheWith(options, io.Discard, &stderr, load, build); !handled || rc != 0 || stderr.String() != payload {
			t.Fatal("oversized diagnostics changed compiler output")
		}
	}
	if builds != 2 {
		t.Fatal("oversized transcript became a cache hit")
	}
	artifact, ok := buildCacheObjectArtifactForProgram(options, program)
	if !ok {
		t.Fatal("fixture key unavailable")
	}
	if _, err := os.Stat(artifact.object); !os.IsNotExist(err) {
		t.Fatal("oversized transcript published an artifact", err)
	}
}

func TestBuildObjectCacheRepairsCorruptedObject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	program := &loadedProgram{filename: filepath.Join(dir, "unit.elisa"), source: []byte("def main() -> i64:\n    return 0\n")}
	options := cliOptions{filename: program.filename, emit: emitObject, output: filepath.Join(dir, "result.o")}
	load := func(string, io.Writer) (*loadedProgram, bool) { return program, true }
	builds := 0
	want := []byte("valid object bytes")
	build := func(opts cliOptions, _ *loadedProgram, _ io.Writer, stderr io.Writer) int {
		builds++
		if _, err := io.WriteString(stderr, "diagnostic\n"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(opts.output, want, 0600); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	run := func() {
		t.Helper()
		var stderr bytes.Buffer
		if rc, handled := tryBuildObjectCacheWith(options, io.Discard, &stderr, load, build); !handled || rc != 0 || stderr.String() != "diagnostic\n" {
			t.Fatalf("cache flow: rc=%d handled=%v stderr=%q", rc, handled, stderr.String())
		}
		got, err := os.ReadFile(options.output)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("damaged cache object reached output: got=%q err=%v", got, err)
		}
	}
	run()
	artifact, ok := buildCacheObjectArtifactForProgram(options, program)
	if !ok {
		t.Fatal("fixture key unavailable")
	}
	if err := os.WriteFile(artifact.object, []byte("damaged but nonempty"), 0600); err != nil {
		t.Fatal(err)
	}
	transcript, valid := readBuildCacheTranscript(artifact)
	if !valid {
		t.Fatal("fixture metadata unavailable")
	}
	if err := restoreBuildCacheObject(artifact, options.output, transcript); err == nil {
		t.Fatal("corrupt object accepted by direct restore")
	}
	unchanged, err := os.ReadFile(options.output)
	if err != nil || !bytes.Equal(unchanged, want) {
		t.Fatal("failed restore replaced prior output")
	}
	if leftovers, err := filepath.Glob(filepath.Join(dir, ".elisa-cache-restore-*")); err != nil || len(leftovers) != 0 {
		t.Fatal("failed restore leaked staged output", leftovers, err)
	}
	run()
	if builds != 2 {
		t.Fatal("damaged object did not rebuild")
	}
	run()
	if builds != 2 {
		t.Fatal("repaired entry did not hit")
	}
}
