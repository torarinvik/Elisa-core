//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestSMTBooleanStringMatchContract(t *testing.T) {
	for _, tc := range []struct {
		name, arms string
		reject     bool
	}{
		{"correct", "\"load\" | \"store\" -> true\n        _ -> false", false},
		{"wrong_literal", "\"load\" | \"other\" -> true\n        _ -> false", true},
		{"wrong_default", "\"load\" | \"store\" -> true\n        _ -> true", true},
		{"wrong_value", "\"load\" | \"store\" -> false\n        _ -> false", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeContractStrict(t, tc.name+".elisa", "def classified(name: sview) -> bool:\n    ensure result == (name == \"load\" or name == \"store\")\n    return when name:\n        "+tc.arms+"\n")
			rejected := strings.Contains(allDiagnostics(result), "could not be proven")
			if rejected != tc.reject {
				t.Fatalf("rejected %v want %v: %v", rejected, tc.reject, result.Errors())
			}
		})
	}
}

func TestSMTBooleanStringMatchPreservesArmPriority(t *testing.T) {
	selector := &ast.Ident{Name: "name"}
	match := &ast.MatchExpr{Value: selector, Arms: []ast.MatchArm{
		{Pattern: &ast.MatchStringLiteralPattern{Value: "load"}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BoolLit{Value: false}}}},
		{Pattern: &ast.MatchStringLiteralPattern{Value: "load"}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BoolLit{Value: true}}}},
		{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BoolLit{Value: false}}}},
	}}
	function := &ast.FuncDecl{
		Name:   "classified",
		Params: []ast.ParamDecl{{Name: "name"}},
		Body:   []ast.Stmt{&ast.ReturnStmt{Value: match}},
	}
	analyzer := &Analyzer{currentFuncDecl: function, currentScope: NewScope(nil)}
	analyzer.currentScope.Define(&Symbol{Name: "name", Kind: SymbolParam, Type: &SViewType{}, Node: function, ParamIndex: 0})
	translator := &smtTranslator{a: analyzer, boolDecls: map[string]bool{}}

	term, ok := translator.booleanMatchTerm(match, nil)
	want := "(ite str_eq_6e616d65_6c6f6164 false (ite str_eq_6e616d65_6c6f6164 true false))"
	if !ok || term != want {
		t.Fatalf("match term = %q, %v; want %q", term, ok, want)
	}
}

func TestSMTClassifierStringEqualityDeclinesUnsupportedBindings(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutable    bool
		paramBound bool
		env        map[string]string
		left       ast.Expr
		right      ast.Expr
	}{
		{name: "mutable_parameter", mutable: true, paramBound: true, left: &ast.Ident{Name: "name"}, right: &ast.StringLit{Value: "load"}},
		{name: "local_binding", left: &ast.Ident{Name: "name"}, right: &ast.StringLit{Value: "load"}},
		{name: "symbolic_pair", paramBound: true, left: &ast.Ident{Name: "name"}, right: &ast.Ident{Name: "other"}},
		{name: "substituted_parameter", paramBound: true, env: map[string]string{"name": "v_other"}, left: &ast.Ident{Name: "name"}, right: &ast.StringLit{Value: "load"}},
		{name: "embedded_nul", paramBound: true, left: &ast.Ident{Name: "name"}, right: &ast.StringLit{Value: "load\x00tail"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			function := &ast.FuncDecl{Name: "classified", Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.BoolLit{Value: true}}}}
			analyzer := &Analyzer{currentFuncDecl: function, currentScope: NewScope(nil)}
			if tc.paramBound {
				function.Params = []ast.ParamDecl{{Name: "name", Mutable: tc.mutable}}
				analyzer.currentScope.Define(&Symbol{Name: "name", Kind: SymbolParam, Type: &SViewType{}, Node: function, ParamIndex: 0, Mutable: tc.mutable})
			} else {
				analyzer.currentScope.Define(&Symbol{Name: "name", Kind: SymbolLocal, Type: &SViewType{}})
			}
			translator := &smtTranslator{a: analyzer, boolDecls: map[string]bool{}}
			if term, ok := translator.classifierStringEquality(tc.left, tc.right, tc.env); ok {
				t.Fatalf("unsupported comparison translated as %q", term)
			}
			if len(translator.boolDecls) != 0 {
				t.Fatalf("declined comparison left SMT declarations: %v", translator.boolDecls)
			}
		})
	}
}

