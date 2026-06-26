//go:build cgo

package semantic

import "testing"

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
