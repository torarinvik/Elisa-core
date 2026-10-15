package main

import (
	"bytes"
	"os"
	"testing"
)

// The checked-in walker must be the generator's current output: a field added to an AST struct
// that the walker does not visit would silently drop nodes from every region/store analysis.
// Regenerate with: (cd compiler && go run ./internal/astwalkgen)
func TestGeneratedWalkerIsCurrent(t *testing.T) {
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	for _, m := range modes {
		want, err := generate(m)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(m.File)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s is stale; regenerate with go run ./internal/astwalkgen", m.File)
		}
	}
}
