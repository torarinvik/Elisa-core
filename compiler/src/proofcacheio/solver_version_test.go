package proofcacheio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSolverVersionTracksReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "solver")
	write := func(text string) {
		t.Helper()
		tmp := path + ".new"
		if err := os.WriteFile(tmp, []byte("#!/bin/sh\nprintf '"+text+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	if got, ok := SolverVersion(path); !ok || got != "first" {
		t.Fatalf("first probe: %q %v", got, ok)
	}
	write("second")
	if got, ok := SolverVersion(path); !ok || got != "second" {
		t.Fatalf("replaced probe: %q %v", got, ok)
	}
}

func TestSolverVersionBoundsOutput(t *testing.T) {
	var output boundedVersionOutput
	if _, err := output.Write([]byte(strings.Repeat("x", 4096))); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("x")); err == nil {
		t.Fatal("accepted oversized version")
	}
}
