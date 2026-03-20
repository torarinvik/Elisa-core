package semantic_test

import (
	"testing"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func requireOptimizationFactsFunctionDecl(t *testing.T, result *semantic.Result, name string) *ast.FuncDecl {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	fn, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected %s decl to be a function, got %T", name, sym.Node)
	}
	return fn
}

func requireOptimizationFactsVarInitExpr(t *testing.T, fn *ast.FuncDecl, name string) ast.Expr {
	t.Helper()
	for _, stmt := range fn.Body {
		decl, ok := stmt.(*ast.VarDeclStmt)
		if !ok {
			continue
		}
		if decl.Name == name {
			return decl.Value
		}
	}
	t.Fatalf("expected var decl %q in function %q", name, fn.Name)
	return nil
}

func requireExprOptimizationFacts(t *testing.T, result *semantic.Result, expr ast.Expr) semantic.OptimizationFacts {
	t.Helper()
	facts, ok := result.ExprOptimizationFacts(expr)
	if !ok {
		t.Fatalf("expected optimization facts for %T", expr)
	}
	return facts
}

func TestAnalyzeCollectsOptimizationFactsForShapeBackedCollections(t *testing.T) {
	src := `def inspect(values: darray[i32, row], other: darray[i32, row], text: dstr[row], any_values: darray[i32], buf: array[i32, 4]) -> int:
	same_a: darray[i32, row] = values
	same_b: darray[i32, row] = other
	text_copy: dstr[row] = text
	wildcard_copy: darray[i32] = any_values
	slice: view[i32, 0u, 2u] = buf[0u:2u]
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_shape_backed.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	sameAFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "same_a"))
	sameBFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "same_b"))
	textFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "text_copy"))
	wildcardFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "wildcard_copy"))
	sliceFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "slice"))

	if !sameAFacts.Contiguous || !sameAFacts.UnitStride {
		t.Fatalf("expected same_a facts to mark contiguous unit-stride access, got %#v", sameAFacts)
	}
	if sameAFacts.ReadOnly {
		t.Fatalf("expected same_a facts to stay writable, got %#v", sameAFacts)
	}
	if !sameAFacts.HasExactExtent() {
		t.Fatalf("expected same_a facts to preserve exact shape extent, got %#v", sameAFacts)
	}
	if !sameAFacts.SameExtent(sameBFacts) {
		t.Fatalf("expected same-shape darray values to share exact extent, got %#v vs %#v", sameAFacts, sameBFacts)
	}

	if !textFacts.ReadOnly || !textFacts.Contiguous || !textFacts.UnitStride {
		t.Fatalf("expected dstr facts to be readonly contiguous unit-stride, got %#v", textFacts)
	}
	if !textFacts.HasExactExtent() {
		t.Fatalf("expected dstr facts to preserve exact shape extent, got %#v", textFacts)
	}
	if !sameAFacts.SameExtent(textFacts) {
		t.Fatalf("expected shared shape identity between darray and dstr facts, got %#v vs %#v", sameAFacts, textFacts)
	}

	if wildcardFacts.HasExactExtent() {
		t.Fatalf("expected wildcard-shape darray facts to keep extent unknown, got %#v", wildcardFacts)
	}
	if !wildcardFacts.Contiguous || !wildcardFacts.UnitStride {
		t.Fatalf("expected wildcard-shape darray facts to retain contiguity/stride, got %#v", wildcardFacts)
	}

	if !sliceFacts.Contiguous || !sliceFacts.UnitStride {
		t.Fatalf("expected fixed-array slice facts to mark contiguous unit-stride access, got %#v", sliceFacts)
	}
	if !sliceFacts.HasExactExtent() {
		t.Fatalf("expected bounded slice facts to preserve exact view bounds, got %#v", sliceFacts)
	}
	if sliceFacts.ReadOnly {
		t.Fatalf("expected numeric array slice facts to stay writable by default, got %#v", sliceFacts)
	}
	if sliceFacts.SameExtent(sameAFacts) {
		t.Fatalf("expected bounded slice extent to differ from shape-backed row extent, got %#v vs %#v", sliceFacts, sameAFacts)
	}
}

func TestAnalyzeMarksFreshRegionAllocationsAsExclusive(t *testing.T) {
	src := `def inspect(seed: i32) -> i32:
	region scratch(1024u)
	slot: any i32& = new[scratch] seed
	alias: any i32& = slot
	return alias[0u]
`
	result, errs := parseAndAnalyze(t, "optimization_facts_region_alloc_exclusive.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	allocFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "slot"))
	aliasFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "alias"))

	if !allocFacts.Exclusive {
		t.Fatalf("expected fresh region allocation to be marked exclusive, got %#v", allocFacts)
	}
	if aliasFacts.Exclusive {
		t.Fatalf("expected plain identifier rebinding to avoid claiming exclusivity, got %#v", aliasFacts)
	}
	if allocFacts.Contiguous || allocFacts.UnitStride || allocFacts.ReadOnly || allocFacts.HasExactExtent() {
		t.Fatalf("expected scalar region allocation to only contribute exclusivity, got %#v", allocFacts)
	}
}
