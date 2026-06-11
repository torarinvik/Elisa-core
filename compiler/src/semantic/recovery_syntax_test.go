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
	a: i64 = get maybe else 11
	b: i64 = get maybe else return 12
	c: i64 = try read_value(flag) else err:
		return 13
	return a + b + c
`)
}

func TestAnalyzeBareTryPropagatesAndTryElseHandlesLocally(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "try_propagates_try_else_handles.elisa", `
error FileError:
	NotFound

extern read_value(flag: bool) -> i64 error[FileError]

def propagate(flag: bool) -> i64 error[FileError]:
	return try read_value(flag)

def handle_locally(flag: bool) -> i64:
	return try read_value(flag) else 7
`)
}

func TestAnalyzeTryElseRecoveryActions(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "try_else_recovery_actions.elisa", `
error FileError:
	NotFound

extern read_value(flag: bool) -> i64 error[FileError]
extern write_value(value: i64) -> void error[FileError]

def fallback_return(flag: bool) -> i64:
	value: i64 = try read_value(flag) else return 7
	return value

def fallback_raise(flag: bool) -> i64 error[FileError]:
	value: i64 = try read_value(flag) else raise FileError.NotFound
	return value

def fallback_void(flag: bool) -> void:
	try write_value(1) else void
`)
}

func TestAnalyzeRejectsBareTryWithoutErrorUnionReturn(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "bare_try_requires_error_return.elisa", `
error FileError:
	NotFound

extern read_value(flag: bool) -> i64 error[FileError]

def bad(flag: bool) -> i64:
	return try read_value(flag)
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "try without else requires the current function to return an error union") {
		t.Fatalf("expected bare try propagation diagnostic, got:\n%s", got)
	}
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

// The `return?` / `try? ... default` recovery forms have been removed; rejection is covered by the
// parser tests (TestParseReturnQuestion*, TestParseTryQuestionDefaultRejected).

func TestAnalyzeOptionalMatchWithNullAndPayloadPatterns(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "optional_match_null_payload.elisa", `
enum Expr:
	Int(value: i64)
	Missing

def score(maybe: Expr?) -> i64:
	match maybe:
		null:
			return 0
		Expr.Int(value):
			return value
		_:
			return 2
	return 3
`)
}

func TestAnalyzeOptionalMatchExpressionWithNullAndPayloadPatterns(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "optional_match_expr_null_payload.elisa", `
enum Expr:
	Int(value: i64)
	Missing

def score(maybe: Expr?) -> i64:
	return match maybe:
		null:
			0
		Expr.Int(value):
			value
		_:
			2
`)
}

func TestAnalyzeOptionalElementCanUseGetElse(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "optional_element_get_else.elisa", `def read(xs: darray[u8&?], i: usize) -> u8&:
	val: u8&? = xs[i]
	return get val else ""
`)
}
