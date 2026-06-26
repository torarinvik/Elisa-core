package semantic

import (
	"strings"
	"testing"
)

func TestWhereRefinementTypeErasesToBase(t *testing.T) {
	src := `
def f(n: i64 where true) -> i64:
    return n + 1
`
	result := analyzeTreeTestSource(t, "where_refine_base.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where refinement type should erase to its base and analyze cleanly, got: %v", errs)
	}
}

func TestWhereRefinementDoesNotEnterSameTypeOrAssignableTo(t *testing.T) {
	src := `
def plain(n: i64) -> i64:
    return n

def refined(n: i64 where n >= 0) -> i64 where result >= 0:
    return n
`
	result := analyzeTreeTestSource(t, "where_refine_type_identity.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where refinement should analyze cleanly, got: %v", errs)
	}
	plainSym, ok := result.GlobalScope.Lookup("plain")
	if !ok {
		t.Fatal("expected plain function symbol")
	}
	refinedSym, ok := result.GlobalScope.Lookup("refined")
	if !ok {
		t.Fatal("expected refined function symbol")
	}
	plain, ok := plainSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected plain function type, got %T", plainSym.Type)
	}
	refined, ok := refinedSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected refined function type, got %T", refinedSym.Type)
	}
	if !SameType(plain.Params[0], refined.Params[0]) || !SameType(plain.Return, refined.Return) {
		t.Fatalf("anonymous where refinement must erase before SameType; got %s -> %s vs %s -> %s",
			plain.Params[0], plain.Return, refined.Params[0], refined.Return)
	}
	if !AssignableTo(plain.Params[0], refined.Params[0]) || !AssignableTo(refined.Params[0], plain.Params[0]) {
		t.Fatalf("anonymous where refinement must not introduce directional AssignableTo behavior")
	}
	if !AssignableTo(plain.Return, refined.Return) || !AssignableTo(refined.Return, plain.Return) {
		t.Fatalf("anonymous where return refinement must remain assignable exactly like the base type")
	}
}

func TestWhereRefinementConstantPredicateMustBeBool(t *testing.T) {
	src := `
def f(n: i64 where 1) -> i64:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_refine_nonbool.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "where refinement predicate must be bool") {
		t.Fatalf("non-bool where refinement predicate should be rejected, got:\n%s", allDiagnostics(result))
	}
}

func TestParamWhereRefinementActsAsPrecondition(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def ok() -> i64:
    n: i64 where n > 0 = 4
    return need_positive(n)

def bad() -> i64:
    return need_positive(0)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_param_precondition.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, "where precondition of need_positive is violated") {
		t.Fatalf("bad call should violate param where precondition, got:\n%s", all)
	}
}

func TestReturnWhereRefinementActsAsEnsure(t *testing.T) {
	src := `
def good(n: i64 where n >= 0) -> i64 where result >= n:
    return n + 1

def bad(n: i64 where n >= 0) -> i64 where result > n:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_return_ensure.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, "return where refinement could not be proven statically") {
		t.Fatalf("bad return should produce an unproven return where obligation, got:\n%s", all)
	}
}

func TestLocalWhereRefinementObligation(t *testing.T) {
	src := `
def f() -> i64:
    x: i64 where x > 0 = 0
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_local_obligation.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, "local where refinement is violated") {
		t.Fatalf("local where refinement should be checked at the binding, got:\n%s", all)
	}
}

func TestWhereRefinementRestrictions(t *testing.T) {
	src := `
def later(a: i64 where a < b, b: i64) -> i64:
    return a

def impure(x: i64 where helper(x)) -> i64:
    return x

def helper(x: i64) -> bool:
    return x > 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_restrictions.elisa", src, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, "parameter where refinement may only reference its parameter and earlier parameters") {
		t.Fatalf("later-param reference should be rejected, got:\n%s", all)
	}
	if !strings.Contains(all, "where refinement predicate must be pure and side-effect-free") {
		t.Fatalf("call predicate should be rejected as impure for v1, got:\n%s", all)
	}
}
