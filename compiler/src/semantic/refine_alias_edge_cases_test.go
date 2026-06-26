package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// ── Alias used in return position ────────────────────────────────────────────

// A refine alias in a return position must be rewritten to a WhereRefinementTypeExpr
// and the caller's returned value must satisfy the predicate.
func TestNamedRefineAliasReturnPosition(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def return_positive(n: i64) -> Positive:
    return n + 1
`
	result := analyzeTreeTestSource(t, "refine_alias_return.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("refine alias in return position should analyze cleanly, got: %v", errs)
	}
	// The return type in the FuncDecl must have been rewritten to WhereRefinementTypeExpr.
	decl := findFuncDecl(t, result, "return_positive")
	if _, ok := decl.ReturnType.(*ast.WhereRefinementTypeExpr); !ok {
		t.Fatalf("expected return type rewritten to WhereRefinementTypeExpr, got %T", decl.ReturnType)
	}
}

// A return value that violates a refine alias return type must be rejected.
func TestNamedRefineAliasReturnViolated(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def return_positive(n: i64) -> Positive:
    return -1
`
	result := analyzeContractStrict(t, "refine_alias_return_viol.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "is violated") {
		t.Fatalf("violating return value against refine alias must error, got: %v", result.Errors())
	}
}

// ── Alias used in local variable position ────────────────────────────────────

// A refine alias used in a local variable declaration binder must rewrite and
// enforce the constraint on initialization.
func TestNamedRefineAliasLocalBinder(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def f(n: i64) -> i64:
    x: Positive = n + 1
    return x
`
	result := analyzeTreeTestSource(t, "refine_alias_local.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("refine alias in local binder should analyze cleanly, got: %v", errs)
	}
}

// A refine alias in a local binder with a violating initializer must be rejected.
func TestNamedRefineAliasLocalBinderViolated(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def f() -> i64:
    x: Positive = -1
    return x
`
	result := analyzeContractStrict(t, "refine_alias_local_viol.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "is violated") {
		t.Fatalf("violating refine alias local initializer must error, got: %v", result.Errors())
	}
}

// ── Parametric alias with dotted-path value args ─────────────────────────────

// A parametric refine alias with a dotted field-path argument (e.g., `IndexOf[xs.count]`)
// must expand the predicate correctly so `self < xs.count` works as expected.
func TestNamedRefineAliasParametricDottedPath(t *testing.T) {
	src := `
refine IndexOf(limit: i64) = i64 where self >= 0 and self < limit

def get_at(xs: darray[i64], i: IndexOf[xs.count]) -> i64:
    return xs[i]
`
	result := analyzeTreeTestSource(t, "refine_alias_dotted_path.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric alias with dotted-path arg should analyze cleanly, got: %v", errs)
	}
	// Verify the binder was rewritten to WhereRefinementTypeExpr.
	decl := findFuncDecl(t, result, "get_at")
	if _, ok := decl.Params[1].Type.(*ast.WhereRefinementTypeExpr); !ok {
		t.Fatalf("expected parametric alias binder rewritten to WhereRefinementTypeExpr, got %T", decl.Params[1].Type)
	}
}

// A parametric alias with a dotted-path arg must allow the predicate to constrain
// based on that field value.
func TestNamedRefineAliasParametricDottedPathValidation(t *testing.T) {
	src := `
refine InBounds(limit: i64) = i64 where self >= 0 and self < limit

def safe_access(xs: darray[i64], idx: InBounds[xs.count]) -> i64:
    return xs[idx]

def caller() -> i64:
    arr: darray[i64] = [10, 20, 30]
    return safe_access(arr, 2)
`
	result := analyzeTreeTestSource(t, "refine_alias_dotted_validation.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric dotted-path arg validation should succeed, got: %v", errs)
	}
}

// ── Alias-composing-alias chain ──────────────────────────────────────────────

