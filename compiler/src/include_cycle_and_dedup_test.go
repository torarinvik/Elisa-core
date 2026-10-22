package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSourceWithIncludesRejectsCycle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.elisa")
	child := filepath.Join(dir, "child.elisa")
	if err := os.WriteFile(root, []byte("include \"child.elisa\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("include \"root.elisa\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := readSourceWithIncludes(root, map[string]bool{})
	if err == nil || !strings.Contains(err.Error(), "cyclic include detected") {
		t.Fatalf("expected include cycle error, got %v", err)
	}
}

func TestReadSourceWithIncludesDeduplicatesRepeatedInclude(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.elisa")
	leaf := filepath.Join(dir, "leaf.elisa")
	if err := os.WriteFile(root, []byte("include \"leaf.elisa\"\ninclude \"leaf.elisa\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, []byte("leaf_value: i64 = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	expanded, err := readSourceWithIncludes(root, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(expanded), "leaf_value: i64 = 1"); got != 1 {
		t.Fatalf("repeated include emitted leaf %d times, want once:\n%s", got, expanded)
	}
}
