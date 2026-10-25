//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

var specializationAnalysisCloneSink *Analyzer

func TestSpecializationConstructionImportsOnlyOwnedFunctionAnalyses(t *testing.T) {
	root, fn := privateSpecializationFixture(t)
	if len(root.analyzer.functionAnalyses) < 2 {
		t.Fatal("fixture lacks foreign canonical analyses")
	}
	worker := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	if worker.functionAnalyses == nil || len(worker.functionAnalyses) != 0 {
		t.Fatal("specialization cloned unrelated analysis map")
	}
	isolateWorkerScopes(root.analyzer, worker)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	private := context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	canonical := root.analyzer.functionAnalyses[fn]
	if canonical == nil || worker.functionAnalyses[private] == nil || worker.functionAnalyses[private] == canonical {
		t.Fatal("owned import lost its private analysis copy")
	}
	if _, present := worker.functionAnalyses[fn]; present {
		t.Fatal("canonical template analysis leaked into private construction map")
	}
	for decl := range worker.functionAnalyses {
		if decl != private {
			t.Fatalf("unrelated analysis imported: %s", decl.Name)
		}
	}
}

func TestSpecializationSparseConstructionPreservesForeignAndNestedQueries(t *testing.T) {
	root, fn := privateSpecializationFixture(t)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	var foreign *ast.FuncDecl
	for _, decl := range root.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate != fn {
			foreign = candidate
			break
		}
	}
	expected, present := root.FunctionAnalysis(foreign)
	if !present || expected == nil {
		t.Fatal("fixture lacks actual foreign function analysis")
	}
	template, _ := root.FunctionAnalysis(fn)
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	if current, ok := product.Result.FunctionAnalysis(fn); !ok || current == nil || current == template {
		t.Fatal("current specialization analysis routing changed")
	}
	if current, ok := product.Result.FunctionAnalysis(foreign); !ok || current != expected {
		t.Fatal("foreign canonical analysis fallback changed")
	}
	if current, ok := product.Result.FunctionAnalysisByName(foreign.Name); !ok || current != expected {
		t.Fatal("foreign name query changed")
	}
	nested, err := product.Result.SpecializeFunction(product.Decl, []Type{root.NamedTypes["i64"]})
	if err != nil || nested != product {
		t.Fatal("nested specialization root/memo routing changed", err)
	}
}

func TestSpecializationFunctionAnalysisCloneAllocationsDoNotScaleWithForeignRows(t *testing.T) {
	// Isolate the removed input map; representative full construction allocation
	// remains covered by the real-source construction benchmark separately.
	makeAnalyzer := func(count int) *Analyzer {
		source := &Analyzer{functionAnalyses: map[*ast.FuncDecl]*FunctionAnalysis{}}
		for i := 0; i < count; i++ {
			source.functionAnalyses[&ast.FuncDecl{Name: fmt.Sprintf("foreign_%d", i)}] = &FunctionAnalysis{}
		}
		return source
	}
	small, large := makeAnalyzer(0), makeAnalyzer(8192)
	allocations := func(source *Analyzer) float64 {
		return testing.AllocsPerRun(3, func() {
			specializationAnalysisCloneSink = cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
		})
	}
	smallAllocs, largeAllocs := allocations(small), allocations(large)
	if largeAllocs > smallAllocs+2 {
		t.Fatalf("unrelated function-analysis rows still cloned: small=%v large=%v", smallAllocs, largeAllocs)
	}
	if len(specializationAnalysisCloneSink.functionAnalyses) != 0 || len(large.functionAnalyses) != 8192 {
		t.Fatal("sparse clone mutated or retained foreign map")
	}
	// Existing body-worker semantics remain unchanged.
	bodyWorker := cloneAnalyzerValueForBodyWorker(large).Addr().Interface().(*Analyzer)
	if len(bodyWorker.functionAnalyses) != 8192 || reflect.ValueOf(bodyWorker.functionAnalyses).Pointer() == reflect.ValueOf(large.functionAnalyses).Pointer() {
		t.Fatal("ordinary worker map isolation changed")
	}
	specializationAnalysisCloneSink = nil
}
