package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedSyntaxCopierIsCurrent(t *testing.T) {
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	want, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	parts, err := splitGenerated(want)
	if err != nil {
		t.Fatal(err)
	}
	wanted := make(map[string]bool, len(parts))
	for _, part := range parts {
		wanted[part.name] = true
		got, err := os.ReadFile(part.name)
		if err != nil || !bytes.Equal(got, part.data) {
			t.Fatalf("syntax copier part %s is stale; regenerate with go run ./internal/astclonegen", part.name)
		}
	}
	existing, err := filepath.Glob("src/ast/syntax_snapshot_part_*_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range existing {
		if !wanted[path] {
			t.Fatalf("unexpected stale syntax copier part %s", path)
		}
	}
}
