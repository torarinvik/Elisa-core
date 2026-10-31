//go:build cgo

package backend

import (
	"fmt"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestSpecializedFuncTypeMemoUsesCompleteBindings(t *testing.T) {
	result := &semantic.Result{}
	shared := &sharedCodegenLookups{result: result}
	base := &semantic.FuncType{
		Name:         "region_signature",
		TypeParams:   []string{"T"},
		RegionParams: []string{"r"},
		Params: []semantic.Type{
			&semantic.RefType{Elem: &semantic.TypeParamType{Name: "T"}, Region: "r"},
		},
		Return: &semantic.RefType{Elem: &semantic.TypeParamType{Name: "T"}, Region: "r"},
	}
	concrete := &semantic.BuiltinType{Name: "i64"}
	firstBindings := map[string]semantic.Type{"T": concrete, "r": &semantic.RegionParamType{Name: "heap"}}
	secondBindings := map[string]semantic.Type{"r": &semantic.RegionParamType{Name: "stack"}, "T": concrete}
	firstGenerator := &llvmGenerator{result: result, sharedLookups: shared}
	secondGenerator := &llvmGenerator{result: result, sharedLookups: shared}

	first := firstGenerator.cachedSpecializedFuncType(base, firstBindings)
	reused := secondGenerator.cachedSpecializedFuncType(base, firstBindings)
	if reused != first {
		t.Fatal("identical complete bindings did not reuse the immutable specialized signature")
	}
	second := secondGenerator.cachedSpecializedFuncType(base, secondBindings)
	if second == first {
		t.Fatal("different non-layout region bindings aliased in the shared cache")
	}
	if got := first.Return.(*semantic.RefType).Region; got != "heap" {
		t.Fatalf("first specialization region = %q, want heap", got)
	}
	if got := second.Return.(*semantic.RefType).Region; got != "stack" {
		t.Fatalf("second specialization region = %q, want stack", got)
	}
	otherResult := &semantic.Result{}
	otherGenerator := &llvmGenerator{result: otherResult, sharedLookups: shared}
	otherDomain := otherGenerator.cachedSpecializedFuncType(base, firstBindings)
	if otherDomain == first {
		t.Fatal("specialized signature crossed semantic-result domains")
	}
}

func nestedSpecializationFuncType(depth int) *semantic.FuncType {
	var nested semantic.Type = &semantic.TypeParamType{Name: "T"}
	for index := 0; index < depth; index++ {
		name := fmt.Sprintf("Box%d", index)
		nested = &semantic.GenericInstanceType{
			Name: name,
			Base: &semantic.StructType{Name: name},
			Args: []semantic.Type{nested},
		}
	}
	return &semantic.FuncType{Name: "deep", TypeParams: []string{"T"}, Params: []semantic.Type{nested, nested}, Return: nested}
}

func BenchmarkSpecializeFuncTypeAcrossPartitions(b *testing.B) {
	base := nestedSpecializationFuncType(24)
	bindings := map[string]semantic.Type{"T": &semantic.BuiltinType{Name: "i64"}}
	b.Run("recompute", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = specializeFuncType(base, bindings, nil)
		}
	})
	b.Run("shared-cache-hit", func(b *testing.B) {
		result := &semantic.Result{}
		generator := &llvmGenerator{result: result, sharedLookups: &sharedCodegenLookups{result: result}}
		_ = generator.cachedSpecializedFuncType(base, bindings)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = generator.cachedSpecializedFuncType(base, bindings)
		}
	})
}

func TestOrderedSemanticTypeArgsIgnoreEnclosingImplBindings(t *testing.T) {
	concrete := &semantic.BuiltinType{Name: "i64"}
	bindings := map[string]semantic.Type{"T": concrete}
	// ensureSpecializedImplMethod specializes the signature from enclosing
	// impl parameters, while the method declares no generic params of its own.
	method := &ast.FuncDecl{Name: "method"}
	base := &semantic.FuncType{Name: "method", Params: []semantic.Type{&semantic.TypeParamType{Name: "T"}}, Return: &semantic.TypeParamType{Name: "T"}}
	specialized := specializeFuncType(base, bindings, nil)
	if len(bindings) == 0 {
		t.Fatal("fixture must have enclosing bindings")
	}
	if args := orderedGenericTypeArgs(specialized, method, bindings); len(args) != 0 {
		t.Fatal("enclosing impl bindings became declaration semantic arguments")
	}
	if specialized.Params[0] != concrete || specialized.Return != concrete {
		t.Fatal("backend signature substitution was lost")
	}
	// An ordinary generic declaration still receives its concrete arguments
	// even though specializeFuncType has erased the signature's generic list.
	generic := &ast.FuncDecl{Name: "generic", GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}}}
	args := orderedGenericTypeArgs(specialized, generic, bindings)
	if len(args) != 1 || args[0] != concrete {
		t.Fatal("actual declaration semantic specialization was skipped")
	}
}

func BenchmarkEnsureSpecializedFunctionWarm(b *testing.B) {
	result := parseAndAnalyzeBackendBenchmarkSource(b, "warm_specialization.elisa", "def bounce[T](value: T) -> T:\n    return value\n")
	symbol := result.GlobalScope.Symbols["bounce"]
	if symbol == nil {
		b.Fatal("missing generic function")
	}
	decl, ok := symbol.Node.(*ast.FuncDecl)
	if !ok {
		b.Fatal("missing declaration")
	}
	base, ok := symbol.Type.(*semantic.FuncType)
	if !ok {
		b.Fatal("missing function type")
	}
	g, err := newLLVMGenerator(result)
	if err != nil {
		b.Fatal(err)
	}
	defer g.dispose()
	g.sharedLookups = newSharedCodegenLookups(result)
	bindings := map[string]semantic.Type{"T": &semantic.BuiltinType{Name: "i64"}}
	if _, _, err := g.ensureSpecializedFunction(decl, base, bindings); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := g.ensureSpecializedFunction(decl, base, bindings); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSpecializedFunctionWithMoreThanEightBindings(t *testing.T) {
	source := `def first[A, B, C, D, E, F, G, H, I](a: A, b: B, c: C, d: D, e: E, f: F, g: G, h: H, i: I) -> A:
    return a

def answer_impl(n: i64) -> i64:
    return first(n, n, n, n, n, n, n, n, n)

export fn answer(n: i64) -> i64 = answer_impl
`
	result := parseAndAnalyzeBackendTest(t, "nine_generic_bindings.elisa", source)
	if _, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0); err != nil {
		t.Fatalf("nine-parameter specialization failed: %v", err)
	}
}
