package main

import (
	"bytes"
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

func TestAnalysisDiskContractHunkSchedulesScratch(t *testing.T) {
	t.Setenv("ELISACORE_RETAINED_FAST_PREPARE", "on")
	artifact := analysisDiskArtifact{path: filepath.Join(t.TempDir(), "baseline")}
	for _, clause := range []string{
		"requires value > 0", "ensure result > 0", "decreases value", "decreases * \"reason\"",
		"uses Positive(value)", "changes value", "preserves value", "fulfills Rule(value)",
	} {
		t.Run(clause, func(t *testing.T) {
			before := "def f(value: i64) -> i64:\n    " + clause + "\n    return value\n"
			after := "def f(value: i64) -> i64:\n    " + clause + " # edited\n    return value\n"
			if err := os.WriteFile(artifact.baselineSourcePath(), []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			if analysisDiskLikelyBodyEdit(artifact, []byte(after)) {
				t.Fatal("contract change deferred declaration preparation")
			}
			without := "def f(value: i64) -> i64:\n    return value\n"
			if analysisDiskLikelyBodyEdit(artifact, []byte(without)) {
				t.Fatal("contract removal deferred declaration preparation")
			}
			if err := os.WriteFile(artifact.baselineSourcePath(), []byte(without), 0600); err != nil {
				t.Fatal(err)
			}
			if analysisDiskLikelyBodyEdit(artifact, []byte(before)) {
				t.Fatal("contract insertion deferred declaration preparation")
			}
		})
	}
	before := "def f() -> i64:\n    ensure_count: i64 = 1\n    return ensure_count\n"
	if err := os.WriteFile(artifact.baselineSourcePath(), []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	after := strings.Replace(before, "ensure_count: i64 = 1", "ensure_count: i64 = 2", 1)
	if !analysisDiskLikelyBodyEdit(artifact, []byte(after)) {
		t.Fatal("identifier with contract-keyword prefix caused eager preparation")
	}
}

func TestAnalysisDiskLikelyBodyEditSourcesMatchesSplitReference(t *testing.T) {
	samples := [][]byte{nil, {}, []byte("\n"), []byte("\n\n"), []byte("a"), []byte("a\n"), []byte("a\nb"), []byte(" a\n\tb\n")}
	var addShortStrings func([]byte, int)
	addShortStrings = func(prefix []byte, remaining int) {
		samples = append(samples, bytes.Clone(prefix))
		if remaining == 0 {
			return
		}
		for _, next := range []byte{'a', ' ', '\t', '\n'} {
			addShortStrings(append(prefix, next), remaining-1)
		}
	}
	addShortStrings(nil, 4)
	for _, text := range []string{
		"requires value > 0", "ensure result > 0", "decreases value", "uses Positive(value)",
		"changes value", "preserves value", "fulfills Rule(value)", "ensure_count: i64 = 1",
		"def f() -> i64:\r\n    return 1\r\n",
	} {
		samples = append(samples, []byte(text))
	}

	for beforeIndex, before := range samples {
		for afterIndex, after := range samples {
			want := analysisDiskLikelyBodyEditSourcesSplitReference(before, after)
			if got := analysisDiskLikelyBodyEditSources(before, after); got != want {
				t.Fatalf("pair (%d,%d), before=%q after=%q: got=%v want=%v", beforeIndex, afterIndex, before, after, got, want)
			}
		}
	}
}

func analysisDiskLikelyBodyEditSourcesSplitReference(before, after []byte) bool {
	beforeLines, afterLines := bytes.Split(before, []byte("\n")), bytes.Split(after, []byte("\n"))
	for len(beforeLines) != 0 && len(afterLines) != 0 && bytes.Equal(beforeLines[0], afterLines[0]) {
		beforeLines, afterLines = beforeLines[1:], afterLines[1:]
	}
	for len(beforeLines) != 0 && len(afterLines) != 0 && bytes.Equal(beforeLines[len(beforeLines)-1], afterLines[len(afterLines)-1]) {
		beforeLines, afterLines = beforeLines[:len(beforeLines)-1], afterLines[:len(afterLines)-1]
	}
	for _, lines := range [][][]byte{beforeLines, afterLines} {
		for _, line := range lines {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) != 0 && (!analysisDiskIndented(line) || analysisDiskContractClause(trimmed)) {
				return false
			}
		}
	}
	return true
}

func BenchmarkAnalysisDiskLikelyBodyEditSources(b *testing.B) {
	const line = "    return value + 1\n"
	before := append([]byte("def f() -> i64:\n"), bytes.Repeat([]byte(line), 50_000)...)
	after := bytes.Clone(before)
	edit := len(after) / 2
	after[edit] ^= 1 // The changed hunk remains an indented body line.

	for _, benchmark := range []struct {
		name string
		fn   func([]byte, []byte) bool
	}{
		{"cursor", analysisDiskLikelyBodyEditSources},
		{"split-reference", analysisDiskLikelyBodyEditSourcesSplitReference},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.SetBytes(int64(len(before)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if !benchmark.fn(before, after) {
					b.Fatal("body edit was not recognized")
				}
			}
		})
	}
}
