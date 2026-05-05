package backend_test

import (
	"strings"
	"testing"

	"elisacore/src/backend"
)

func TestGenerateLLVMIRLowersBraceStructErgonomics(t *testing.T) {
	src := `struct Row:
    left: int
    right: int
    flag: bool

def run(items: array[Row, 2], row: Row, flag: bool) -> int:
    let {left: base_left, right} = row
    total: mutable int = 0
    for {left, right: item_right, flag: keep} in items if keep:
        total <- total + left + item_right
    built: Row = Row{flag, right, left: base_left}
    next: Row = built{flag, right = total}
    return next.left + next.right
`
	result := parseAndAnalyze(t, "backend_brace_struct_ergonomics.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	ir := functionIR(output, "run")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for run, got:\n%s", output)
	}
	for _, check := range []string{
		"let.field = extractvalue %Row",
		"iter.filter.body",
		"record.update = insertvalue %Row",
		"extractvalue %Row",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected run IR to contain %q, got:\n%s", check, ir)
		}
	}
}

func TestGenerateLLVMIRDefinesBraceStructLiteralGlobal(t *testing.T) {
	src := `struct Row:
    left: int
    right: int
    flag: bool

global default_row: Row = Row{flag: true, right: 2, left: 1}
`
	result := parseAndAnalyze(t, "backend_brace_struct_global.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	for _, check := range []string{
		"%Row = type { i64, i64, i1 }",
		"@default_row = global %Row { i64 1, i64 2, i1",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
