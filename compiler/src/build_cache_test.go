package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// emitObjForCacheTest compiles src to out via the CLI and returns stderr.
func emitObjForCacheTest(t *testing.T, src, out string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	rc := runCLI([]string{"-emit", "obj", "-o", out, src}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("emit obj failed (rc=%d): %s", rc, stderr.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected object at %s: %v", out, err)
	}
	return stderr.String()
}

func TestBuildObjectCacheHitAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	t.Setenv("ELISAC_BUILD_CACHE_DIR", cacheDir)
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_TEST_CACHE_DEBUG", "1")

	src := filepath.Join(dir, "prog.elisa")
	if err := os.WriteFile(src, []byte("def add(a: i64, b: i64) -> i64:\n    return a + b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// First build: miss + store.
	out1 := filepath.Join(dir, "a.o")
	s1 := emitObjForCacheTest(t, src, out1)
	if !strings.Contains(s1, "build-object miss-store") {
		t.Fatalf("first build should miss-store; stderr:\n%s", s1)
	}

	// Second build of identical inputs: hit, byte-identical object.
	out2 := filepath.Join(dir, "b.o")
	s2 := emitObjForCacheTest(t, src, out2)
	if !strings.Contains(s2, "build-object hit") {
		t.Fatalf("second build should hit; stderr:\n%s", s2)
	}
	b1, _ := os.ReadFile(out1)
	b2, _ := os.ReadFile(out2)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("cached object differs from freshly built one")
	}

	// Source change: must invalidate (miss again).
	if err := os.WriteFile(src, []byte("def add(a: i64, b: i64) -> i64:\n    return a - b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out3 := filepath.Join(dir, "c.o")
	s3 := emitObjForCacheTest(t, src, out3)
	if !strings.Contains(s3, "build-object miss-store") {
		t.Fatalf("changed source should miss-store; stderr:\n%s", s3)
	}
}

func TestBuildObjectCacheDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "0")
	t.Setenv("ELISACORE_TEST_CACHE_DEBUG", "1")

	src := filepath.Join(dir, "prog.elisa")
	if err := os.WriteFile(src, []byte("def id(a: i64) -> i64:\n    return a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "a.o")
	s1 := emitObjForCacheTest(t, src, out)
	s2 := emitObjForCacheTest(t, src, out)
	if strings.Contains(s1+s2, "build-object") {
		t.Fatalf("cache must be inert when disabled; stderr:\n%s\n%s", s1, s2)
	}
}

// Every codegen flag that reaches the backend through the environment must change the
// `-emit obj` cache key; otherwise e.g. a -fstrict-fp build is served a relaxed-FP object.
func TestBuildObjectCacheKeysEveryCodegenFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_TEST_CACHE_DEBUG", "1")
	flagEnvs := []string{"ELISACORE_FAST_MATH", "ELISACORE_STRICT_FP", "ELISACORE_NOALIAS_MUTABLE_REFS", "ELISACORE_FORCE_BOUNDS_CHECK", "ELISACORE_TARGET_CPU"}
	for _, name := range flagEnvs {
		t.Setenv(name, "") // restored after the test; the CLI sets these process-wide
	}
	reset := func() {
		for _, name := range flagEnvs {
			_ = os.Unsetenv(name)
		}
	}
	src := filepath.Join(dir, "prog.elisa")
	if err := os.WriteFile(src, []byte("def scale(a: f64, b: f64, c: f64) -> f64:\n    return a * b + c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(flag string) string {
		t.Helper()
		args := []string{"-emit", "obj", "-o", filepath.Join(dir, "out.o")}
		if flag != "" {
			args = append(args, flag)
		}
		var stdout, stderr bytes.Buffer
		if rc := runCLI(append(args, src), &stdout, &stderr); rc != 0 {
			t.Fatalf("emit obj %s failed: %s", flag, stderr.String())
		}
		return stderr.String()
	}
	reset()
	if s := build(""); !strings.Contains(s, "build-object miss-store") {
		t.Fatalf("baseline should miss-store:\n%s", s)
	}
	for _, flag := range []string{"-ffast-math", "-fstrict-fp", "-fnoalias", "-fbounds-check", "-mcpu=generic", "-flto"} {
		reset()
		if s := build(flag); !strings.Contains(s, "build-object miss-store") {
			t.Fatalf("%s was served the cached object of a build without it:\n%s", flag, s)
		}
		reset()
		if s := build(""); !strings.Contains(s, "build-object hit") {
			t.Fatalf("after %s, the flag-less build should hit again:\n%s", flag, s)
		}
	}
}

// A valid disk hit must not depend on server availability or transport settings.
func TestBuildObjectCacheHitBeforeServerDiscovery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_TEST_CACHE_DEBUG", "1")
	t.Setenv("ELISACORE_RETAINED_SERVER", "")
	t.Setenv("ELISACORE_RETAINED_AUTO_MIN", "0")
	src := filepath.Join(dir, "prog.elisa")
	if err := os.WriteFile(src, []byte("def value() -> i64:\n    return 42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(dir, "first.o")
	emitObjForCacheTest(t, src, first)
	t.Setenv("ELISACORE_RETAINED_SERVER", filepath.Join(dir, "absent.sock"))
	second := filepath.Join(dir, "second.o")
	var stdout, stderr bytes.Buffer
	code, hit := tryBuildObjectCacheHit([]string{"-emit", "obj", "-o", second, src}, &stdout, &stderr)
	if code != 0 || !hit || !strings.Contains(stderr.String(), "build-object hit") {
		t.Fatalf("pre-server lookup: %d %v %s", code, hit, &stderr)
	}
	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	if !bytes.Equal(a, b) {
		t.Fatal("pre-server lookup changed output")
	}
	if _, err := os.Stat(filepath.Join(dir, "absent.sock")); !os.IsNotExist(err) {
		t.Fatal("lookup created a server socket")
	}
	// Codegen flags must be applied before the pre-server key is computed.
	t.Setenv("ELISACORE_FORCE_BOUNDS_CHECK", "")
	if _, hit := tryBuildObjectCacheHit([]string{"-fbounds-check", "-emit", "obj", "-o", second, src}, &stdout, &stderr); hit {
		t.Fatal("changed codegen flags reused old object")
	}
}

// Exercise main, rather than runCLI, so moving server forwarding ahead of disk
// lookup cannot silently regress standalone reuse. Every child has a 30s cap.
func TestBuildObjectCacheMainAcrossProcesses(t *testing.T) {
	if os.Getenv("TEST_BUILD_CACHE_MAIN") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
				main()
			}
		}
		panic("missing child CLI arguments")
	}
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_TEST_CACHE_DEBUG", "1")
	t.Setenv("ELISACORE_RETAINED_SERVER", "")
	source := filepath.Join(dir, "program.elisa")
	if err := os.WriteFile(source, []byte("def answer() -> i64:\n    return 42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var original []byte
	for i, server := range []string{"", filepath.Join(dir, "nonexistent.sock")} {
		t.Setenv("ELISACORE_RETAINED_SERVER", server)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out := filepath.Join(dir, fmt.Sprintf("result%d.o", i))
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBuildObjectCacheMainAcrossProcesses$", "--", "-emit", "obj", "-o", out, source)
		cmd.Env = append(os.Environ(), "TEST_BUILD_CACHE_MAIN=1")
		transcript, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("child%d: %v %s", i, err, transcript)
		}
		if strings.Contains(string(transcript), "[retained]") {
			t.Fatalf("child%d contacted server: %s", i, transcript)
		}
		if i == 1 && !strings.Contains(string(transcript), "build-object hit") {
			t.Fatalf("fresh process missed disk: %s", transcript)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			original = data
		} else if !bytes.Equal(original, data) {
			t.Fatal("disk output differs across processes")
		}
	}
}

// Wrapper-only settings must not make the same compiler invocation miss.
func TestBuildObjectCacheIgnoresSeedWrapperSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_TEST_CACHE_DEBUG", "1")
	src := filepath.Join(dir, "prog.elisa")
	if err := os.WriteFile(src, []byte("def add(a: i64, b: i64) -> i64:\n    return a + b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out1 := filepath.Join(dir, "first.o")
	if stderr := emitObjForCacheTest(t, src, out1); !strings.Contains(stderr, "build-object miss-store") {
		t.Fatalf("initial miss: %s", stderr)
	}
	for _, name := range []string{"ELISA_STAGE1_SEED_MAX_RSS_KB", "ELISA_STAGE1_SEED_OPT_LEVEL", "ELISA_STAGE1_SEED_BOOTSTRAP_BIN", "ELISA_STAGE1_SEED_BOOTSTRAP_KIND", "ELISA_STAGE1_SEED_CLANG", "ELISA_STAGE0_SOURCE_ROOT"} {
		t.Setenv(name, "changed-wrapper-setting")
	}
	if !strings.Contains(strings.Join(buildCacheEnvKey(), "\n"), "ELISA_STAGE1_SEED_MAX_RSS_KB=changed-wrapper-setting") {
		t.Fatal("test execution environment omitted from general cache identity")
	}
	out2 := filepath.Join(dir, "second.o")
	if stderr := emitObjForCacheTest(t, src, out2); !strings.Contains(stderr, "build-object hit") {
		t.Fatalf("wrapper setting caused a miss: %s", stderr)
	}
	a, err := os.ReadFile(out1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("cache replay changed object")
	}
}
