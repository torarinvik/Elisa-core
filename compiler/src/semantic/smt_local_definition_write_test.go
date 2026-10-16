//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS: an immutable local's defining equation (`y == x + 1`) holds only while every binding
// the initializer read still has the value it had at the declaration. A later `x <- 5` used to be
// ignored, so `ensure result == 6` was "proven" for `return y` although y is x0 + 1.
func TestSMTLocalDefinitionDroppedAfterDependencyWrite(t *testing.T) {
	cases := map[string]string{
		"reassigned_after": `
def reassigned_after(x0: i64) -> i64:
    requires x0 > 0 and x0 < 100
    ensure result == 6
    x: mutable i64 = x0
    y: i64 = x + 1
    x <- 5
    return y
`,
		"written_in_loop": `
def written_in_loop(x0: i64) -> i64:
    requires x0 > 0 and x0 < 100
    ensure result == x0 + 1
    x: mutable i64 = x0
    y: i64 = x + 1
    i: mutable i64 = 0
    while i < 3:
        decreases 3 - i
        x <- x + 1
        i <- i + 1
    return x + 1
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", src)
			if ensureFailuresFor(result.Errors(), name) == 0 {
				t.Fatalf("%s: the ensure is false for some input and must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}

// The equation still serves when no write follows the declaration.
func TestSMTLocalDefinitionKeptWithoutLaterWrite(t *testing.T) {
	src := `
def unwritten(x0: i64) -> i64:
    requires x0 > 0 and x0 < 100
    ensure result == x0 + 1
    x: mutable i64 = x0
    x <- x0
    y: i64 = x + 1
    return y
`
	result := analyzeContractStrict(t, "unwritten.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "unwritten"); n != 0 {
		t.Fatalf("expected the ensure to prove through y's definition, got: %v", result.Errors())
	}
}

// A callee ensure over `result` alone holds for the returned value whatever the arguments were, so it
// survives a later write to an argument that retires the local's defining equation.
func TestSMTCallLocalResultOnlyEnsureSurvivesArgumentWrite(t *testing.T) {
	src := `
def bounded(p: i64) -> i64:
    ensure result == -1 or (result >= 0 and result <= 9)
    return -1 if p < 0 or p > 9 else p

def needs_digit(d: i64) -> i64:
    requires d >= 0 and d <= 9
    return d

def caller(p0: i64) -> i64:
    p: mutable i64 = p0
    d: i64 = bounded(p)
    p <- p + 4
    if d < 0:
        return 0
    return needs_digit(d)
`
	result := analyzeContractStrict(t, "result_only.elisa", src)
	for _, err := range result.Errors() {
		if strings.Contains(err, "precondition") {
			t.Fatalf("expected the result-only ensure to discharge the precondition, got: %v", result.Errors())
		}
	}
}

// NEGATIVE: an ensure that reads a formal is NOT assumed after the argument was written.
func TestSMTCallLocalParamEnsureDroppedAfterArgumentWrite(t *testing.T) {
	src := `
def same(p: i64) -> i64:
    ensure result == p
    return p

def stale_arg(p0: i64) -> i64:
    requires p0 > 0 and p0 < 100
    ensure result == p0 + 4
    p: mutable i64 = p0
    d: i64 = same(p)
    p <- p + 4
    return d
`
	result := analyzeContractStrict(t, "param_ensure.elisa", src)
	if ensureFailuresFor(result.Errors(), "stale_arg") == 0 {
		t.Fatalf("the ensure is false (d == p0) and must stay unproven; got: %v", result.Errors())
	}
}
