package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeUnifiedElseRecoveryForOptionalAndTry(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "unified_else_recovery.elisa", `
error FileError:
	NotFound

extern read_value(flag: bool) -> i64 error[FileError]

def fallback_value(maybe: i64?, flag: bool) -> i64:
	a: i64 = maybe else 11
	b: i64 = maybe else return 12
	c: i64 = try read_value(flag) else err:
		return 13
	return a + b + c
`)
}

func TestAnalyzeRejectsElseVoidInValueContext(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "else_void_value_context.elisa", `
def bad(maybe: i64?) -> i64:
	return maybe else void
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "else fallback cannot use else void") {
		t.Fatalf("expected else void diagnostic, got:\n%s", got)
	}
}

func TestAnalyzeDeprecatedQuestionMarkRecoverySyntax(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "deprecated_question_recovery.elisa", `
error FileError:
	NotFound

extern read_value() -> i64 error[FileError]

def fallback_value(maybe: i64?) -> i64:
	return? maybe
	return try? read_value() default 1
`)
	warnings := strings.Join(result.Deprecations(), "\n")
	for _, check := range []string{"`return?` is deprecated", "`try? ... default` is deprecated"} {
		if !strings.Contains(warnings, check) {
			t.Fatalf("expected warning %q, got:\n%s", check, warnings)
		}
	}
}
