//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

const calleeEnsureOrderCaller = `
def caller(text: sview) -> (valid: bool, value: i64):
    ensure not result.valid implies result.value == 0
    if text.len == 0:
        return (true, 0)
    return callee(text)
`

const calleeEnsureOrderCallee = `
def reference(text: sview) -> (valid: bool, value: i64):
    return (text.len > 0, 0)

def callee(text: sview) -> (valid: bool, value: i64):
    ensure result.valid == reference(text).valid and result.value == reference(text).value and (result.valid or result.value == 0)
    return reference(text)
`

// A callee's declared ensures are usable at a call site whether or not the callee's
// body was analyzed first: the verdict may not depend on body-analysis order.
func TestCalleeEnsureIndependentOfBodyOrder(t *testing.T) {
	for name, src := range map[string]string{
		"caller_first": calleeEnsureOrderCaller + calleeEnsureOrderCallee,
		"callee_first": calleeEnsureOrderCallee + calleeEnsureOrderCaller,
	} {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", src)
			if n := ensureFailuresFor(result.Errors(), "caller"); n != 0 {
				t.Fatalf("%s: callee ensures not used: %v", name, result.Errors())
			}
		})
	}
}

// Name-keyed bounds facts (`start < xs.count` from an early-return guard) must not leak from one
// body into the next: `unguarded` has no proof that `start` is in bounds, whatever precedes it.
func TestIndexBoundsFactsDoNotLeakAcrossBodies(t *testing.T) {
	const guarded = `
def guarded(xs: darray[i32]&, start: usize) -> i32:
    return 0 if start >= xs.count
    return xs[start]
`
	const unguarded = `
def unguarded(xs: darray[i32]&, start: usize) -> i32:
    return xs[start]
`
	for name, src := range map[string]string{"guarded_first": guarded + unguarded, "unguarded_first": unguarded + guarded} {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptions(t, name+".elisa", src, AnalyzeOptions{})
			proven := map[int]bool{}
			for expr, ok := range result.IndexBoundsProven {
				if ok {
					proven[expr.Position.Line] = true
				}
			}
			lines := strings.Split(src, "\n")
			for i, line := range lines {
				if strings.Contains(line, "return xs[start]") {
					inGuarded := i > 0 && strings.Contains(lines[i-1], "return 0 if")
					if proven[i+1] != inGuarded {
						t.Fatalf("%s line %d %q: proven=%v, want %v", name, i+1, line, proven[i+1], inGuarded)
					}
				}
			}
		})
	}
}
