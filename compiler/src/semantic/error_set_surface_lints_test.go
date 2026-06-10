package semantic

import (
	"strings"
	"testing"
)

func deprecatedDiagnostics(result *Result) string {
	var out []string
	for _, d := range result.Diagnostics {
		if d.Severity == DiagnosticSeverityDeprecated {
			out = append(out, d.String())
		}
	}
	return strings.Join(out, "\n")
}

// The `, ...` suffix in error[...] expands listed tags to whole declared
// families — a closed set, despite reading as "open to more". It is deprecated
// in favor of naming the full sets directly.
func TestErrorSetEllipsisSuffixDeprecated(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "esl_ellipsis.elisa", `
error IoErr:
    Bad

extern reader(f: func() -> i64 error[IoErr, ...]) -> i64
`)
	dep := deprecatedDiagnostics(result)
	if !strings.Contains(dep, "`, ...` suffix in error[...] is deprecated") {
		t.Fatalf("expected error[IoErr, ...] to emit the ellipsis deprecation, got:\n%s", dep)
	}
}

// The qualified-tag form `error[Set.Tag, ...]` (round Tag up to its whole
// family) gets the same deprecation, and its expansion semantics stay intact.
func TestErrorSetEllipsisQualifiedTagDeprecatedButStillExpands(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "esl_ellipsis_qualified.elisa", `
error IoErr:
    Bad
    Worse

extern reader() -> i64 error[IoErr.Bad, ...]

def use() -> i64 error[IoErr]:
    return try reader()
`)
	dep := deprecatedDiagnostics(result)
	if !strings.Contains(dep, "`, ...` suffix in error[...] is deprecated") {
		t.Fatalf("expected error[IoErr.Bad, ...] to emit the ellipsis deprecation, got:\n%s", dep)
	}
}

// An `[errorset X]` parameter that reuses the name of a DECLARED error set
// silently rebinds every `error[X]` in the signature to the parameter — the
// signature reads as raising the declared set while accepting anything. Warn.
func TestErrorSetParamShadowingDeclaredSetWarns(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "esl_shadow.elisa", `
error IoErr:
    Bad

error NetErr:
    Down

extern fetch() -> i64 error[NetErr]

def shady[errorset IoErr](f: func() -> i64 error[IoErr]) -> i64 error[IoErr]:
    return try f()

def use() -> i64 error[NetErr]:
    return shady(fetch)
`)
	warnings := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(warnings, `errorset parameter "IoErr" on "shady" shadows the declared error set`) {
		t.Fatalf("expected the shadowing warning for [errorset IoErr], got:\n%s", warnings)
	}
}

// A fresh, non-colliding errorset parameter name stays warning-free.
func TestErrorSetParamFreshNameNoShadowWarning(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "esl_fresh.elisa", `
error IoErr:
    Bad

extern reader() -> i64 error[IoErr]

def applies[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    return try f()

def use() -> i64 error[IoErr]:
    return applies(reader)
`)
	warnings := strings.Join(result.Warnings(), "\n")
	if strings.Contains(warnings, "shadows the declared error set") {
		t.Fatalf("expected no shadowing warning for [errorset R], got:\n%s", warnings)
	}
}
