package semantic

import (
	"testing"
)

// TestErasureAnonymousWhereParam verifies that an anonymous `where` refinement on a parameter
// erases to the base type such that SameType and AssignableTo return true against the plain base.
func TestErasureAnonymousWhereParam(t *testing.T) {
	src := `
def plain(n: i64) -> i64:
    return n

def refined(n: i64 where n > 0) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "erasure_anon_where_param.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("anonymous where param should erase cleanly, got: %v", errs)
	}
	plainSym, ok := result.GlobalScope.Lookup("plain")
	if !ok {
		t.Fatal("expected plain function symbol")
	}
	refinedSym, ok := result.GlobalScope.Lookup("refined")
	if !ok {
		t.Fatal("expected refined function symbol")
	}
	plain := plainSym.Type.(*FuncType)
	refined := refinedSym.Type.(*FuncType)

	// SameType must return true: predicate does not affect type identity.
	if !SameType(plain.Params[0], refined.Params[0]) {
		t.Fatalf("anonymous where param must erase in SameType: got %s vs %s",
			plain.Params[0], refined.Params[0])
	}

	// AssignableTo must be bidirectional: no directional type hierarchy from the predicate.
	if !AssignableTo(plain.Params[0], refined.Params[0]) {
		t.Fatalf("plain -> refined must be assignable after erasure")
	}
	if !AssignableTo(refined.Params[0], plain.Params[0]) {
		t.Fatalf("refined -> plain must be assignable after erasure")
	}
}

// TestErasureAnonymousWhereReturn verifies that an anonymous `where` refinement on the return type
// erases to the base type such that SameType and AssignableTo return true against the plain base.
func TestErasureAnonymousWhereReturn(t *testing.T) {
	src := `
def plain() -> i64:
    return 1

def refined() -> i64 where result > 0:
    return 1
`
	result := analyzeTreeTestSource(t, "erasure_anon_where_return.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("anonymous where return should erase cleanly, got: %v", errs)
	}
	plainSym, ok := result.GlobalScope.Lookup("plain")
	if !ok {
		t.Fatal("expected plain function symbol")
	}
	refinedSym, ok := result.GlobalScope.Lookup("refined")
	if !ok {
		t.Fatal("expected refined function symbol")
	}
	plain := plainSym.Type.(*FuncType)
	refined := refinedSym.Type.(*FuncType)

	// SameType must return true: predicate does not affect type identity.
	if !SameType(plain.Return, refined.Return) {
		t.Fatalf("anonymous where return must erase in SameType: got %s vs %s",
			plain.Return, refined.Return)
	}

	// AssignableTo must be bidirectional: no directional type hierarchy from the predicate.
	if !AssignableTo(plain.Return, refined.Return) {
		t.Fatalf("plain -> refined return must be assignable after erasure")
	}
	if !AssignableTo(refined.Return, plain.Return) {
		t.Fatalf("refined -> plain return must be assignable after erasure")
	}
}

// TestErasureNamedRefineAliasParam verifies that a named `refine` alias used as a parameter type
// erases to the base type such that SameType and AssignableTo return true against the plain base.
func TestErasureNamedRefineAliasParam(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def plain(n: i64) -> i64:
    return n

def refined(n: Positive) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "erasure_named_refine_param.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("named refine alias param should erase cleanly, got: %v", errs)
	}
	plainSym, ok := result.GlobalScope.Lookup("plain")
	if !ok {
		t.Fatal("expected plain function symbol")
	}
	refinedSym, ok := result.GlobalScope.Lookup("refined")
	if !ok {
		t.Fatal("expected refined function symbol")
	}
	plain := plainSym.Type.(*FuncType)
	refined := refinedSym.Type.(*FuncType)

	// SameType must return true: refine alias erases to base type.
	if !SameType(plain.Params[0], refined.Params[0]) {
		t.Fatalf("named refine alias param must erase in SameType: got %s vs %s",
			plain.Params[0], refined.Params[0])
	}

	// AssignableTo must be bidirectional: no directional type hierarchy from the alias.
	if !AssignableTo(plain.Params[0], refined.Params[0]) {
		t.Fatalf("plain -> refined param must be assignable after erasure")
	}
	if !AssignableTo(refined.Params[0], plain.Params[0]) {
		t.Fatalf("refined -> plain param must be assignable after erasure")
	}
}

// TestErasureLocalVariableWhere verifies that a local variable with an inline `where` refinement
// (the binding target has a where-refined type) erases to the base type in SameType/AssignableTo.
func TestErasureLocalVariableWhere(t *testing.T) {
	src := `
def test_plain() -> i64:
    x: i64 = 5
    return x

def test_refined() -> i64:
    y: i64 where y > 0 = 5
    return y
`
	result := analyzeTreeTestSource(t, "erasure_local_var_where.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("local variable where should erase cleanly, got: %v", errs)
	}
}

// TestErasureMatrixParamAndReturn is a comprehensive matrix test that verifies all refinement
// surface forms (anonymous where param, anonymous where return, named refine alias) produce
// identical type behavior under SameType and AssignableTo.
func TestErasureMatrixParamAndReturn(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def anon_where_param(n: i64 where n > 0) -> i64:
    return n

def anon_where_return() -> i64 where result > 0:
    return 1

def named_alias_param(n: Positive) -> i64:
    return n

def plain_param(n: i64) -> i64:
    return n

def plain_return() -> i64:
    return 1
`
	result := analyzeTreeTestSource(t, "erasure_matrix_param_return.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("erasure matrix should analyze cleanly, got: %v", errs)
	}

	// Verify all parameter types with refinements erase to base i64
	anonWhereParamSym, _ := result.GlobalScope.Lookup("anon_where_param")
	namedAliasParamSym, _ := result.GlobalScope.Lookup("named_alias_param")
	plainParamSym, _ := result.GlobalScope.Lookup("plain_param")

	anonWhereParamFunc := anonWhereParamSym.Type.(*FuncType)
	namedAliasParamFunc := namedAliasParamSym.Type.(*FuncType)
	plainParamFunc := plainParamSym.Type.(*FuncType)

	// All three parameter types should be SameType.
	if !SameType(plainParamFunc.Params[0], anonWhereParamFunc.Params[0]) {
		t.Fatalf("plain param vs anon_where param must be SameType after erasure")
	}
	if !SameType(plainParamFunc.Params[0], namedAliasParamFunc.Params[0]) {
		t.Fatalf("plain param vs named_alias param must be SameType after erasure")
	}
	if !SameType(anonWhereParamFunc.Params[0], namedAliasParamFunc.Params[0]) {
		t.Fatalf("anon_where param vs named_alias param must be SameType after erasure")
	}

	// Verify all return types with refinements erase to base i64
	anonWhereReturnSym, _ := result.GlobalScope.Lookup("anon_where_return")
	plainReturnSym, _ := result.GlobalScope.Lookup("plain_return")

	anonWhereReturnFunc := anonWhereReturnSym.Type.(*FuncType)
	plainReturnFunc := plainReturnSym.Type.(*FuncType)

	if !SameType(plainReturnFunc.Return, anonWhereReturnFunc.Return) {
		t.Fatalf("plain return vs anon_where return must be SameType after erasure")
	}

	// Verify bidirectional assignability for all erased types
	if !AssignableTo(plainParamFunc.Params[0], anonWhereParamFunc.Params[0]) ||
		!AssignableTo(anonWhereParamFunc.Params[0], plainParamFunc.Params[0]) {
		t.Fatalf("plain and anon_where params must be bidirectionally assignable after erasure")
	}
	if !AssignableTo(plainParamFunc.Params[0], namedAliasParamFunc.Params[0]) ||
		!AssignableTo(namedAliasParamFunc.Params[0], plainParamFunc.Params[0]) {
		t.Fatalf("plain and named_alias params must be bidirectionally assignable after erasure")
	}
	if !AssignableTo(plainReturnFunc.Return, anonWhereReturnFunc.Return) ||
		!AssignableTo(anonWhereReturnFunc.Return, plainReturnFunc.Return) {
		t.Fatalf("plain and anon_where returns must be bidirectionally assignable after erasure")
	}
}