func TestSMTClassifierStringEqualityDeclinesEffectfulContexts(t *testing.T) {
	for _, tc := range []struct {
		name            string
		body            []ast.Stmt
		calleeBodyDepth int
	}{
		{
			name: "call_in_body",
			body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "mutate"}}}},
		},
		{
			name: "can_handler",
			body: []ast.Stmt{&ast.CanStmt{
				HandlerName: "Recover",
				Body:        []ast.Stmt{&ast.ReturnStmt{Value: &ast.BoolLit{Value: true}}},
			}},
		},
		{
			name:            "callee_body",
			body:            []ast.Stmt{&ast.ReturnStmt{Value: &ast.BoolLit{Value: true}}},
			calleeBodyDepth: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			function := &ast.FuncDecl{
				Name:   "classified",
				Params: []ast.ParamDecl{{Name: "name"}},
				Body:   tc.body,
			}
			analyzer := &Analyzer{currentFuncDecl: function, currentScope: NewScope(nil)}
			analyzer.currentScope.Define(&Symbol{Name: "name", Kind: SymbolParam, Type: &SViewType{}, Node: function, ParamIndex: 0})
			translator := &smtTranslator{a: analyzer, boolDecls: map[string]bool{}, calleeBodyDepth: tc.calleeBodyDepth}
			if term, ok := translator.classifierStringEquality(&ast.Ident{Name: "name"}, &ast.StringLit{Value: "load"}, nil); ok {
				t.Fatalf("effectful context translated as %q", term)
			}
			if len(translator.boolDecls) != 0 {
				t.Fatalf("declined comparison left SMT declarations: %v", translator.boolDecls)
			}
		})
	}
}

func TestSMTBooleanStringMatchDeclinesGuardsAndNonliteralArms(t *testing.T) {
	for _, tc := range []struct {
		name string
		arm  ast.MatchArm
	}{
		{
			name: "guarded_arm",
			arm: ast.MatchArm{
				Pattern: &ast.MatchStringLiteralPattern{Value: "load"},
				Guard:   &ast.BoolLit{Value: true},
				Body:    []ast.Stmt{&ast.ExprStmt{Expr: &ast.BoolLit{Value: true}}},
			},
		},
		{
			name: "call_arm_value",
			arm: ast.MatchArm{
				Pattern: &ast.MatchStringLiteralPattern{Value: "load"},
				Body:    []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "classify"}}}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := &ast.Ident{Name: "name"}
			match := &ast.MatchExpr{Value: selector, Arms: []ast.MatchArm{
				tc.arm,
				{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BoolLit{Value: false}}}},
			}}
			function := &ast.FuncDecl{
				Name:   "classified",
				Params: []ast.ParamDecl{{Name: "name"}},
				Body:   []ast.Stmt{&ast.ReturnStmt{Value: &ast.BoolLit{Value: true}}},
			}
			analyzer := &Analyzer{currentFuncDecl: function, currentScope: NewScope(nil)}
			analyzer.currentScope.Define(&Symbol{Name: "name", Kind: SymbolParam, Type: &SViewType{}, Node: function, ParamIndex: 0})
			translator := &smtTranslator{a: analyzer, boolDecls: map[string]bool{}}
			if term, ok := translator.booleanMatchTerm(match, nil); ok {
				t.Fatalf("unsupported match translated as %q", term)
			}
			if len(translator.boolDecls) != 0 {
				t.Fatalf("declined match left SMT declarations: %v", translator.boolDecls)
			}
		})
	}
}
