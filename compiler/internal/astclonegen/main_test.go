package main

import (
	"bytes"
	"os"
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
	got, err := os.ReadFile("src/ast/syntax_snapshot_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("syntax copier is stale; regenerate with go run ./internal/astclonegen")
	}
}
