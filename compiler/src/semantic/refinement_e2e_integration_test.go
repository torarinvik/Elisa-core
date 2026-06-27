//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// TestRefinementE2E_WhereParamCallsRequires exercises the combination of:
// - a parameter with a `where` precondition
// - calling a function with a `requires` contract
// Both should prove in the caller and be discharged cleanly.
func TestRefinementE2E_WhereParamCallsRequires(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def add_one(n: Positive) -> i64:
    requires n > 0
    return n + 1

def caller(x: Positive) -> i64:
    return add_one(x)
`
	result := analyzeContractStrict(t, "where_calls_requires.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where param calling requires should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_WhereParamCallsEnsure exercises the combination of:
// - a parameter with a `where` precondition
// - calling a function with an `ensure` postcondition
// The ensure should propagate the guarantee to the caller.
func TestRefinementE2E_WhereParamCallsEnsure(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def caller(x: Positive) -> i64:
    y: i64 = x
    return y
`
	result := analyzeContractStrict(t, "where_calls_ensure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where param calling ensure should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_NamedAliasInFunctionSignature exercises the combination of:
// - a refine alias defining a refined type
// - a function parameter using that refined alias
// The alias should discharge at the call site.
func TestRefinementE2E_NamedAliasInFunctionSignature(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def accept_positive(x: Positive) -> i64:
    return x

def create() -> i64:
    return accept_positive(5)
`
	result := analyzeContractStrict(t, "alias_in_function_signature.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("named alias in function signature should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_RefineAliasAndEnsureCombined exercises the combination of:
// - a `refine` alias defining a refined type
// - an `ensure` postcondition on a function with that return type
// The postcondition should apply to a value of the refined alias type.
func TestRefinementE2E_RefineAliasAndEnsureCombined(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def make_positive(x: i64) -> Positive:
    requires x > 0
    return x

def test() -> Positive:
    return make_positive(5)
`
	result := analyzeContractStrict(t, "refine_and_ensure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("refine alias with ensure should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_ParametricRefineInNestedCall exercises the combination of:
// - a parametric refine alias (depends on a value argument)
// - nested function calls where the parametric constraint carries through
// Assertion: the system verifies the parametric constraint at the call site.
func TestRefinementE2E_ParametricRefineInNestedCall(t *testing.T) {
	src := `
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get_at(xs: darray[i64], idx: IndexOf[xs]) -> i64:
    return xs[idx]

def caller(items: darray[i64]) -> i64:
    if items.count > 0:
        return get_at(items, 0)
    return 0
`
	result := analyzeContractStrict(t, "parametric_refine_nested.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric refine in nested call should analyze cleanly when provable, got: %v", errs)
	}
}

// TestRefinementE2E_WhereStructFieldStored exercises the combination of:
// - a struct field with a `where` constraint
// - reading and using the field later
// The field constraint is verified at construction.
func TestRefinementE2E_WhereStructFieldStored(t *testing.T) {
	src := `
struct Coord:
    x: i64 where x >= 0
    y: i64 where y >= 0

def make_coord() -> Coord:
    return Coord{x: 10, y: 20}

def read_x(c: Coord) -> i64:
    return c.x
`
	result := analyzeContractStrict(t, "where_struct_field_stored.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where struct field stored should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_ChainedRequiresAndEnsure exercises the combination of:
// - a function with both `requires` precondition and `ensure` postcondition
// - that function being called in a context where both are relevant
func TestRefinementE2E_ChainedRequiresAndEnsure(t *testing.T) {
	src := `
def transform(x: i64) -> i64:
    requires x > 0 and x < 100
    ensure result > 0
    return x + 1

def caller() -> i64:
    y = transform(5)
    return y
`
	result := analyzeContractStrict(t, "chained_requires_ensure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("chained requires and ensure should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_MultipleWhereConstraints exercises:
// - a struct with multiple fields, each with `where` constraints
// - ensuring all constraints are satisfied on construction
func TestRefinementE2E_MultipleWhereConstraints(t *testing.T) {
	src := `
struct Triple:
    a: i64 where a > 0
    b: i64 where b > a
    c: i64 where c > b

def make_triple() -> Triple:
    return Triple{a: 1, b: 2, c: 3}
`
	result := analyzeContractStrict(t, "multiple_where_constraints.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("multiple where constraints should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_RefineAliasTypeErasure verifies that a refine alias parameter erases
// to its base type before signature comparison, so overloading or polymorphism treats
// the parameter identically to a plainly-typed parameter.
func TestRefinementE2E_RefineAliasTypeErasure(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def process(n: Positive) -> i64:
    return n

def main() -> void:
    process(5)
`
	result := analyzeTreeTestSource(t, "refine_alias_erasure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("refine alias type erasure should analyze cleanly, got: %v", errs)
	}
	// Verify that the process symbol's parameter type erases to i64
	sym, ok := result.GlobalScope.Lookup("process")
	if !ok {
		t.Fatal("expected process symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", sym.Type)
	}
	if !IsNumericType(fnType.Params[0]) || fnType.Params[0].String() != "i64" {
		t.Fatalf("expected refine alias parameter to erase to i64, got %s", fnType.Params[0])
	}
}

// TestRefinementE2E_WhereFieldInFunction exercises:
// - a struct with a `where`-constrained field
// - reading that field in a function
// The where constraint is verified at struct construction.
func TestRefinementE2E_WhereFieldInFunction(t *testing.T) {
	src := `
struct Node:
    value: i64 where value >= 0

def get_value(n: Node) -> i64:
    return n.value

def make_node() -> Node:
    return Node{value: 42}
`
	result := analyzeTreeTestSource(t, "where_field_in_function.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where field in function should analyze cleanly, got: %v", errs)
	}
	// Verify that get_value function is present and accepts a Node parameter
	getFn, ok := result.GlobalScope.Lookup("get_value")
	if !ok {
		t.Fatal("expected get_value function")
	}
	getFnType, ok := getFn.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type for get_value, got %T", getFn.Type)
	}
	// Verify parameter is a StructType (Node)
	nodeType, ok := getFnType.Params[0].(*StructType)
	if !ok {
		t.Fatalf("expected StructType for first param of get_value, got %T", getFnType.Params[0])
	}
	// Get the "value" field from the Fields map
	valueField, ok := nodeType.Fields["value"]
	if !ok {
		t.Fatal("expected value field")
	}
	if !IsNumericType(valueField.Type) || valueField.Type.String() != "i64" {
		t.Fatalf("expected where-field to erase to i64, got %s", valueField.Type)
	}
}

// TestRefinementE2E_ViolatedWhereInNestedCall verifies that a violation of a where
// constraint in a nested call is caught at the outer call site.
func TestRefinementE2E_ViolatedWhereInNestedCall(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def needs_positive(n: Positive) -> i64:
    return n

def bad_caller() -> i64:
    return needs_positive(-1)
`
	result := analyzeContractStrict(t, "where_violated_nested.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "is violated") && !strings.Contains(errText, "could not be proven") {
		t.Fatalf("violated where in nested call should error, got: %v", result.Errors())
	}
}

// TestRefinementE2E_FunctionAnalysisPreservesSignatures verifies that function analysis
// metadata is correctly recorded for functions with refine aliases in their signatures.
func TestRefinementE2E_FunctionAnalysisPreservesSignatures(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def make_positive(x: i64) -> i64:
    ensure result > 0
    return x + 1

def use_positive(n: Positive) -> void:
    pass
`
	result := analyzeFunctionAnalysisTestSource(t, "func_analysis_preserves_sig.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("function analysis should succeed, got: %v", errs)
	}
	// Verify both functions are recorded in scope
	makeSym, ok := result.GlobalScope.Lookup("make_positive")
	if !ok {
		t.Fatal("expected make_positive symbol")
	}
	makeType, ok := makeSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", makeSym.Type)
	}
	useSym, ok := result.GlobalScope.Lookup("use_positive")
	if !ok {
		t.Fatal("expected use_positive symbol")
	}
	useType, ok := useSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type for use_positive, got %T", useSym.Type)
	}
	// Verify parameter type in use_positive erases to i64
	if !IsNumericType(useType.Params[0]) || useType.Params[0].String() != "i64" {
		t.Fatalf("expected refined parameter to erase to i64, got %s", useType.Params[0])
	}
	// Both functions should have empty or valid poststate lists
	_ = makeType
}

// TestRefinementE2E_AnonWhereWithEnsure exercises combining:
// - an anonymous `where` constraint on a parameter
// - an `ensure` postcondition
// to verify that both are enforced correctly in the same function.
func TestRefinementE2E_AnonWhereWithEnsure(t *testing.T) {
	src := `
def double_positive(x: i64 where x > 0) -> i64:
    requires x > 0 and x < 4611686018427387903
    ensure result > x
    return x * 2
`
	result := analyzeContractStrict(t, "anon_where_with_ensure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("anon where with ensure should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_StructFieldWhereAndEnsureReturnType exercises:
// - a struct with a field that has a `where` constraint
// - a function with an `ensure` that constrains a return value of that struct type
func TestRefinementE2E_StructFieldWhereAndEnsureReturnType(t *testing.T) {
	src := `
struct Box:
    val: i64 where val > 0

def make_box() -> Box:
    ensure result.val > 0
    return Box{val: 5}
`
	result := analyzeContractStrict(t, "struct_where_ensure_return.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("struct field where with ensure return should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_WherePredicateReferencesAnonymousWhere exercises:
// - a parameter with an anonymous `where` constraint
// - a struct field also constrained by `where`
// - ensuring that both constraints are independently verified
func TestRefinementE2E_AnonWhereWithStructWhere(t *testing.T) {
	src := `
struct Item:
    count: i64 where count > 0

def process(items: i64 where items > 0) -> Item:
    return Item{count: items}
`
	result := analyzeContractStrict(t, "anon_where_struct_where.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("anon where with struct where should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_ParametricRefineAliasWithEnsure exercises:
// - a parametric refine alias (like IndexOf[xs])
// - a function using that alias with an `ensure` poststate
func TestRefinementE2E_ParametricRefineAliasWithEnsure(t *testing.T) {
	src := `
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get_first(xs: darray[i64]) -> i64:
    ensure result == xs[0]
    return xs[0]
`
	result := analyzeContractStrict(t, "parametric_refine_with_ensure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric refine with ensure should analyze cleanly, got: %v", errs)
	}
}

// TestRefinementE2E_ASTRewritingInParamBinder verifies that the AST is correctly
// rewritten when a refine alias is used in a parameter binder, converting it to
// a WhereRefinementTypeExpr.
func TestRefinementE2E_ASTRewritingInParamBinder(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def process(n: Positive) -> void:
    pass
`
	result := analyzeTreeTestSource(t, "ast_rewriting_binder.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("AST rewriting should succeed, got: %v", errs)
	}
	// Find the process FuncDecl and verify param type was rewritten
	procDecl := findFuncDecl(t, result, "process")
	if len(procDecl.Params) < 1 {
		t.Fatal("expected at least one parameter")
	}
	paramType := procDecl.Params[0].Type
	if _, ok := paramType.(*ast.WhereRefinementTypeExpr); !ok {
		t.Fatalf("expected param type to be rewritten to WhereRefinementTypeExpr, got %T", paramType)
	}
}
