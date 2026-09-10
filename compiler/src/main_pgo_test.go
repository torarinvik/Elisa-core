package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestCLIProfileUseMarksMeasuredHotFunction(t *testing.T) {
	directory := t.TempDir()
	sourcePath := directory + "/profile.elisa"
	profilePath := directory + "/profile.elisapgo"
	if err := os.WriteFile(sourcePath, []byte("@inline(never)\ndef hot_helper(value: i64) -> i64:\n    return value + 1\n\ndef main() -> i64:\n    return hot_helper(1)\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(profilePath, []byte("ELISA_PGO_V1\nhot hot_helper\n"), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", "-O0", "-fprofile-use", profilePath, sourcePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("profile-guided compile failed with code %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hot") {
		t.Fatalf("profile-selected function did not receive LLVM hot attribute:\n%s", stdout.String())
	}
}

func TestCLIProfileUseRejectsMalformedProfile(t *testing.T) {
	directory := t.TempDir()
	profilePath := directory + "/invalid.elisapgo"
	if err := os.WriteFile(profilePath, []byte("not a profile\n"), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	if _, err := parseArgs([]string{"-fprofile-use", profilePath}); err == nil {
		t.Fatal("malformed optimization profile was accepted")
	}
}
