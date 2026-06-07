package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Graduated strictness (docs/70): the performance-friction lints are warnings by default
// (prototyping stays fluid, the build succeeds) but `-Wperf` promotes them to hard errors
// so shipped code can ban the anti-pattern outright.
func TestRunCLIPerfStrictPromotesLintsToErrors(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		marker string
	}{
		{
			name: "pointer_graph.elisa",
			src: `struct Node:
    value: i64
    next: heap Node&?

def main() -> i64:
    return 0
`,
			marker: "raw self-referential pointer graph",
		},
		{
			name: "churn.elisa",
			src: `struct Node:
    v: i64

def main() -> i64:
    can Memory.Allocate, Abort.Panic:
        region r:
            acc: mutable i64 = 0
            for i in 0..<4:
                n: Node& @r = new[r] Node(i.i64())
                acc <- acc + n.v
            return acc
`,
			marker: "boxes a value on every iteration",
		},
		{
			name: "uninferred_auto_reserve.elisa",
			src: `def main(src: darray[darray[i64]]&) -> i64:
    can Memory.Allocate, Abort.Panic:
        ys: mutable darray[i64] = []
        for chunk in src:
            for x in chunk:
                ys.push(x)
        return ys[0]
`,
			marker: "cannot infer a safe reserve bound",
		},
		{
			name: "unreserved_counting_fill.elisa",
			src: `def main(n: usize) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        gap: i64 = 0
        for i in 0..<n:
            xs.push(i.i64() + gap)
        return xs.count
`,
			marker: "without a matching immediately preceding reserve",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			// Default: a warning — the compile still succeeds.
			var so, se bytes.Buffer
			if code := runCLI([]string{"-emit", "llvm", path}, &so, &se); code != 0 {
				t.Fatalf("expected default (warn) compile to succeed, exit=%d stderr:\n%s", code, se.String())
			}
			if !strings.Contains(se.String(), tc.marker) {
				t.Fatalf("expected a default warning containing %q, got:\n%s", tc.marker, se.String())
			}

			// -Wperf: the same diagnostic becomes a hard error — the compile fails.
			var so2, se2 bytes.Buffer
			if code := runCLI([]string{"-Wperf", "-emit", "llvm", path}, &so2, &se2); code == 0 {
				t.Fatalf("expected -Wperf to fail the compile, but it succeeded; stderr:\n%s", se2.String())
			}
			if !strings.Contains(se2.String(), tc.marker) {
				t.Fatalf("expected the -Wperf error to contain %q, got:\n%s", tc.marker, se2.String())
			}
		})
	}
}
