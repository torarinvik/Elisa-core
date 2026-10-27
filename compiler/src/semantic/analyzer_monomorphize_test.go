//go:build cgo

package semantic

import (
	"maps"
	"sync"
	"testing"

	"elisacore/src/ast"
)

func TestSpecializedExprTypesRestoresFunctionAnnotationsOnly(t *testing.T) {
	result := analyzeTreeTestSource(t, "specialized_expr_types.elisa", `def id[T](x: T) -> T:
    return x

def other(x: i64) -> i64:
    return x + 1
`)
	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "id" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatal("generic function id was not found")
	}

	before := maps.Clone(result.ExprTypes)
	functionExprs := map[ast.Expr]struct{}{}
	collectSpecializedFunctionExprs(fn, functionExprs)
	overlay := result.SpecializedExprTypes(fn, []Type{&BuiltinType{Name: "i64"}})
	if len(overlay) == 0 {
		t.Fatal("expected concrete type annotations from specialized re-analysis")
	}
	for expr, typ := range before {
		if _, belongsToFunction := functionExprs[expr]; belongsToFunction {
			continue
		}
		if result.ExprTypes[expr] != typ {
			t.Fatalf("specialization changed expression type outside id: %T", expr)
		}
	}
	for expr, typ := range before {
		if _, belongsToFunction := functionExprs[expr]; belongsToFunction && result.ExprTypes[expr] != typ {
			t.Fatalf("specialization did not restore the template type for %T", expr)
		}
	}
}

func TestSpecializedExprTypesAfterCodegenMemo(t *testing.T) {
	result := analyzeTreeTestSource(t, "specialized_expr_memo.elisa", `def id[T](x: T) -> T:
    return x
`)
	fn, ok := result.File.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatal("missing generic declaration")
	}
	args := []Type{result.analyzer.namedTypes["i64"]}
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	ordinary, err := result.SpecializeFunction(fn, args)
	if err != nil || ordinary == nil {
		t.Fatalf("codegen specialization: %v", err)
	}
	if ordinary.originalExprs != nil {
		t.Fatal("codegen specialization built the compatibility projection")
	}
	if overlay := result.SpecializedExprTypes(fn, args); len(overlay) == 0 {
		t.Fatal("compatibility query lost concrete expression types after codegen memoization")
	}
	projected, err := result.specializeFunctionWithOptions(fn, args, false, true)
	if err != nil || projected == nil || projected == ordinary || len(projected.originalExprs) == 0 {
		t.Fatalf("compatibility projection was not separately memoized: %v", err)
	}
	privateProjection, err := ordinary.Result.specializeFunctionWithOptions(ordinary.Decl, args, false, true)
	if err != nil || privateProjection != projected {
		t.Fatalf("private view did not route the compatibility product through its canonical root: %v", err)
	}
	if overlay := ordinary.Result.SpecializedExprTypes(ordinary.Decl, args); len(overlay) != 0 {
		t.Fatal("already-specialized private view redundantly projected concrete types")
	}
	repeated, err := result.SpecializeFunction(fn, args)
	if err != nil || repeated != ordinary {
		t.Fatal("compatibility query displaced the codegen product")
	}
	audited, err := result.SpecializeFunctionWithInputAudit(fn, args)
	if err != nil || audited == nil || !audited.inputReadsObserved || audited.originalExprs != nil {
		t.Fatalf("diagnostic specialization lost its independent observations: %v", err)
	}
}

type unkeyableProjectionType struct{}

func (*unkeyableProjectionType) String() string { return "unkeyable" }
func (*unkeyableProjectionType) isType()        {}

func TestSpecializationProjectionUnkeyableArgument(t *testing.T) {
	result := analyzeTreeTestSource(t, "specialized_expr_unkeyable.elisa", `def id[T](x: T) -> T:
    return x
`)
	fn := result.File.Decls[0].(*ast.FuncDecl)
	args := []Type{&unkeyableProjectionType{}}
	if _, keyed := specializationMemoKeyForProjection(fn, args, true); keyed {
		t.Fatal("unsupported type unexpectedly received a memo key")
	}
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	ordinary, ordinaryErr := result.specializeFunctionWithOptions(fn, args, false, false)
	projected, projectionErr := result.specializeFunctionWithOptions(fn, args, false, true)
	if (ordinaryErr == nil) != (projectionErr == nil) {
		t.Fatalf("unkeyed modes disagree: ordinary=%v projection=%v", ordinaryErr, projectionErr)
	}
	if ordinaryErr == nil && (ordinary == nil || projected == nil || ordinary.originalExprs != nil || len(projected.originalExprs) == 0) {
		t.Fatal("unkeyed compatibility product lost its projection")
	}
	if len(result.shared.bodies) != 0 {
		t.Fatal("unkeyable argument entered the shared memo")
	}
}

func TestSpecializationProjectionModesBuildConcurrently(t *testing.T) {
	result := analyzeTreeTestSource(t, "specialized_expr_parallel.elisa", `def id[T](x: T) -> T:
    return x
`)
	fn := result.File.Decls[0].(*ast.FuncDecl)
	args := []Type{result.analyzer.namedTypes["i64"]}
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	start := make(chan struct{})
	var normal *SpecializedFunction
	var normalErr error
	var overlay map[ast.Expr]Type
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); <-start; normal, normalErr = result.SpecializeFunction(fn, args) }()
	go func() { defer wg.Done(); <-start; overlay = result.SpecializedExprTypes(fn, args) }()
	close(start)
	wg.Wait()
	if normalErr != nil || normal == nil || normal.originalExprs != nil || len(overlay) == 0 {
		t.Fatalf("concurrent codegen and compatibility products failed: %v", normalErr)
	}
}
