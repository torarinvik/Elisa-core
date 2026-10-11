//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestSMTConstantStringContracts(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		reject     bool
	}{
		{"returns", "return \"..=\" if code == 109\n    return \"\"", false},
		{"match", "return match code:\n        109: \"..=\"\n        _: \"\"", false},
		{"wrong_match", "return match code:\n        108: \"..=\"\n        _: \"\"", true},
		{"wrong_return", "return \"\"", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeContractStrict(t, tc.name+".elisa", "def name(code: u64) -> sview:\n    ensure code == 109 implies result == \"..=\"\n    "+tc.body+"\n")
			rejected := strings.Contains(allDiagnostics(result), "could not be proven")
			if rejected != tc.reject {
				t.Fatalf("rejected %v want %v: %v", rejected, tc.reject, result.Errors())
			}
		})
	}
}

func TestSMTConstantStringBindingsAreEnvironmentScoped(t *testing.T) {
	a := &Analyzer{}
	tr := &smtTranslator{a: a}
	first := map[string]string{}
	second := map[string]string{}
	literal := &ast.StringLit{Value: "first"}
	a.bindSMTEnvArg(tr, first, "value", literal, nil, true)
	id := &ast.Ident{Name: "value"}
	if term, ok := tr.constantStringEquality(id, literal, first, 0); !ok || term != "true" {
		t.Fatalf("first binding: %q %v", term, ok)
	}
	if term, ok := tr.constantStringEquality(id, literal, second, 0); ok {
		t.Fatalf("binding leaked: %q", term)
	}
	a.bindSMTEnvArg(tr, second, "value", &ast.StringLit{Value: "second"}, nil, true)
	if term, ok := tr.constantStringEquality(id, literal, second, 0); !ok || term != "false" {
		t.Fatalf("second binding: %q %v", term, ok)
	}
	if term, ok := tr.constantStringEquality(id, literal, first, 0); !ok || term != "true" {
		t.Fatalf("caller changed: %q %v", term, ok)
	}
}

func TestSMTConstantStringComparisonDeclinesSymbolicPair(t *testing.T) {
	a := &Analyzer{}
	tr := &smtTranslator{a: a}
	env := map[string]string{}
	a.bindSMTEnvArg(tr, env, "left", &ast.StringLit{Value: "same"}, nil, true)
	a.bindSMTEnvArg(tr, env, "right", &ast.StringLit{Value: "different"}, nil, true)
	if term, ok := tr.constantStringEquality(&ast.Ident{Name: "left"}, &ast.Ident{Name: "right"}, env, 0); ok {
		t.Fatalf("unsupported two-environment comparison accepted: %q", term)
	}
}

func TestSMTConstantStringFragmentRejectsDynamicPatterns(t *testing.T) {
	for _, pattern := range []*ast.MatchLiteralPattern{
		{Value: &ast.Ident{Name: "runtime"}},
		{Value: &ast.IntLit{}, Pinned: true},
	} {
		match := &ast.MatchExpr{Value: &ast.Ident{Name: "code"}, Arms: []ast.MatchArm{
			{Pattern: pattern, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "yes"}}}},
			{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "no"}}}},
		}}
		if constantStringResult(match, 0) {
			t.Fatal("dynamic/pinned selector accepted")
		}
	}
}

func TestSMTConstantStringFragmentRejectsCallSubject(t *testing.T) {
	match := &ast.MatchExpr{Value: &ast.CallExpr{}, Arms: []ast.MatchArm{
		{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "yes"}}}},
	}}
	if constantStringResult(match, 0) {
		t.Fatal("call subject accepted")
	}
}

func TestSMTConstantStringEqualityDeclinesEmbeddedNUL(t *testing.T) {
	tr := &smtTranslator{}
	prefix := &ast.StringLit{Value: "a"}
	terminated := &ast.StringLit{Value: "a\x00tail"}
	for _, operands := range [][2]ast.Expr{{prefix, terminated}, {terminated, prefix}} {
		if term, ok := tr.constantStringEquality(operands[0], operands[1], nil, 0); ok {
			t.Fatalf("NUL comparison accepted: %q", term)
		}
	}
	if constantStringResult(terminated, 0) {
		t.Fatal("NUL literal binding accepted")
	}
}

func TestSMTConstantStringNestedNamesDoNotProveFalseEquality(t *testing.T) {
	result := analyzeContractStrict(t, "nested_string_names.elisa", `def same(x: sview, y: sview) -> bool:
    return x == y

def forward(x: sview, y: sview) -> bool:
    return same("a", x)

def claim() -> bool:
    ensure result == true
    return forward("b", "a")
`)
	if !strings.Contains(allDiagnostics(result), "could not be proven") {
		t.Fatalf("false nested equality claim was not rejected: %v", result.Errors())
	}
}
