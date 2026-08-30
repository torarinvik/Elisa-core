package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestASTSummaryRendersTupleReturnType(t *testing.T) {
	path := t.TempDir() + "/tuple_return.elisa"
	source := "def divmod(a: i64, b: i64) -> (quotient: i64, remainder: i64):\n" +
		"    return a / b, a % b\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "ast", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("-emit ast failed with code %d: %s", code, stderr.String())
	}
	want := "def divmod(2 params) -> (quotient: i64, remainder: i64) (1 stmts)"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("AST summary does not render tuple return type %q:\n%s", want, stdout.String())
	}
}
