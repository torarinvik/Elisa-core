package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceLabelsMatchCanonicalPaths(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(real, "source.elisa")
	if err := os.WriteFile(file, []byte("value: i64 = 1"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(real, "leaf.elisa")
	if err := os.Symlink(file, leaf); err != nil {
		t.Fatal(err)
	}
	labels := sourceLabelResolver{}
	for _, name := range []string{file, filepath.Join(alias, "source.elisa"), leaf, filepath.Join(alias, "leaf.elisa")} {
		expected, err := filepath.EvalSymlinks(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := labels.resolve(name); got != expected {
			t.Errorf("%s: got %s, want %s", name, got, expected)
		}
	}
	// Leaf identities remain dynamic even after the parent has been cached.
	if err := os.Remove(leaf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, []byte("value: i64 = 2"), 0600); err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels.resolve(leaf); got != expected {
		t.Fatalf("replaced leaf: got %s, want %s", got, expected)
	}
}
