//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/119 §1 (W1): a non-void value discarded in statement position warns — but only
// under the opt-in WarnDiscardedValues channel. void statements never warn; `_ = expr`
// is the deliberate-discard escape hatch (it isn't an ExprStmt, so never reaches W1).

func TestDiscardedValueWarnsUnderFlag(t *testing.T) {
	src := `
def produce() -> i64:
    return 5

def f() -> void:
    produce()
    pass
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "w1.elisa", src, AnalyzeOptions{WarnDiscardedValues: true})
	if !strings.Contains(allDiagnostics(result), "is discarded") {
		t.Fatalf("expected a W1 discarded-value warning, got: %s", allDiagnostics(result))
	}
}

func TestDiscardedValueSilentByDefault(t *testing.T) {
	src := `
def produce() -> i64:
    return 5

def f() -> void:
    produce()
    pass
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "w1off.elisa", src, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "is discarded") {
		t.Fatalf("W1 must be OFF by default, got: %s", allDiagnostics(result))
	}
}

func TestDiscardedVoidCallNeverWarns(t *testing.T) {
	src := `
def side_effect() -> void:
    pass

def f() -> void:
    side_effect()
    pass
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "w1void.elisa", src, AnalyzeOptions{WarnDiscardedValues: true})
	if strings.Contains(allDiagnostics(result), "is discarded") {
		t.Fatalf("a void statement must never warn, got: %s", allDiagnostics(result))
	}
}

func TestDeliberateDiscardNeverWarns(t *testing.T) {
	src := `
def produce() -> i64:
    return 5

def f() -> void:
    _ = produce()
    pass
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "w1discard.elisa", src, AnalyzeOptions{WarnDiscardedValues: true})
	if strings.Contains(allDiagnostics(result), "is discarded") {
		t.Fatalf("`_ = expr` is a deliberate discard and must not warn, got: %s", allDiagnostics(result))
	}
}
