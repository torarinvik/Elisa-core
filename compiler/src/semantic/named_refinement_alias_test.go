package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// A named refinement alias used in a parameter binder discharges like the equivalent anonymous where:
// a provably-satisfying call argument analyzes cleanly.
func TestNamedRefineAliasParamSatisfied(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def needs_positive(n: Positive) -> i64:
    return n

def caller() -> i64:
    return needs_positive(5)
`
	result := analyzeTreeTestSource(t, "refine_alias_sat.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a satisfied refine-alias precondition should analyze cleanly, got: %v", errs)
	}
}

// A provably-violating argument to a refine-alias parameter is rejected, exactly like an anonymous
// `where self > 0` would be.
func TestNamedRefineAliasParamViolated(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def needs_positive(n: Positive) -> i64:
    return n

def caller() -> i64:
    return needs_positive(0)
`
	result := analyzeContractStrict(t, "refine_alias_viol.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "is violated") {
		t.Fatalf("a violating refine-alias argument must error, got: %v", result.Errors())
	}
}

// The refine alias is REPRESENTATION-ERASED: a parameter typed by the alias has exactly the alias's
// base type, identical (by SameType / AssignableTo) to a plainly-typed parameter.
func TestNamedRefineAliasErasesToBase(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def plain(n: i64) -> i64:
    return n

def refined(n: Positive) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "refine_alias_erase.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("refine alias should erase and analyze cleanly, got: %v", errs)
	}
	plainSym, ok := result.GlobalScope.Lookup("plain")
	if !ok {
		t.Fatal("expected plain symbol")
	}
	refinedSym, ok := result.GlobalScope.Lookup("refined")
	if !ok {
		t.Fatal("expected refined symbol")
	}
	plain := plainSym.Type.(*FuncType)
	refined := refinedSym.Type.(*FuncType)
	if !SameType(plain.Params[0], refined.Params[0]) {
		t.Fatalf("refine alias must erase before SameType; got %s vs %s", plain.Params[0], refined.Params[0])
	}
	if !AssignableTo(plain.Params[0], refined.Params[0]) || !AssignableTo(refined.Params[0], plain.Params[0]) {
		t.Fatalf("refine alias must not introduce directional AssignableTo behavior")
	}
}

// A parametric refine alias used in a parameter binder substitutes its use-site value argument into
// the predicate (`IndexOf[xs]` → `self >= 0 and self < xs.count`).
func TestNamedRefineAliasParametricBinder(t *testing.T) {
	src := `
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get(xs: darray[i64], i: IndexOf[xs]) -> i64:
    return xs[i]
`
	result := analyzeTreeTestSource(t, "refine_alias_parametric.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric refine alias should analyze cleanly, got: %v", errs)
	}
	getSym, ok := result.GlobalScope.Lookup("get")
	if !ok {
		t.Fatal("expected get symbol")
	}
	ft := getSym.Type.(*FuncType)
	// The `i` parameter must have erased to plain i64.
	if !IsNumericType(ft.Params[1]) {
		t.Fatalf("expected the IndexOf-aliased parameter to erase to a numeric base, got %s", ft.Params[1])
	}
	// And the FuncDecl param type must have been rewritten in place to a WhereRefinementTypeExpr.
	getDecl := findFuncDecl(t, result, "get")
	if _, ok := getDecl.Params[1].Type.(*ast.WhereRefinementTypeExpr); !ok {
		t.Fatalf("expected the binder to be rewritten to WhereRefinementTypeExpr, got %T", getDecl.Params[1].Type)
	}
}

// Using a refine alias as an ORDINARY type (not a binder) yields a clear binder-position-only
// diagnostic rather than silently erasing.
func TestNamedRefineAliasMisuseAsOrdinaryType(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def f() -> i64:
    xs: darray[Positive] = []
    return 0
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "refine_alias_misuse.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "may only be used in a binder position") {
		t.Fatalf("misusing a refine alias as an ordinary type must produce the binder-position diagnostic, got: %v", result.Errors())
	}
}

// findFuncDecl locates a top-level FuncDecl by name in the analyzed file.
func findFuncDecl(t *testing.T, result *Result, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("function decl %q not found", name)
	return nil
}
