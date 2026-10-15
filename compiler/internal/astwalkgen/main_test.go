package main

import (
	"bytes"
	"os"
	"testing"
)

// The checked-in walker must be the generator's current output: a field added to an AST struct
// that the walker does not visit would silently drop nodes from every region/store analysis.
// Regenerate with: (cd compiler && go run ./internal/astwalkgen > src/semantic/ast_pointer_walk_gen.go)
func TestGeneratedWalkerIsCurrent(t *testing.T) {
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	want, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("src/semantic/ast_pointer_walk_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("src/semantic/ast_pointer_walk_gen.go is stale; regenerate it with go run ./internal/astwalkgen")
	}
}
