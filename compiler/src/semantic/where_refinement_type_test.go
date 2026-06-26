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
	if !strings.Contains(all, "return where refinement") || !strings.Contains(all, "could not be proven statically") {
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

// TestWhereParamPopulatesSpecSignature verifies that a `T where <bool>` annotation on a parameter
// produces a non-empty ParamPredicates entry in the canonical SpecSignature.
func TestWhereParamPopulatesSpecSignature(t *testing.T) {
	src := `
def f(n: i64 where true) -> i64:
    return n + 1
`
	result := analyzeTreeTestSource(t, "where_spec_param.elisa", src)
	sym, ok := result.GlobalScope.Lookup("f")
	if !ok || sym == nil {
		t.Fatal("symbol 'f' not found in global scope")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || ft == nil {
		t.Fatal("symbol 'f' is not a FuncType")
	}
	sig := ft.SpecSignature
	if sig == nil {
		t.Fatal("SpecSignature is nil for function with where param refinement")
	}
	if len(sig.ParamPredicates) == 0 {
		t.Fatal("expected non-empty ParamPredicates for 'where' param refinement, got empty")
	}
	pred := sig.ParamPredicates[0]
	if pred.Subject.Kind != SpecBinderParam || pred.Subject.Position != 0 {
		t.Fatalf("expected predicate subject to be param 0, got %+v", pred.Subject)
	}
	if pred.SourceExpr == nil {
		t.Fatal("expected SourceExpr to be set for where predicate, got nil")
	}
}

// TestWhereReturnPopulatesSpecSignature verifies that a `T where <bool>` annotation on a return
// type produces a non-empty ResultPredicates entry in the canonical SpecSignature.
func TestWhereReturnPopulatesSpecSignature(t *testing.T) {
	src := `
def g() -> i64 where true:
    return 1
`
	result := analyzeTreeTestSource(t, "where_spec_return.elisa", src)
	sym, ok := result.GlobalScope.Lookup("g")
	if !ok || sym == nil {
		t.Fatal("symbol 'g' not found in global scope")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || ft == nil {
		t.Fatal("symbol 'g' is not a FuncType")
	}
	sig := ft.SpecSignature
	if sig == nil {
		t.Fatal("SpecSignature is nil for function with where return refinement")
	}
	if len(sig.ResultPredicates) == 0 {
		t.Fatal("expected non-empty ResultPredicates for 'where' return refinement, got empty")
	}
	pred := sig.ResultPredicates[0]
	if pred.Subject.Kind != SpecBinderResult {
		t.Fatalf("expected predicate subject to be result binder, got %+v", pred.Subject)
	}
	if pred.SourceExpr == nil {
		t.Fatal("expected SourceExpr to be set for where return predicate, got nil")
	}
}

// TestWhereSpecSignatureClone verifies that cloneSpecSignature preserves where predicates.
func TestWhereSpecSignatureClone(t *testing.T) {
	src := `
def h(n: i64 where true) -> i64 where true:
    return n
`
	result := analyzeTreeTestSource(t, "where_spec_clone.elisa", src)
	sym, ok := result.GlobalScope.Lookup("h")
	if !ok || sym == nil {
		t.Fatal("symbol 'h' not found in global scope")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || ft == nil {
		t.Fatal("symbol 'h' is not a FuncType")
	}
	sig := ft.SpecSignature
	if sig == nil {
		t.Fatal("SpecSignature is nil")
	}
	cloned := cloneSpecSignature(sig)
	if len(cloned.ParamPredicates) != len(sig.ParamPredicates) {
		t.Fatalf("clone lost param predicates: got %d, want %d", len(cloned.ParamPredicates), len(sig.ParamPredicates))
	}
	if len(cloned.ResultPredicates) != len(sig.ResultPredicates) {
		t.Fatalf("clone lost result predicates: got %d, want %d", len(cloned.ResultPredicates), len(sig.ResultPredicates))
	}
	if cloned.ParamPredicates[0].SourceExpr == nil {
		t.Fatal("cloned param predicate has nil SourceExpr")
	}
	if cloned.ResultPredicates[0].SourceExpr == nil {
		t.Fatal("cloned result predicate has nil SourceExpr")
	}
}

func TestWhereRefinementFactSeedingPositive(t *testing.T) {
	src := `
def f(n: i64) -> i64:
    x: i64 where n > 0 = n
    return x
`
	result := analyzeTreeTestSource(t, "where_fact_seed_positive.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where binding with no mutation should analyze cleanly, got: %v", errs)
	}
}

// TestWhereRefinementParamFactSeeded verifies that a param-level where predicate is seeded on
// function entry so the body can treat it as an assumed hypothesis. The predicate is a plain bool
// constraint; analyzing it with no errors is the observable.
func TestWhereRefinementParamFactSeeded(t *testing.T) {
	src := `
def f(n: i64 where n > 0) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "where_param_fact_seed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("param where predicate should seed cleanly and allow clean analysis, got: %v", errs)
	}
}

// TestWhereRefinementMutableParamAnalyzesClean verifies that a function with a mutable where-typed
// param analyzes cleanly. Mutable params skip fact seeding (sound: a later mutation could break the
// predicate), but the function body still compiles without errors.
func TestWhereRefinementMutableParamAnalyzesClean(t *testing.T) {
	src := `
def f(n: mutable i64 where n > 0) -> i64:
    n <- 5
    return n
`
	result := analyzeTreeTestSource(t, "where_mutable_param.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("mutable where param should analyze cleanly, got: %v", errs)
	}
}

// TestWhereRefinementDischargeRoutesThroughProver verifies that the dischargeWhereRefinement helper
// correctly classifies a statically-trivial predicate (true) as proven. This tests that the shared
// proof ladder (proveRequiresClause) is wired up and returns proven for a trivially-true goal.
func TestWhereRefinementDischargeRoutesThroughProver(t *testing.T) {
	src := `
def f(n: i64) -> i64:
    x: i64 where true = n
    return x
`
	result := analyzeTreeTestSource(t, "where_discharge_trivial.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where binding with trivially-true predicate should analyze cleanly, got: %v", errs)
	}
}

// TestLocalWhereDischargeStaticProvable verifies that a local where obligation whose predicate is
// statically provable via the linear prover (constant 5 > 0) is discharged without any diagnostic.
// This specifically exercises the new dischargeWhereRefinement wiring in dischargeLocalWhereRefinement.
func TestLocalWhereDischargeStaticProvable(t *testing.T) {
	src := `
def f() -> i64:
    x: i64 where x > 0 = 5
    return x
`
	result := analyzeTreeTestSource(t, "where_local_static_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("statically provable where binding should be clean, got: %v", errs)
	}
}

// TestLocalWhereDischargeRefutedErrors verifies that a local where obligation whose predicate is
// statically REFUTED (constant -1 > 0 is false) produces a hard error, not a runtime-check lint.
// The refuted path in dischargeWhereRefinement must not silently downgrade to a runtime check.
func TestLocalWhereDischargeRefutedErrors(t *testing.T) {
	src := `
def f() -> i64:
    x: i64 where x > 0 = -1
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_local_refuted.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, "is violated") {
		t.Fatalf("refuted local where refinement should produce 'is violated' error, got:\n%s", all)
	}
}

// TestLocalWhereDischargeUnknownEmitsRuntimeCheck verifies that a local where obligation that
// cannot be statically proven or refuted (predicate depends on an opaque function call) emits a
// proof-lint diagnostic indicating a runtime check, not a hard error.
func TestLocalWhereDischargeUnknownEmitsRuntimeCheck(t *testing.T) {
	src := `
def opaque() -> i64:
    return 1

def f() -> i64:
    x: i64 where x > 0 = opaque()
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_local_unknown.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	all := allDiagnostics(result)
	// Should emit a proof-lint (runtime check), not a hard "is violated" error.
	if strings.Contains(all, "is violated") {
		t.Fatalf("unknown local where should not error as violated, got:\n%s", all)
	}
	if !strings.Contains(all, "could not be proven statically") {
		t.Fatalf("unknown local where should emit runtime-check diagnostic, got:\n%s", all)
	}
}
