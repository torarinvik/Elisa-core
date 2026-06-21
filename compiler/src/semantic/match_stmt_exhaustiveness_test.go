package semantic

import (
	"strings"
	"testing"
)

const matchExhaustivenessColorEnum = `enum Color:
    Red
    Green
    Blue
`

// A statement-form match over an enum that omits a variant (and has no `_`)
// is non-exhaustive and must be a hard error naming the missing variant.
func TestEnumMatchStmtMissingVariantErrors(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "missing.elisa", matchExhaustivenessColorEnum+`
def code(c: Color) -> i64:
    match c:
        Color.Red:
            return 0
        Color.Green:
            return 1
    return -1
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "non-exhaustive") {
		t.Fatalf("expected non-exhaustive error, got:\n%s", all)
	}
	if !strings.Contains(all, "Color.Blue") {
		t.Fatalf("expected the error to name the missing Color.Blue, got:\n%s", all)
	}
}

// A statement-form match that handles every variant is exhaustive — no error.
func TestEnumMatchStmtAllVariantsOk(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "complete.elisa", matchExhaustivenessColorEnum+`
def code(c: Color) -> i64:
    match c:
        Color.Red:
            return 0
        Color.Green:
            return 1
        Color.Blue:
            return 2
    return -1
`)
	all := allDiagnostics(result)
	if strings.Contains(all, "non-exhaustive") {
		t.Fatalf("a complete enum match must NOT be flagged non-exhaustive, got:\n%s", all)
	}
}

// A `_` catch-all makes a statement-form match exhaustive even when variants
// are omitted.
func TestEnumMatchStmtWildcardOk(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "wildcard.elisa", matchExhaustivenessColorEnum+`
def code(c: Color) -> i64:
    match c:
        Color.Red:
            return 0
        _:
            return -1
`)
	all := allDiagnostics(result)
	if strings.Contains(all, "non-exhaustive") {
		t.Fatalf("a `_`-covered match must NOT be flagged non-exhaustive, got:\n%s", all)
	}
}

// A redundant arm (a variant already handled) is reported as unreachable.
func TestEnumMatchStmtRedundantArmUnreachable(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "redundant.elisa", matchExhaustivenessColorEnum+`
def code(c: Color) -> i64:
    match c:
        Color.Red:
            return 0
        Color.Green:
            return 1
        Color.Blue:
            return 2
        Color.Red:
            return 3
    return -1
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "unreachable") {
		t.Fatalf("expected an unreachable/redundant-arm diagnostic, got:\n%s", all)
	}
}
