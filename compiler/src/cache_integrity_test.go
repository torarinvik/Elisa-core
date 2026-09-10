package main

import (
	"os"
	"path/filepath"
	"testing"
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
