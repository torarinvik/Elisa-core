package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeBindingOrPatternRequiresSameBindings(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ergonomics_binding_or_pattern.elisa", `enum Token:
    Ident(value: i64)
    Keyword(width: i64)

enum Expr:
    Leaf(kind: Token)

def score(expr: Expr) -> i64:
    match expr:
        Expr.Leaf(Token.Ident(value) | Token.Keyword(width)):
            return 0
        _:
            return 0
`)
	errs := result.Errors()
	if len(errs) == 0 {
		t.Fatalf("expected semantic error for mismatched or-pattern bindings")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "or-pattern alternatives must bind the same names") {
		t.Fatalf("expected or-pattern binding diagnostic, got %v", errs)
	}
}

func TestAnalyzeStructFieldDefaultsFillMissingNamedFields(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_struct_field_defaults_named.elisa", `struct Accessors:
    read_name_id: i64? = null
    write_name_id: i64? = null
    default_enabled: bool = false
    count: i64 = 7

def build() -> Accessors:
    return Accessors{count: 9}
`)
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[len(buildDecl.Body)-1].(*ast.ReturnStmt)
	lit := ret.Value.(*ast.StructLitExpr)
	if !lit.ResolvedArgsValid || len(lit.ResolvedArgs) != 4 {
		t.Fatalf("expected defaults to resolve all fields, got %#v", lit.ResolvedArgs)
	}
	if _, ok := lit.ResolvedArgs[0].(*ast.NullLit); !ok {
		t.Fatalf("expected read_name_id default null, got %T", lit.ResolvedArgs[0])
	}
	if boolLit, ok := lit.ResolvedArgs[2].(*ast.BoolLit); !ok || boolLit.Value {
		t.Fatalf("expected default_enabled default false, got %T %#v", lit.ResolvedArgs[2], lit.ResolvedArgs[2])
	}
	if intLit, ok := lit.ResolvedArgs[3].(*ast.IntLit); !ok || intLit.Value != "9" {
		t.Fatalf("expected explicit count 9, got %T %#v", lit.ResolvedArgs[3], lit.ResolvedArgs[3])
	}
}

func TestAnalyzeStructFieldDefaultsFillTrailingPositionalFields(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_struct_field_defaults_positional.elisa", `struct Pair:
    left: i64
    right: i64 = 5

def build() -> Pair:
    return Pair{left: 3}
`)
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[len(buildDecl.Body)-1].(*ast.ReturnStmt)
	lit := ret.Value.(*ast.StructLitExpr)
	if !lit.ResolvedArgsValid || len(lit.ResolvedArgs) != 2 {
		t.Fatalf("expected positional default to resolve both fields, got %#v", lit.ResolvedArgs)
	}
	if intLit, ok := lit.ResolvedArgs[1].(*ast.IntLit); !ok || intLit.Value != "5" {
		t.Fatalf("expected right default 5, got %T %#v", lit.ResolvedArgs[1], lit.ResolvedArgs[1])
	}
}

func TestAnalyzeStructFieldDefaultTypeMismatch(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ergonomics_struct_field_default_mismatch.elisa", `struct Bad:
    flag: bool = 7

def build() -> Bad:
    return Bad{}
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `default value for struct field "flag" expects bool, got int`) {
		t.Fatalf("expected struct field default type diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsMissingDestructureField(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ergonomics_missing_destructure_field.elisa", `struct PairRow:
    first: i64
    second: i64

def build(pair: PairRow) -> i64:
    let PairRow{third} = pair
    return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `struct "PairRow" has no field "third"`) {
		t.Fatalf("expected missing destructure field diagnostic, got:\n%s", all)
	}
}
