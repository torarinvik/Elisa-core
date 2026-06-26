package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// ── Richer use-site args ─────────────────────────────────────────────────────

// A parametric refine alias whose value arg is a dotted field path (`xs.count`) expands correctly
// so the predicate can reference a field on the argument.
func TestNamedRefineAliasRicherArgFieldPath(t *testing.T) {
	src := `
refine BoundedBy(limit: i64) = i64 where self >= 0 and self < limit

def first_n(xs: darray[i64], n: BoundedBy[xs.count]) -> i64:
    return xs[n]
`
	result := analyzeTreeTestSource(t, "refine_alias_richer_field.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("richer field-path arg should analyze cleanly, got: %v", errs)
	}
	// The binder must have been rewritten in-place to a WhereRefinementTypeExpr.
	decl := findFuncDecl(t, result, "first_n")
	if _, ok := decl.Params[1].Type.(*ast.WhereRefinementTypeExpr); !ok {
		t.Fatalf("expected binder rewritten to WhereRefinementTypeExpr, got %T", decl.Params[1].Type)
	}
}

// A provably-satisfied call via a field-path arg discharges cleanly.
func TestNamedRefineAliasRicherArgCallDischarges(t *testing.T) {
	src := `
refine IndexOfSlice(limit: i64) = i64 where self >= 0 and self < limit

def get_elem(xs: darray[i64], i: IndexOfSlice[xs.count]) -> i64:
    return xs[i]

def caller(xs: darray[i64]) -> i64:
    xs2: darray[i64] = [10, 20, 30]
    return get_elem(xs2, 1)
`
	result := analyzeTreeTestSource(t, "refine_alias_richer_discharge.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("richer-arg alias call should discharge, got: %v", errs)
	}
}

// An arg that cannot be converted to a value expression (e.g. a real generic type instantiation
// like `darray[i64]` used as a value arg) must be rejected with a clear diagnostic.
func TestNamedRefineAliasImpureArgRejected(t *testing.T) {
	src := `
refine BoundedBy(limit: i64) = i64 where self >= 0 and self < limit

def f(xs: darray[i64], n: BoundedBy[darray[i64]]) -> i64:
    return xs[n]
`
	// A real-type arg (not a value expr) must produce a diagnostic.
	result := analyzeTreeTestSourceWithSemanticErrors(t, "refine_alias_impure_arg.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "pure value expression") {
		t.Fatalf("non-value bracket arg must be rejected with 'pure value expression' diagnostic, got: %v", result.Errors())
	}
}

// ── Alias-composing-alias ─────────────────────────────────────────────────────

// An alias whose base names another alias flattens transitively: the composed predicate
// encompasses both the outer and inner constraints.
func TestNamedRefineAliasComposingAlias(t *testing.T) {
	src := `
refine Positive = i64 where self > 0
refine SmallPositive = Positive where self < 100

def needs_small(n: SmallPositive) -> i64:
    return n

def ok_caller() -> i64:
    return needs_small(5)
`
	result := analyzeTreeTestSource(t, "refine_alias_compose.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("alias-composing-alias should analyze cleanly, got: %v", errs)
	}
}

// A violating call to a composed alias (fails the inner base constraint) must be rejected.
func TestNamedRefineAliasComposingAliasViolated(t *testing.T) {
	src := `
refine Positive = i64 where self > 0
refine SmallPositive = Positive where self < 100

def needs_small(n: SmallPositive) -> i64:
    return n

def bad_caller() -> i64:
    return needs_small(-1)
`
	result := analyzeContractStrict(t, "refine_alias_compose_viol.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "is violated") {
		t.Fatalf("violating composed-alias argument must error, got: %v", result.Errors())
	}
}

// A directly recursive alias (self-referential base) must produce a clear cycle error and not loop.
func TestNamedRefineAliasRecursiveCycleDetected(t *testing.T) {
	src := `
refine Cycle = Cycle where self > 0

def f(n: Cycle) -> i64:
    return n
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "refine_alias_cycle.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "recursive") && !strings.Contains(all, "cycle") {
		t.Fatalf("self-recursive alias must produce a recursion/cycle diagnostic, got: %v", result.Errors())
	}
}

// A mutually-recursive pair of aliases must also produce a cycle error.
func TestNamedRefineAliasMutualCycleDetected(t *testing.T) {
	src := `
refine A = B where self > 0
refine B = A where self < 100

def f(n: A) -> i64:
    return n
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "refine_alias_mutual_cycle.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "recursive") && !strings.Contains(all, "cycle") {
		t.Fatalf("mutually-recursive aliases must produce a recursion/cycle diagnostic, got: %v", result.Errors())
	}
}

// Erasure guardrail: even a composed alias must erase to the innermost concrete base type.
func TestNamedRefineAliasComposingAliasErasesToConcreteBase(t *testing.T) {
	src := `
refine Positive = i64 where self > 0
refine SmallPositive = Positive where self < 100

def plain(n: i64) -> i64:
    return n

def composed(n: SmallPositive) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "refine_alias_compose_erase.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("composed alias erasure should analyze cleanly, got: %v", errs)
	}
	plainSym, ok := result.GlobalScope.Lookup("plain")
	if !ok {
		t.Fatal("expected plain symbol")
	}
	composedSym, ok := result.GlobalScope.Lookup("composed")
	if !ok {
		t.Fatal("expected composed symbol")
	}
	plain := plainSym.Type.(*FuncType)
	composed := composedSym.Type.(*FuncType)
	if !SameType(plain.Params[0], composed.Params[0]) {
		t.Fatalf("composed alias must erase to i64 (SameType with plain param); got %s vs %s", plain.Params[0], composed.Params[0])
	}
}
