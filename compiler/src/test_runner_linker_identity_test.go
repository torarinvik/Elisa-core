package main

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveTestRunnerLinkerPathUsesPATHOnlyWhenUnambiguous(t *testing.T) {
	if !nativeToolFixturesSupported() {
		t.Skip("native linker fixtures are supported on ELF and Mach-O hosts")
	}
	dir := t.TempDir()
	linker := filepath.Join(dir, "ld")
	if err := os.WriteFile(linker, nativeToolTestFixture("selected-linker"), 0o755); err != nil {
		t.Fatalf("write linker fixture: %v", err)
	}
	t.Setenv("PATH", dir)

	resolved, err := resolveTestRunnerLinkerPath("ld", "x86_64-unknown-linux-gnu")
	if err != nil {
		t.Fatalf("resolve ELF linker from PATH: %v", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(linker) {
		t.Fatalf("resolved linker = %q, want %q", resolved, linker)
	}
	if _, err := resolveTestRunnerLinkerPath("ld", "x86_64-apple-darwin"); err == nil {
		t.Fatal("ambiguous Mach-O linker selector was admitted through PATH")
	}
	if runtime.GOOS == "darwin" {
		if _, err := resolveTestRunnerLinkerPath("ld", ""); err == nil {
			t.Fatal("ambiguous host linker selector was admitted through PATH on macOS")
		}
	}
}

func TestTestRunnerLinkerIdentityTracksSamePathReplacement(t *testing.T) {
	if !nativeToolFixturesSupported() {
		t.Skip("native linker fixtures are supported on ELF and Mach-O hosts")
	}
	linker := filepath.Join(t.TempDir(), "ld")
	write := func(contents string) {
		t.Helper()
		if err := os.WriteFile(linker, nativeToolTestFixture(contents), 0o755); err != nil {
			t.Fatalf("write linker fixture: %v", err)
		}
	}
	key := func() string {
		t.Helper()
		hash := sha256.New()
		if err := writeTestRunnerLinkerCacheIdentity(hash, linker); err != nil {
			t.Fatalf("write linker cache identity: %v", err)
		}
		return string(hash.Sum(nil))
	}

	write("linker-v1")
	first := key()
	write("replacement-linker-v2")
	if first == key() {
		t.Fatal("same-path linker replacement retained the test-runner identity")
	}
	if err := os.WriteFile(linker, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("replace linker fixture with script: %v", err)
	}
	if err := writeTestRunnerLinkerCacheIdentity(sha256.New(), linker); err == nil {
		t.Fatal("script linker was admitted to the persistent test-runner identity")
	}
}

func TestTestRunnerCacheDeclinesOpaqueLinkDependencies(t *testing.T) {
	for _, linkFlags := range [][]string{
		{"-lfoo"},
		{"-L", "vendor/lib"},
		{"@link.rsp"},
		{"-Wl,-T,linker.ld"},
		{"-Tlinker.ld"},
		{"-Xlinker", "--version-script=exports.map"},
		{"-plugin=plugin.so"},
		{"-I", "include"},
		{"-DVALUE=1"},
		{"-std=c++20"},
		{"vendor/libfoo.a"},
	} {
		if err := writeTestRunnerLinkDependencyInputs(sha256.New(), linkFlags); err == nil {
			t.Errorf("opaque link flags %q were admitted to the persistent test-runner cache", linkFlags)
		}
	}
	if err := writeTestRunnerLinkDependencyInputs(sha256.New(), []string{"-fuse-ld=lld"}); err != nil {
		t.Fatalf("keyed linker-selection flag declined: %v", err)
	}
}

func TestTestRunnerCacheKeysDirectLinkArchiveContents(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "libnative.a")
	write := func(contents string) {
		t.Helper()
		if err := os.WriteFile(archive, append([]byte("!<arch>\n"), []byte(contents)...), 0o644); err != nil {
			t.Fatalf("write archive fixture: %v", err)
		}
	}
	key := func() string {
		t.Helper()
		hash := sha256.New()
		if err := writeTestRunnerLinkDependencyInputs(hash, []string{archive}); err != nil {
			t.Fatalf("write direct link dependency identity: %v", err)
		}
		return string(hash.Sum(nil))
	}

	write("member-v1")
	first := key()
	write("replacement-member-v2")
	if first == key() {
		t.Fatal("direct archive replacement retained the test-runner identity")
	}
}

func TestTestRunnerCacheDeclinesExternalIncludeEnvironment(t *testing.T) {
	for _, name := range []string{"CPATH", "C_INCLUDE_PATH", "CPLUS_INCLUDE_PATH", "OBJC_INCLUDE_PATH"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "")
			if err := testRunnerCacheIncludeEnvironmentIsSafe(); err == nil {
				t.Fatalf("test-runner cache admitted externally selected include path from %s", name)
			}
		})
	}
}
