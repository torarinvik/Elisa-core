package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
)

func TestAnalyzeParamPackCallExpansionAndAmbientArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_param_pack_ambient.llcontext", `params SharedArgs:
    value: i64
    width: i64 = 5

def consume(value: i64, width: i64 = 9, extra: i64 = 11) -> i64:
    return value + width + extra

def build(value: i64, extra: i64) -> i64:
    with args(use SharedArgs(value:), extra:):
        return consume(use SharedArgs(), width: 7)
`)
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	argsScope := buildDecl.Body[0].(*ast.ArgsScopeStmt)
	ret := argsScope.Body[len(argsScope.Body)-1].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 3 {
		t.Fatalf("expected 3 resolved args, got %#v", call.ResolvedArgs)
	}
	if ident, ok := call.ResolvedArgs[0].(*ast.Ident); !ok || ident.Name != "value" {
		t.Fatalf("expected ambient value arg, got %T %#v", call.ResolvedArgs[0], call.ResolvedArgs[0])
	}
	if lit, ok := call.ResolvedArgs[1].(*ast.IntLit); !ok || lit.Value != "7" {
		t.Fatalf("expected explicit named arg to override pack default, got %T %#v", call.ResolvedArgs[1], call.ResolvedArgs[1])
	}
	if ident, ok := call.ResolvedArgs[2].(*ast.Ident); !ok || ident.Name != "extra" {
		t.Fatalf("expected ambient extra arg, got %T %#v", call.ResolvedArgs[2], call.ResolvedArgs[2])
	}
}

func TestAnalyzeArgForwardOverridesAmbientArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_forward_over_ambient.llcontext", `def consume(value: i64, width: i64, extra: i64 = 11) -> i64:
    return value + width + extra

def build(value: i64, width: i64, extra: i64) -> i64:
    with args(width: 99, extra: 77):
        return consume(..)
`)
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	argsScope := buildDecl.Body[0].(*ast.ArgsScopeStmt)
	ret := argsScope.Body[len(argsScope.Body)-1].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 3 {
		t.Fatalf("expected 3 resolved args, got %#v", call.ResolvedArgs)
	}
	for i, want := range []string{"value", "width", "extra"} {
		if ident, ok := call.ResolvedArgs[i].(*ast.Ident); !ok || ident.Name != want {
			t.Fatalf("expected forwarded arg %q at %d, got %T %#v", want, i, call.ResolvedArgs[i], call.ResolvedArgs[i])
		}
	}
}

func TestAnalyzeSignatureParamPackExpandsIntoFunctionBody(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_signature_pack.llcontext", `params Pair:
    left: i64
    right: i64 = 7

def add(use Pair) -> i64:
    return left + right

def build(left: i64) -> i64:
    return add(use Pair(left:))
`)
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 2 {
		t.Fatalf("expected 2 resolved args, got %#v", call.ResolvedArgs)
	}
	if ident, ok := call.ResolvedArgs[0].(*ast.Ident); !ok || ident.Name != "left" {
		t.Fatalf("expected shorthand left arg, got %T %#v", call.ResolvedArgs[0], call.ResolvedArgs[0])
	}
	if lit, ok := call.ResolvedArgs[1].(*ast.IntLit); !ok || lit.Value != "7" {
		t.Fatalf("expected pack default right arg, got %T %#v", call.ResolvedArgs[1], call.ResolvedArgs[1])
	}
}

func TestAnalyzeRejectsMissingShorthandValue(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ergonomics_missing_shorthand.llcontext", `def consume(missing: i64) -> i64:
    return missing

def build() -> i64:
    return consume(missing:)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `no value named "missing" for shorthand argument`) {
		t.Fatalf("expected targeted shorthand diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsMissingDestructureField(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ergonomics_missing_destructure_field.llcontext", `struct PairRow:
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
