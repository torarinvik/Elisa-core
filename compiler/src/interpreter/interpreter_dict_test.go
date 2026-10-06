package interpreter_test

import (
	"fmt"
	"strings"
	"testing"

	"elisacore/src/interpreter"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

// `d[k]` is an optional reference (stage1 parity): a missing key is absent, never a trap.
func TestExecuteDictIndexMissingKeyIsAbsent(t *testing.T) {
	src := `def run() -> i64:
    values: mutable dict[i64, i64] = {1: 40, 7: 3}
    a: i64 = get values[2] else 2
    b: i64 = get values[7] else 100
    if values[9] is missing:
        return 999
    if values[1] is found:
        return found + a + b
    return 0
`
	result := parseAndAnalyzeInterpreterTest(t, "interpreter_dict_index_optional.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "45" {
		t.Fatalf("expected 45, got %s", got)
	}
}

// Binding the lookup straight to the payload type is rejected with stage1's wording.
func TestAnalyzeRejectsDictIndexAsPlainValue(t *testing.T) {
	src := `def run() -> i64:
    values: mutable dict[i64, i64] = {1: 42}
    value: i64 = values[1]
    return value
`
	l := lexer.New("dict_index_value.elisa", []byte(src))
	tokens := l.Tokenize()
	p := parser.New(tokens)
	file := p.ParseFile("dict_index_value.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	errs := semantic.Analyze(file).Errors()
	want := `variable "value" expects i64, got optional reference to dictionary value`
	if !strings.Contains(fmt.Sprint(errs), want) {
		t.Fatalf("expected %q, got %v", want, errs)
	}
}
