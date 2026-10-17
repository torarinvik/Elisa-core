//go:build cgo

package semantic

import (
	"fmt"
	"testing"

	"elisacore/src/ast"
)

func TestSMTViewLiteralArgumentLength(t *testing.T) {
	for _, tc := range []struct {
		literal string
		length  int
	}{
		{`""`, 0}, {`"abc"`, 3}, {`"é😺"`, 6}, {`"a\0b"`, 3},
	} {
		t.Run(tc.literal, func(t *testing.T) {
			source := fmt.Sprintf(`def length(view: sview) -> usize:
    ensure result == view.len
    return view.len

def literal_length() -> usize:
    ensure result == %d
    return length(%s)
`, tc.length, tc.literal)
			result := analyzeContractStrict(t, "view_literal_length.elisa", source)
			if len(result.Errors()) != 0 {
				t.Fatal(result.Errors())
			}
		})
	}
}

func TestSMTViewLiteralArgumentLengthRejectsWrongResult(t *testing.T) {
	result := analyzeContractStrict(t, "wrong_literal_length.elisa", `def length(view: sview) -> usize:
    ensure result == view.len
    return view.len

def wrong() -> usize:
    ensure result == 1
    return length("abc")
`)
	if ensureFailuresFor(result.Errors(), "wrong") == 0 {
		t.Fatalf("wrong literal length proved: %v", result.Errors())
	}
}

func TestSMTViewLiteralLengthBindingDoesNotExposeByteArray(t *testing.T) {
	literal := &ast.StringLit{Value: "abc"}
	a := &Analyzer{exprTypes: map[ast.Expr]Type{literal: &SViewType{}}}
	tr := a.newSMTTranslator(nil)
	env := map[string]string{}
	if !tr.bindSMTSViewLengthArg(env, "view", literal, nil) {
		t.Fatal("literal header must bind")
	}
	tr.calleeBodyDepth = 1
	object := &ast.Ident{Name: "view"}
	length, ok := tr.termEnv(&ast.FieldExpr{Object: object, Field: "len"}, env)
	if !ok || length != "3" {
		t.Fatalf("expected exact length 3, got %q, %v", length, ok)
	}
	if _, ok := tr.arrayTermEnv(object, env); ok {
		t.Fatal("length-only binding exposed a byte array")
	}
}

func TestSMTViewLiteralArgumentLengthDoesNotReadCallerProjection(t *testing.T) {
	for _, expected := range []int{3, 99} {
		source := fmt.Sprintf(`def length(view: sview) -> usize:
    ensure result == view.len
    return view.len

def shadowed() -> usize:
    ensure result == %d
    view: (len: usize, other: usize) = (len: 99.usize(), other: 0.usize())
    return length("abc")
`, expected)
		r := analyzeContractStrict(t, "literal_caller_projection.elisa", source)
		if rejected := ensureFailuresFor(r.Errors(), "shadowed") != 0; rejected != (expected != 3) {
			t.Fatalf("expected %d: %s", expected, allDiagnostics(r))
		}
	}
}
