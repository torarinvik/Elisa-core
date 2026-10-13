package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
	"testing"
)

func TestSMTScalarMatchPostcondition(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		value := "10"
		if wrong {
			value = "11"
		}
		source := `def classify(x: i32) -> i32:
    ensure result == (10 if x == -1 else (20 if x == 2 else 0))
    return match x:
        -1: ` + value + `
        2: 20
        _: 0
`
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scalar_match.elisa", source, ensureDiagOpts)
		failed := strings.Contains(allDiagnostics(result), "ensure postcondition")
		if failed != wrong {
			t.Fatalf("wrong=%v, diagnostics: %s", wrong, allDiagnostics(result))
		}
	}
}

func TestSMTBooleanMatchContractArms(t *testing.T) {
	source := `def classify(x: i32) -> bool:
    ensure match x:
        1: result == true
        _: result == false
    return x == 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "boolean_match.elisa", source, ensureDiagOpts)
	if strings.Contains(allDiagnostics(result), "ensure postcondition") {
		t.Fatal(allDiagnostics(result))
	}
}

func TestSMTMatchLiteralResultDoesNotSkipVoidEnsure(t *testing.T) {
	source := `def invalid(x: sview) -> void:
    ensure match x:
        "result": false
        _: false
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "void_match.elisa", source, ensureDiagOpts)
	if !strings.Contains(allDiagnostics(result), "ensure postcondition") && !strings.Contains(allDiagnostics(result), "refuted") {
		t.Fatal("false void contract was accepted: " + allDiagnostics(result))
	}
}

func TestSMTScalarMatchRefusesBranchCallsWithoutAddingAxioms(t *testing.T) {
	tr := &smtTranslator{}
	match := &ast.MatchExpr{Value: &ast.IntLit{Value: "1"}, Arms: []ast.MatchArm{
		{Pattern: &ast.MatchWildcardPattern{}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "contracted"}}}}},
	}}
	if _, ok := tr.scalarMatchTerm(match, nil, tr.termEnv); ok {
		t.Fatal("branch call accepted without guarded axioms")
	}
	if len(tr.auxDecls) != 0 {
		t.Fatal("declined translation added axioms")
	}
}

func TestSMTMatchResultDependencyRespectsBindings(t *testing.T) {
	matched := &ast.MatchExpr{Value: &ast.Ident{Name: "x"}, Arms: []ast.MatchArm{
		{Pattern: &ast.MatchBindPattern{Name: "result"}, Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "result"}}}},
	}}
	if exprReferencesResult(matched) {
		t.Fatal("arm binder mistaken for implicit result")
	}
	matched.Value = &ast.Ident{Name: "result"}
	if !exprReferencesResult(matched) {
		t.Fatal("arm binder erased scrutinee dependency")
	}
	matched.Value = &ast.Ident{Name: "x"}
	matched.Arms[0].Pattern = &ast.MatchWildcardPattern{}
	matched.Arms[0].Body = []ast.Stmt{&ast.ExprStmt{Expr: &ast.StringLit{Value: "result"}}}
	if exprReferencesResult(matched) {
		t.Fatal("literal text mistaken for implicit result")
	}
}

func TestSMTQuantifierDependencyPreservesOuterName(t *testing.T) {
	formula := &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: &ast.Ident{Name: "result"}, Right: &ast.QuantifierExpr{Vars: []string{"result"}, Body: &ast.Ident{Name: "result"}}}
	if !exprReferencesResult(formula) {
		t.Fatal("quantifier binder erased an outer result read")
	}
	if exprReferencesResult(formula.Right) {
		t.Fatal("bound name mistaken for an outer result read")
	}
}

