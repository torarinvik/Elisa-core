package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
