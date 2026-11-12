package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Separate edits must take the lazy path and emit the same code and diagnostics
// as a fresh analysis. The unchanged function between them exercises the hunk.
func TestSeparateBodyEditsUseLazyScratchAndMatchCold(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	base := "def first() -> i64:\n    return 1\n\ndef untouched() -> i64:\n    return 2\n\ndef last() -> i64:\n    return 3\n\ndef main() -> i64:\n    return first() + untouched() + last()\n"
	edited := strings.Replace(strings.Replace(base, "return 1", "return 4", 1), "return 3", "return 5", 1)
	if err := os.WriteFile(source, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "replay.ll")
	args := []string{"-emit", "llvm", "-strict", "-o", output, source}
	cache := filepath.Join(dir, "cache")
	if log, code := runDefaultFastPreparationChild(t, nil, cache, false, args...); code != 0 {
		t.Fatalf("baseline failed: %d %s", code, log)
	}
	if err := os.WriteFile(source, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	log, code := runDefaultFastPreparationChild(t, nil, cache, false, args...)
	if code != 0 || !strings.Contains(log, "scratch parsing deferred until needed") || !strings.Contains(log, "fast=true") {
		t.Fatalf("separate body edits did not use fast replay: %d %s", code, log)
	}
	coldOutput := filepath.Join(dir, "cold.ll")
	off := "off"
	cold, code := runDefaultFastPreparationChild(t, &off, filepath.Join(dir, "cold-cache"), true, "-emit", "llvm", "-strict", "-o", coldOutput, source)
	if code != 0 || defaultFastProofTranscript(log) != defaultFastProofTranscript(cold) {
		t.Fatalf("replayed diagnostics differ from cold analysis: %d %s / %s", code, log, cold)
	}
	replayed, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	coldIR, err := os.ReadFile(coldOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(replayed, coldIR) {
		t.Fatal("replayed LLVM IR differs from cold analysis")
	}
}
