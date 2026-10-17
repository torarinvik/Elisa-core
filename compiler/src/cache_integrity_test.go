package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsableCachedFileRejectsInvalidEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if usableCachedFile(filepath.Join(dir, "missing"), false) {
		t.Fatal("missing cache entry was accepted")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if usableCachedFile(empty, false) {
		t.Fatal("empty cache entry was accepted")
	}
	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, []byte("object"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !usableCachedFile(regular, false) {
		t.Fatal("non-empty regular cache entry was rejected")
	}
	if usableCachedFile(regular, true) {
		t.Fatal("non-executable runner cache entry was accepted")
	}
	if err := os.Chmod(regular, 0o755); err != nil {
		t.Fatal(err)
	}
	if !usableCachedFile(regular, true) {
		t.Fatal("executable runner cache entry was rejected")
	}
	directory := filepath.Join(dir, "directory")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if usableCachedFile(directory, false) {
		t.Fatal("directory cache entry was accepted")
	}
}

func TestToolchainContentStampDistinguishesToolBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := filepath.Join(dir, "tool-a")
	second := filepath.Join(dir, "tool-b")
	if err := os.WriteFile(first, []byte("toolchain-v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("toolchain-v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	firstStamp, err := toolchainContentStamp(first)
	if err != nil {
		t.Fatal(err)
	}
	secondStamp, err := toolchainContentStamp(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstStamp == secondStamp {
		t.Fatalf("different tool contents produced the same stamp: %q", firstStamp)
	}
	again, err := toolchainContentStamp(first)
	if err != nil {
		t.Fatal(err)
	}
	if again != firstStamp {
		t.Fatalf("tool stamp was not stable: %q != %q", again, firstStamp)
	}
}

func TestToolchainContentStampInvalidatesSamePathReplacement(t *testing.T) {
	dir := t.TempDir()
	toolPath := filepath.Join(dir, "tool")
	replacementPath := filepath.Join(dir, "tool.new")
	fixedTime := time.Unix(1_700_000_000, 0)
	if err := os.WriteFile(toolPath, []byte("toolchain-v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(toolPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	first, err := toolchainContentStamp(toolPath)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a package manager atomically replacing a binary at the same path while
	// preserving the same byte length and mtime. Device/inode and ctime still change.
	if err := os.WriteFile(replacementPath, []byte("toolchain-v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacementPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(toolPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, toolPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(toolPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	second, err := toolchainContentStamp(toolPath)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("same-path replacement reused stale tool digest %q", first)
	}
}
