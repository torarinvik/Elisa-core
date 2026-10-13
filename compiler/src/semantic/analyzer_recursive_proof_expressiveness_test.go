package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestGhostRecursiveProofSupportsImmutableLocalsAndSViewIndex(t *testing.T) {
	src := `const SVIEW_FNV1A_PRIME: u64 = 0x100000001b3
ghost def sview_fnv1a_fold_from(value: sview, index: usize, state: u64) -> u64:
    requires value.len > 0 and value.len.usize().i64() == value.len
    requires index <= value.len.usize()
    decreases value.len.usize() - index
    ensure index == value.len.usize() implies result == state
    ensure index < value.len.usize() implies result == sview_fnv1a_fold_from(
        value, index + 1.usize(),
        (state ^ value[index].u8().u64()) * SVIEW_FNV1A_PRIME
    )
    return state if index == value.len.usize()
    byte: u8 = value[index].u8()
    next_state: u64 = (state ^ byte.u64()) * SVIEW_FNV1A_PRIME
    return sview_fnv1a_fold_from(value, index + 1.usize(), next_state)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "stage1_fnv.elisa", src, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	var ghost *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "sview_fnv1a_fold_from" {
			ghost = fn
			break
		}
	}
	if ghost == nil {
		t.Fatal("recursive proof function was not parsed")
	}
	var recursiveEnsureCall *ast.CallExpr
	for _, ensure := range ghost.EnsureValues {
		result.analyzer.walkStaticExpr(ensure, func(expr ast.Expr) bool {
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				return false
			}
			if id, ok := call.Func.(*ast.Ident); ok && id.Name == ghost.Name {
				recursiveEnsureCall = call
				return true
			}
			return false
		})
	}
	if recursiveEnsureCall == nil {
		t.Fatal("recursive call in the conditional ensure was not parsed")
	}
	oldFunc := result.analyzer.currentFuncDecl
	result.analyzer.currentFuncDecl = ghost
	path := result.analyzer.contractCallPath(recursiveEnsureCall)
	result.analyzer.currentFuncDecl = oldFunc
	if path == nil {
		t.Fatal("recursive ensure call must inherit its implication guard")
	}
	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected errors (call path %s): %s", optimizationExprString(path), strings.Join(result.Errors(), "\n"))
	}
	if result.SMTProfile.Proven == 0 {
		t.Fatal("expected the recursive measure to be discharged by SMT")
	}
}

func TestContractCallPathMatchRequiresEquivalentCallArguments(t *testing.T) {
	left := &ast.CallExpr{Position: pos(), Func: identExpr("f"), Args: []ast.Expr{identExpr("x")}}
	clone := &ast.CallExpr{Position: pos(), Func: identExpr("f"), Args: []ast.Expr{identExpr("x")}}
	if !contractCallsMatch(left, clone) {
		t.Fatal("cloned call with the same source expression should match")
	}
	rewritten := &ast.CallExpr{Position: pos(), Func: identExpr("f"), Args: []ast.Expr{identExpr("y")}}
	if contractCallsMatch(left, rewritten) {
		t.Fatal("same-position call with different arguments must not inherit the original call path")
	}
}

func TestSMTRelevantSubstDeclinesMissingReferencedFormal(t *testing.T) {
	analyzer := &Analyzer{}
	translator := analyzer.newSMTTranslator(nil)
	_, ok := analyzer.smtEnvForRelevantSubst(
		translator,
		map[string]ast.Expr{},
		identExpr("index"),
		[]ast.ParamDecl{{Name: "index"}},
	)
	if ok {
		t.Fatal("missing actual for a referenced formal must not leave it free in the caller's namespace")
	}
}
