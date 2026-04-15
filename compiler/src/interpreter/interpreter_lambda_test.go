package interpreter_test

import (
	"testing"

	"llcontext/src/interpreter"
	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

func parseAndAnalyzeInterpreterTest(t *testing.T, filename string, src string) *semantic.Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	result := semantic.Analyze(file)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors:\n%v", errs)
	}
	return result
}

func TestExecuteReturnedLambdaCapturesOuterValue(t *testing.T) {
	src := `def make_adder(offset: i64) -> func(i64) -> i64:
    return lambda value: value + offset

def run() -> i64:
    adder: func(i64) -> i64 = make_adder(2)
    return adder(40)
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_lambda_capture.llcontext", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "42" {
		t.Fatalf("expected lambda capture result 42, got %s", got)
	}
}