func TestSMTMatchPayloadResultDoesNotSkipVoidEnsure(t *testing.T) {
	source := `enum MaybeBool:
    Some(value: bool)
    None

def invalid(value: MaybeBool) -> void:
    ensure match value:
        MaybeBool.Some(result): not result
        _: false
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "payload_match.elisa", source, ensureDiagOpts)
	if !strings.Contains(allDiagnostics(result), "ensure postcondition") {
		t.Fatal("void ensure was skipped: " + allDiagnostics(result))
	}
}

func TestSMTMatchLocalResultDoesNotSkipVoidEnsure(t *testing.T) {
	source := `def invalid(x: bool) -> void:
    ensure match x:
        _:
            result: bool = false
            result == true
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_match.elisa", source, ensureDiagOpts)
	if !strings.Contains(allDiagnostics(result), "ensure postcondition") {
		t.Fatal("void ensure was skipped: " + allDiagnostics(result))
	}
}

func TestSMTOptionalBindResultDoesNotSkipVoidEnsure(t *testing.T) {
	source := `def invalid(x: bool?) -> void:
    ensure (result == true if x is result else false)
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "optional_result.elisa", source, ensureDiagOpts)
	if !strings.Contains(allDiagnostics(result), "ensure postcondition") {
		t.Fatal("void ensure was skipped: " + allDiagnostics(result))
	}
}

func TestSMTNamedIntegerMatchConstants(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		value := "10"
		if wrong {
			value = "11"
		}
		source := `module Tags:
    public:
        const ONE: u8 = 1
        const TWO: u8 = 2

def classify(x: u8) -> i32:
    ensure result == (10 if x == Tags::ONE else (20 if x == Tags::TWO else 0))
    return when x:
        Tags::ONE -> ` + value + `
        Tags::TWO -> 20
        _ -> 0
`
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_match.elisa", source, ensureDiagOpts)
		diagnostics := allDiagnostics(result)
		if strings.Contains(diagnostics, "top-level integer match arm") {
			t.Fatal(diagnostics)
		}
		if failed := strings.Contains(diagnostics, "ensure postcondition"); failed != wrong {
			t.Fatalf("wrong=%v: %s", wrong, diagnostics)
		}
	}
}

func TestSMTNamedIntegerPatternRefusesLocalShadow(t *testing.T) {
	source := `const ONE: u8 = 1

def classify(x: u8, ONE: u8) -> i32:
    return match x:
        ONE: 10
        _: 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shadow_match.elisa", source, ensureDiagOpts)
	if !strings.Contains(allDiagnostics(result), "top-level integer match arm") {
		t.Fatal("local shadow treated as constant: " + allDiagnostics(result))
	}
}

func TestSMTNamedIntegerPatternPreservesWidth(t *testing.T) {
	source := `const TOO_LARGE: u16 = 256

def classify(x: u8) -> i32:
    return match x:
        TOO_LARGE: 10
        _: 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "width_match.elisa", source, ensureDiagOpts)
	if !strings.Contains(allDiagnostics(result), "does not fit in u8") {
		t.Fatal("constant narrowed to scrutinee width: " + allDiagnostics(result))
	}
}

func TestSMTNamedIntegerMatchStringResult(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		value := "one"
		if wrong {
			value = "wrong"
		}
		source := `const ONE: u8 = 1

def name(x: u8) -> sview:
    ensure match x:
        ONE: result == "one"
        _: result == "other"
    return match x:
        ONE: "` + value + `"
        _: "other"
`
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_string_match.elisa", source, ensureDiagOpts)
		if failed := strings.Contains(allDiagnostics(result), "ensure postcondition"); failed != wrong {
			t.Fatalf("wrong=%v: %s", wrong, allDiagnostics(result))
		}
	}
}

func TestSMTGhostScalarMatchEquation(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		value := "10"
		if wrong {
			value = "11"
		}
		source := `ghost def spec(x: i32) -> i32:
    return match x:
        1: 10
        _: 0

def actual(x: i32) -> i32:
    ensure result == spec(x)
    return ` + value + ` if x == 1
    return 0
`
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ghost_scalar_match.elisa", source, ensureDiagOpts)
		diagnostics := allDiagnostics(result)
		if strings.Contains(diagnostics, "must be pure and total") || strings.Contains(diagnostics, "ensure postcondition") != wrong {
			t.Fatalf("wrong=%v, diagnostics: %s", wrong, diagnostics)
		}
	}
}
