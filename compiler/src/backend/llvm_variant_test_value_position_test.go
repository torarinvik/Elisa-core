//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// A variant `is` test with BINDERS, used in a VALUE position, over an enum declared inside
// a `module`. This failed with "unsupported expression *ast.VariantTestExpr".
//
// enumIsTargetPattern resolved the pattern's enum through the raw NamedTypes map, which is
// keyed by the QUALIFIED name (`Ast.Expr`), while a pattern records the name as WRITTEN
// (`Expr`). The lookup missed, the function reported "not an enum target", and emitIsExpr
// fell through to the comparable path — which tried to emit the PATTERN itself as a value.
//
// Only value positions were affected: a condition is intercepted by directConditionPattern
// long before this, which is why the compiler's own pervasive `if x is T.V(...)` never
// tripped it. The regression guard therefore has to use an initializer or a return.
func TestVariantTestWithBindersInValuePositionOverModuleEnum(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "variant_test_value.elisa", `module Ast:
	enum Node layout(handle: u32):
		pass

	enum Expr is Node:
		Ident(name: sview, line: u32)
		Lit(value: i64)

using Ast

def is_ident_init(e: Expr) -> bool:
	flag: bool = e is Expr.Ident(_, _)
	return flag

def is_ident_return(e: Expr) -> bool:
	return e is Expr.Ident(_, _)

def is_ident_named_binders(e: Expr) -> bool:
	flag: bool = e is Expr.Ident(n, l)
	return flag
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, fn := range []string{"is_ident_init", "is_ident_return", "is_ident_named_binders"} {
		if !strings.Contains(output, "@"+fn) {
			t.Fatalf("expected %s to be emitted; got:\n%s", fn, output)
		}
	}
}

// The same shape at TOP LEVEL always worked — it is the module qualification that broke the
// lookup. Kept so a future "simplify the name resolution" change cannot pass by regressing
// both cases to the same wrong answer.
func TestVariantTestWithBindersInValuePositionTopLevelEnum(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "variant_test_value_top.elisa", `enum Node layout(handle: u32):
	pass

enum Expr is Node:
	Ident(name: sview, line: u32)
	Lit(value: i64)

def is_ident(e: Expr) -> bool:
	flag: bool = e is Expr.Ident(_, _)
	return flag
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(output, "@is_ident") {
		t.Fatalf("expected is_ident to be emitted; got:\n%s", output)
	}
}
