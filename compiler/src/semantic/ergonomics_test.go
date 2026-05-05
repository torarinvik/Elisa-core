package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeParamPackCallExpansionAndAmbientArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_param_pack_ambient.elisa", `bundle SharedArgs explicit:
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
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_forward_over_ambient.elisa", `def consume(value: i64, width: i64, extra: i64 = 11) -> i64:
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
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_signature_pack.elisa", `bundle Pair explicit:
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

func TestAnalyzeLocalParamPackShadowsGlobalWithinSameBlock(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_local_pack_shadow.elisa", `bundle Pair explicit:
    left: i64 = 1
    right: i64 = 2

def consume(left: i64, right: i64) -> i64:
    return left + right

def build(left: i64) -> i64:
    bundle Pair explicit:
        left: i64 = left
        right: i64 = 9
    return consume(use Pair)
`)
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[len(buildDecl.Body)-1].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 2 {
		t.Fatalf("expected 2 resolved args, got %#v", call.ResolvedArgs)
	}
	if len(call.ParamPacks) != 1 || !call.ParamPacks[0].Bare {
		t.Fatalf("expected bare pack syntax to be preserved, got %#v", call.ParamPacks)
	}
	if ident, ok := call.ResolvedArgs[0].(*ast.Ident); !ok || ident.Name != "left" {
		t.Fatalf("expected local default left arg, got %T %#v", call.ResolvedArgs[0], call.ResolvedArgs[0])
	}
	if lit, ok := call.ResolvedArgs[1].(*ast.IntLit); !ok || lit.Value != "9" {
		t.Fatalf("expected local default right arg, got %T %#v", call.ResolvedArgs[1], call.ResolvedArgs[1])
	}
}

func TestAnalyzeBareArgsScopeParamPackUse(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ergonomics_bare_args_scope_pack.elisa", `bundle Shared explicit:
	value: i64 = 7
	width: i64 = 9

def consume(value: i64, width: i64) -> i64:
    return value + width

def build() -> i64:
	with args(use Shared):
        return consume(use Shared)
`)
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	argsScope := buildDecl.Body[0].(*ast.ArgsScopeStmt)
	if len(argsScope.ParamPacks) != 1 || !argsScope.ParamPacks[0].Bare {
		t.Fatalf("expected bare args-scope pack syntax, got %#v", argsScope.ParamPacks)
	}
	ret := argsScope.Body[len(argsScope.Body)-1].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 2 {
		t.Fatalf("expected 2 resolved args, got %#v", call.ResolvedArgs)
	}
	if lit, ok := call.ResolvedArgs[0].(*ast.IntLit); !ok || lit.Value != "7" {
		t.Fatalf("expected defaulted value arg, got %T %#v", call.ResolvedArgs[0], call.ResolvedArgs[0])
	}
	if lit, ok := call.ResolvedArgs[1].(*ast.IntLit); !ok || lit.Value != "9" {
		t.Fatalf("expected defaulted width arg, got %T %#v", call.ResolvedArgs[1], call.ResolvedArgs[1])
	}
}

func TestAnalyzeRejectsLocalParamPackUseBeforeDeclaration(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ergonomics_local_pack_use_before_decl.elisa", `def consume(left: i64, right: i64) -> i64:
    return left + right

def build(left: i64) -> i64:
    value: i64 = consume(use Pair())
    bundle Pair explicit:
        left: i64 = left
        right: i64 = 9
    return value
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `unknown explicit bundle "Pair"`) {
		t.Fatalf("expected missing local explicit bundle diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsLocalParamPackFromNestedBlock(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ergonomics_local_pack_nested_block.elisa", `def consume(left: i64, right: i64) -> i64:
    return left + right

def build(left: i64) -> i64:
    bundle Pair explicit:
        left: i64 = left
        right: i64 = 9
    if true:
        return consume(use Pair())
    return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `unknown explicit bundle "Pair"`) {
		t.Fatalf("expected nested-block visibility diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsMissingShorthandValue(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ergonomics_missing_shorthand.elisa", `def consume(missing: i64) -> i64:
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