// An alias that composes another alias which itself composes a third (a chain)
// must flatten all predicates and enforce all constraints.
func TestNamedRefineAliasComposingChain(t *testing.T) {
	src := `
refine Positive = i64 where self > 0
refine SmallPositive = Positive where self < 100
refine VerySmall = SmallPositive where self < 50

def needs_very_small(n: VerySmall) -> i64:
    return n

def ok_caller() -> i64:
    return needs_very_small(5)
`
	result := analyzeTreeTestSource(t, "refine_alias_chain.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("alias-composing-alias chain should analyze cleanly, got: %v", errs)
	}
}

// A call to a chained alias with a value that violates an outer constraint must fail.
func TestNamedRefineAliasComposingChainOuterViolation(t *testing.T) {
	src := `
refine Positive = i64 where self > 0
refine SmallPositive = Positive where self < 100
refine VerySmall = SmallPositive where self < 50

def needs_very_small(n: VerySmall) -> i64:
    return n

def bad_caller() -> i64:
    return needs_very_small(200)
`
	result := analyzeContractStrict(t, "refine_alias_chain_outer_viol.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "is violated") {
		t.Fatalf("violating outer constraint in chain must error, got: %v", result.Errors())
	}
}

// A call to a chained alias with a value that violates an inner constraint must fail.
func TestNamedRefineAliasComposingChainInnerViolation(t *testing.T) {
	src := `
refine Positive = i64 where self > 0
refine SmallPositive = Positive where self < 100
refine VerySmall = SmallPositive where self < 50

def needs_very_small(n: VerySmall) -> i64:
    return n

def bad_caller() -> i64:
    return needs_very_small(-1)
`
	result := analyzeContractStrict(t, "refine_alias_chain_inner_viol.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "is violated") {
		t.Fatalf("violating inner constraint in chain must error, got: %v", result.Errors())
	}
}

// ── Recursive cycle detection ────────────────────────────────────────────────

// A directly recursive alias (A = A where ...) must be caught and reported as a cycle.
func TestNamedRefineAliasSelfCycle(t *testing.T) {
	src := `
refine SelfRef = SelfRef where self > 0

def f(n: SelfRef) -> i64:
    return n
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "refine_alias_self_cycle.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "recursive") && !strings.Contains(all, "cycle") {
		t.Fatalf("self-recursive alias must produce a recursion/cycle diagnostic, got: %v", result.Errors())
	}
}

// A longer chain of mutually recursive aliases (A -> B -> C -> A) must be detected.
func TestNamedRefineAliasMutualCycleChain(t *testing.T) {
	src := `
refine A = B where self > 0
refine B = C where self < 100
refine C = A where self >= 0

def f(n: A) -> i64:
    return n
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "refine_alias_mutual_cycle_chain.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "recursive") && !strings.Contains(all, "cycle") {
		t.Fatalf("mutually-recursive alias chain must produce a recursion/cycle diagnostic, got: %v", result.Errors())
	}
}

// ── Misuse diagnostics ───────────────────────────────────────────────────────

// Using a refine alias in a container type parameter position must produce
// a "binder-position-only" diagnostic.
func TestNamedRefineAliasMisuseInContainerParam(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def f() -> i64:
    xs: darray[Positive] = []
    return 0
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "refine_alias_container_param.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "may only be used in a binder position") {
		t.Fatalf("using refine alias in container param must produce binder-position diagnostic, got: %v", result.Errors())
	}
}

// Using a refine alias in a struct field type must produce a binder-position-only diagnostic.
func TestNamedRefineAliasMisuseInStructField(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

struct Point:
    x: Positive
    y: i64
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "refine_alias_struct_field.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "may only be used in a binder position") {
		t.Fatalf("using refine alias in struct field must produce binder-position diagnostic, got: %v", result.Errors())
	}
}

// Using a refine alias as a function call return type position (not in parameter)
// must still work if it's in the result signature, or fail with a clear message otherwise.
// This tests that the diagnostic is sensible for non-binder contexts.
func TestNamedRefineAliasMisuseInBitcast(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def f(x: i64) -> i64:
    y: i64 = x.cast[Positive]
    return y
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "refine_alias_bitcast.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	// Should reject because Positive is not a valid cast target.
	if !strings.Contains(all, "may only be used in a binder position") {
		t.Fatalf("using refine alias in bitcast must produce a diagnostic, got: %v", result.Errors())
	}
}
