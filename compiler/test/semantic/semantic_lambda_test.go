package semantic_test

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestAnalyzeLambdaContextualTypingCapturesOuterValue(t *testing.T) {
	src := `def apply(fn: func(i64) -> i64, value: i64) -> i64:
    return fn(value)

def run() -> i64:
    offset: i64 = 1
    return apply(lambda value: value + offset, 41)
`

	result, errs := parseAndAnalyze(t, "lambda_contextual_capture.elisa", src)
	requireNoErrors(t, errs)
	decl := requireFuncDecl(t, result, "run")

	ret, ok := decl.Body[1].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[1])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", ret.Value)
	}
	lambda, ok := call.Args[0].(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected lambda argument, got %T", call.Args[0])
	}
	info, ok := result.Lambdas[lambda]
	if !ok || info == nil {
		t.Fatalf("expected lambda capture metadata")
	}
	if len(info.Captures) != 1 || info.Captures[0] != "offset" {
		t.Fatalf("expected lambda to capture offset, got %v", info.Captures)
	}
	fnType, ok := result.ExprTypes[lambda].(*semantic.FuncType)
	if !ok || fnType == nil {
		t.Fatalf("expected lambda function type, got %T", result.ExprTypes[lambda])
	}
	if len(fnType.Params) != 1 || !semantic.SameType(fnType.Params[0], result.NamedTypes["i64"]) {
		t.Fatalf("expected inferred lambda param type i64, got %v", fnType.Params)
	}
	if !semantic.SameType(fnType.Return, result.NamedTypes["i64"]) {
		t.Fatalf("expected inferred lambda return type i64, got %s", fnType.Return.String())
	}
}

func TestAnalyzeBlockLambdaRequiresReturnTypeOrContext(t *testing.T) {
	src := `def run() -> void:
    _ = lambda value:
        return value
`

	_, errs := parseAndAnalyze(t, "lambda_block_context_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatalf("expected block lambda error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "block lambda requires an explicit return type or contextual function type") {
		t.Fatalf("expected block lambda contextual typing diagnostic, got:\n%s", all)
	}
}
