//go:build cgo

package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func liveProofSpecializationFixture(t *testing.T) (*Result, *ast.FuncDecl) {
	t.Helper()
	const source = `law Positive(self: i64) = self > 0

def need_pos(value: i64 is Positive) -> i64:
    return value

def checked[T](value: T) -> T:
    accepted: i64 = need_pos(3)
    return value

def foreign() -> i64 is Positive:
    return 7
`
	result := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if len(result.Errors()) != 0 || len(result.ProofReport) < 2 {
		t.Fatalf("real proof fixture failed: %v %#v", result.Errors(), result.ProofReport)
	}
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "checked" {
			return result, fn
		}
	}
	t.Fatal("missing generic target")
	return nil, nil
}

func TestLiveProofReportSpecializationConstructionOwnsOnlyNewContributions(t *testing.T) {
	root, fn := liveProofSpecializationFixture(t)
	source := root.analyzer
	worker := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	if len(worker.proofLifecycle.Segments) != 0 || worker.proofReport != nil || worker.proofLifecycle.BaseEvents != 0 {
		t.Fatal("private construction inherited a canonical journal/event prefix")
	}
	ordinary := cloneAnalyzerValueForBodyWorker(source).Addr().Interface().(*Analyzer)
	if !reflect.DeepEqual(ordinary.proofLifecycle.Segments, source.proofLifecycle.Segments) || !reflect.DeepEqual(ordinary.proofReport, source.proofReport) {
		t.Fatal("body-worker full report fork changed")
	}
	isolateWorkerScopes(source, worker)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	private := context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	defer worker.closeSMT()
	worker.analyzeFuncWithTypeArgs(private, []Type{root.NamedTypes["i64"]})
	if len(worker.proofReport) == 0 || len(worker.proofLifecycle.Segments) == 0 {
		t.Fatal("concrete body stopped journaling real obligations")
	}
	foundTarget := false
	for key := range worker.proofLifecycle.Segments {
		if key.Owner == fn && key.Phase == proofPhaseBody && key.Instance != "" {
			foundTarget = true
		}
		if foreign, ok := key.Owner.(*ast.FuncDecl); ok && foreign.Name == "foreign" {
			t.Fatal("unrelated baseline row imported")
		}
	}
	if !foundTarget {
		t.Fatal("concrete target owner/phase/instance missing")
	}
}

func TestLiveProofReportSpecializationPublicationRetainsCompleteCanonicalQueries(t *testing.T) {
	root, fn := liveProofSpecializationFixture(t)
	before := append([]ProofFact(nil), root.ProofReport...)
	summary := root.ElisionSummary
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(product.Result.ProofReport, before) || !reflect.DeepEqual(product.Result.ElisionSummary, summary) {
		t.Fatal("private delta replaced canonical public reports")
	}
	if product.Result.analyzer != nil {
		t.Fatal("construction analyzer escaped publication")
	}
	nested, err := product.Result.SpecializeFunction(product.Decl, []Type{root.NamedTypes["i64"]})
	if err != nil || nested != product {
		t.Fatal("nested canonical memo routing changed", err)
	}
	if !reflect.DeepEqual(root.ProofReport, before) || !reflect.DeepEqual(root.ElisionSummary, summary) {
		t.Fatal("private journal changed canonical reports")
	}
	for _, decl := range root.File.Decls {
		foreign, ok := decl.(*ast.FuncDecl)
		if !ok || foreign.Name != "foreign" {
			continue
		}
		expected, present := root.FunctionAnalysis(foreign)
		got, found := product.Result.FunctionAnalysis(foreign)
		if !present || !found || got != expected {
			t.Fatal("foreign public query fallback changed")
		}
	}
}
