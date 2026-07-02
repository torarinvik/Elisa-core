package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// The range stride is spelled on the range itself: `for i in lo..<hi..N:`. The
// `step N` spelling was never syntax — the for-header slice sub-parse silently
// TRUNCATED it, so `for i in 0..<10 step 2:` compiled and stepped by 1 (a silent
// wrong-behavior hazard, found live in corpus code). It now gets a directed
// diagnostic, and recovery adopts N as the stride so the AST carries the
// author's intent (no cascade).
func TestParseRangeForStepKeywordRejected(t *testing.T) {
	file, errs := parseSourceFile(t, "def f() -> void:\n    for i in 0..<10 step 2:\n        pass\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "`step N` is not range syntax; spell the stride on the range: `lo..<hi..N`") {
		t.Fatalf("expected exactly the step removal diagnostic, got: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	forStmt, ok := decl.Body[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected range for stmt, got %T", decl.Body[0])
	}
	if forStmt.Step == nil {
		t.Fatal("expected recovery to adopt the step expression as the range stride")
	}
}

// The canonical range stride parses clean.
func TestParseRangeForDotDotStride(t *testing.T) {
	file, errs := parseSourceFile(t, "def f() -> void:\n    for i in 0..<10..2:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	forStmt, ok := decl.Body[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected range for stmt, got %T", decl.Body[0])
	}
	if forStmt.Step == nil {
		t.Fatal("expected the ..2 stride to populate ForStmt.Step")
	}
}

// A source collection named `step` is untouched (`for x in step:`).
func TestParseForSourceNamedStep(t *testing.T) {
	_, errs := parseSourceFile(t, "def f(step: darray[int]) -> void:\n    for x in step:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("a collection named step must still parse, got: %v", errs)
	}
}
