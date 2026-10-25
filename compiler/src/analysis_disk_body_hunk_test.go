package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisDiskBodyEditHunkScheduling(t *testing.T) {
	t.Setenv("ELISACORE_RETAINED_FAST_PREPARE", "on")
	artifact := analysisDiskArtifact{path: filepath.Join(t.TempDir(), "baseline")}
	before := "def calculate(value: i64) -> i64:\n    return value + 1\n\ndef other() -> i64:\n    return 2\n"
	if err := os.WriteFile(artifact.baselineSourcePath(), []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, source string
		want         bool
	}{
		{"unchanged", before, true},
		{"crlf-insert", strings.Replace(strings.ReplaceAll(before, "\n", "\r\n"), "    return value + 1", "    if value < 3:\r\n        return 0\r\n    return value + 1", 1), true},
		{"insert-control-flow", "def calculate(value: i64) -> i64:\n    if value < 3:\n        return 0\n    return value + 1\n\ndef other() -> i64:\n    return 2\n", true},
		{"delete-body-line", "def calculate(value: i64) -> i64:\n\ndef other() -> i64:\n    return 2\n", true},
		{"blank-body-line", "def calculate(value: i64) -> i64:\n\n    return value + 1\n\ndef other() -> i64:\n    return 2\n", true},
		{"signature", "def calculate(value: u64) -> i64:\n    return value + 1\n\ndef other() -> i64:\n    return 2\n", false},
		{"include", "include \"other.elisa\"\n" + before, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			baseline := before
			if c.name == "crlf-insert" {
				baseline = strings.ReplaceAll(before, "\n", "\r\n")
			}
			if err := os.WriteFile(artifact.baselineSourcePath(), []byte(baseline), 0600); err != nil {
				t.Fatal(err)
			}
			if got := analysisDiskLikelyBodyEdit(artifact, []byte(c.source)); got != c.want {
				t.Fatalf("got=%v want=%v", got, c.want)
			}
		})
	}
	t.Setenv("ELISACORE_RETAINED_FAST_PREPARE", "off")
	if analysisDiskLikelyBodyEdit(artifact, []byte(before)) {
		t.Fatal("disabled mode guessed lazy")
	}
}
