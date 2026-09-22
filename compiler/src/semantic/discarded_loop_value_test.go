//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/119 §3: an accumulator loop whose value is dropped — a statement with more statements
// after it — is an ERROR, not the opt-in W1 warning: the accumulator is declared inside the
// loop's block, so nothing else holds the value (analyzer_discarded_loop_value.go).

const discardedLoopValueMessage = "accumulator loop result `valid` is discarded; make the loop the final expression of its block"

func discardedLoopValueErrors(t *testing.T, name string, src string) string {
	t.Helper()
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{})
	return strings.Join(result.Errors(), "\n")
}

func TestDiscardedLoopValueRejected(t *testing.T) {
	cases := map[string]string{
		// The elisa-engine validator shape: always returns true.
		"for": `
def all_positive(values: i64[3]) -> bool:
    for i in 0..<3 |valid: bool = true| -> valid:
        valid <- false if values[i] <= 0
    true
`,
		"while": `
def all_positive(values: i64[3]) -> bool:
    while index < 3 |index: usize = 0, valid: bool = true| -> valid:
        valid <- false if values[index] <= 0
        index <- index + 1
    true
`,
		// A value block's tail follows every statement in it.
		"value block": `
def all_positive(values: i64[3]) -> bool:
    ok: bool =
        for i in 0..<3 |valid: bool = true| -> valid:
            valid <- false if values[i] <= 0
        true
    return ok
`,
		// A void function's non-final statement is a discard like any other.
		"void": `
def check(values: i64[3]) -> void:
    for i in 0..<3 |valid: bool = true| -> valid:
        valid <- false if values[i] <= 0
    pass
`,
	}
	for name, src := range cases {
		if errs := discardedLoopValueErrors(t, "discarded_loop.elisa", src); !strings.Contains(errs, discardedLoopValueMessage) {
			t.Errorf("%s: expected the discarded-loop-value error, got: %s", name, errs)
		}
	}
}

func TestDiscardedLoopValueAccepted(t *testing.T) {
	cases := map[string]string{
		"final expression": `
def all_positive(values: i64[3]) -> bool:
    for i in 0..<3 |valid: bool = true| -> valid:
        valid <- false if values[i] <= 0
`,
		"bound": `
def all_positive(values: i64[3]) -> bool:
    result: bool = for i in 0..<3 |valid: bool = true| -> valid:
        valid <- false if values[i] <= 0
    return result
`,
		"statement form": `
def all_positive(values: i64[3]) -> bool:
    for i in 0..<3 |valid: bool = true|:
        valid <- false if values[i] <= 0
    true
`,
		// The loop writes a captured outer binding in place; dropping its yield loses nothing.
		"captured yield": `
def total_positive(values: i64[3]) -> i64:
    valid: mutable i64 = 0
    for i in 0..<3 |valid| -> valid:
        valid <- valid + values[i] if values[i] > 0
    valid
`,
	}
	for name, src := range cases {
		if errs := discardedLoopValueErrors(t, "kept_loop.elisa", src); strings.Contains(errs, "is discarded") {
			t.Errorf("%s: expected no discarded-loop-value error, got: %s", name, errs)
		}
	}
}
