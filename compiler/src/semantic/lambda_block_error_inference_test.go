package semantic

import (
	"strings"
	"testing"
)

// TestLambdaValueAnnotationRaiseInfersErrorSet: a lambda with a value-only
// return annotation (-> i64) whose body raises should infer its error set,
// upgrading the return to i64 error[IoErr]. Callers using [errorset R]
// combinators then bind R to the inferred set.
func TestLambdaValueAnnotationRaiseInfersErrorSet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "val_annot_raise.elisa", `
error IoErr:
    Bad

def applyDouble[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    return try f()

def use() -> i64 error[IoErr]:
    catch applyDouble(fn () -> i64 => raise IoErr.Bad):
        v:
            return v
        IoErr.Bad:
            return 99
`)
	if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
		t.Fatalf("lambda with value-only annotation + raise should infer error set, got:\n%s", all)
	}
}

// TestLambdaValueAnnotationTryInfersErrorSet: same but with try in the body.
func TestLambdaValueAnnotationTryInfersErrorSet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "val_annot_try.elisa", `
error IoErr:
    Bad

def applyDouble[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    return try f()

def seven() -> i64 error[IoErr]:
    return 7

def use() -> i64 error[IoErr]:
    return applyDouble(fn () -> i64 => try seven())
`)
	if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
		t.Fatalf("lambda with value-only annotation + try should infer error set, got:\n%s", all)
	}
}

// TestLambdaValueAnnotationRaiseWrongSetRejected: inferred set must be
// compatible with the concrete callee expectation.
func TestLambdaValueAnnotationRaiseWrongSetRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "val_annot_mismatch.elisa", `
error IoErr:
    Bad

error NetErr:
    Down

def wantsIo(f: func() -> i64 error[IoErr]) -> i64 error[IoErr]:
    return try f()

def use() -> i64 error[IoErr]:
    return wantsIo(fn () -> i64 => raise NetErr.Down)
`)
	if all := allDiagnostics(result); strings.TrimSpace(all) == "" {
		t.Fatalf("lambda raising NetErr should not satisfy func -> i64 error[IoErr]")
	}
}
