//go:build cgo

package semantic

import (
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"elisacore/src/ast"
)

func TestOptimizationSymbolIdentityOverlayExactExistingAndNewUIDs(t *testing.T) {
	known, empty, missing, other := &Symbol{}, &Symbol{}, &Symbol{}, &Symbol{}
	counter := &atomic.Uint64{}
	counter.Store(77)
	base := map[*Symbol]string{known: "symbol@12", empty: "", nil: "symbol@13"}
	root := &Analyzer{optimizationSymbolIDs: base, optimizationIdentityNext: counter}
	worker := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if worker.optimizationSymbolIdentityOverlay == nil || len(worker.optimizationSymbolIDs) != 0 {
		t.Fatal("whole canonical ID table was eagerly copied")
	}
	reference := deepCloneValue(reflect.ValueOf(base)).Interface().(map[*Symbol]string)
	for symbol, want := range reference {
		got, present := worker.lookupOptimizationSymbolIdentity(symbol)
		if !present || got != want {
			t.Fatal("known identity or present empty string lost")
		}
	}
	if _, present := worker.lookupOptimizationSymbolIdentity(missing); present {
		t.Fatal("missing became known empty")
	}
	if worker.optimizationSymbolIdentity(known) != "symbol@12" || worker.optimizationSymbolIdentity(empty) != "" || worker.optimizationSymbolIdentity(nil) != "" || counter.Load() != 77 {
		t.Fatal("known/nil lookup changed UID allocation behavior")
	}
	if got := worker.optimizationSymbolIdentity(missing); got != "symbol@78" || worker.optimizationSymbolIdentity(missing) != got || counter.Load() != 78 {
		t.Fatal("new symbol did not allocate exactly once from original counter", got)
	}
	if _, present := base[missing]; present {
		t.Fatal("private miss wrote canonical table")
	}
	worker.setOptimizationSymbolIdentity(known, "")
	if got, present := worker.lookupOptimizationSymbolIdentity(known); !present || got != "" || base[known] != "symbol@12" {
		t.Fatal("private present-empty shadow lost or reached base")
	}
	ordinary := cloneAnalyzerValueForBodyWorker(worker).Addr().Interface().(*Analyzer)
	ordinary.setOptimizationSymbolIdentity(other, "private")
	if _, present := worker.lookupOptimizationSymbolIdentity(other); present {
		t.Fatal("ordinary private clone shares writable local rows")
	}
	if got, present := ordinary.lookupOptimizationSymbolIdentity(known); !present || got != "" {
		t.Fatal("ordinary construction clone lost local/base logical lookup")
	}
	for _, table := range []map[*Symbol]string{nil, {}} {
		source := &Analyzer{optimizationSymbolIDs: table}
		owned := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
		body := cloneAnalyzerValueForBodyWorker(source).Addr().Interface().(*Analyzer)
		if (owned.optimizationSymbolIDs == nil) != (table == nil) || (body.optimizationSymbolIDs == nil) != (table == nil) || body.optimizationSymbolIdentityOverlay != nil {
			t.Fatal("nil/empty or ordinary clone policy changed")
		}
	}
}

func TestOptimizationSymbolIdentityOverlayResourceGuards(t *testing.T) {
	const source = "def identity(x: i32) -> i32:\n    return x\n"
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	worker := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	worker.exprTypeOverlay = nil
	worker.exprFactsOverlay = nil
	worker.iterBinderElementOverlay = nil
	worker.noStorageOverlay = nil
	worker.optimizationSymbolIdentityOverlay.base = map[*Symbol]string{}
	state.a.optimizationSymbolIdentityOverlay = worker.optimizationSymbolIdentityOverlay
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("marshal accepted resource")
	}
	if err := (&AnalysisBuild{retained: state, file: result.File, loweredFile: result.LoweredFile}).validate(); err == nil || !strings.Contains(err.Error(), "optimization-symbol identity overlay") {
		t.Fatal("build accepted resource", err)
	}
	encoded, err := semanticStateRegistry().Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	if restored, err := RestoreAnalysisState(encoded); restored != nil || err == nil || !strings.Contains(err.Error(), "optimization-symbol identity overlay") {
		t.Fatal("restore independently encoded resource", err)
	}
	state.a.optimizationSymbolIdentityOverlay = nil
	if err := validateSparseFactValueType(reflect.ValueOf(worker.optimizationSymbolIdentityOverlay)); err == nil {
		t.Fatal("resource entered fact closure")
	}
	panicked := func() (p bool) {
		defer func() { p = recover() != nil }()
		cloneAnalyzerValueForSpecialization(worker)
		return false
	}()
	if !panicked {
		t.Fatal("nested clone accepted construction root")
	}
	if len(worker.finishProgramResult(result.File, result.LoweredFile, nil, AnalyzeOptions{}, nil).Errors()) == 0 {
		t.Fatal("canonical publication accepted construction root")
	}
	mergeValue(reflect.ValueOf(state.a).Elem(), reflect.ValueOf(worker).Elem(), reflect.ValueOf(state.a).Elem(), nil)
	if len(state.a.diagnostics) == 0 {
		t.Fatal("merge accepted construction root")
	}
	before := len(worker.diagnostics)
	worker.analyzeDeclsParallel(nil, 2)
	if len(worker.diagnostics) <= before {
		t.Fatal("scheduler accepted construction root")
	}
}

func TestOptimizationSymbolIdentityOverlayPrivateFactsAndNestedPublication(t *testing.T) {
	root, fn := privateSpecializationFixture(t)
	// An existing canonical identity is a real full string dependency, not a
	// cardinality-only marker. Specialization must neither clear nor regenerate it.
	symbol := root.GlobalScope.Symbols["first"]
	if symbol == nil {
		t.Fatal("fixture has no canonical first symbol")
	}
	root.analyzer.optimizationIdentityNext.Store(500000)
	root.analyzer.setOptimizationSymbolIdentity(symbol, "symbol@500000")
	before := maps.Clone(root.analyzer.optimizationSymbolIDs)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["Record"]})
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(before, root.analyzer.optimizationSymbolIDs) {
		t.Fatal("publication or nested specialization mutated canonical identity rows")
	}
	if product.Result.analyzer != nil {
		t.Fatal("construction Analyzer escaped sparse publication")
	}
	private := product.Decl.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	var canonicalBounce *ast.FuncDecl
	for _, declaration := range root.File.Decls {
		if candidate, ok := declaration.(*ast.FuncDecl); ok && candidate.Name == "bounce" {
			canonicalBounce = candidate
		}
	}
	if canonicalBounce == nil {
		t.Fatal("fixture has no resolved nested callee")
	}
	if _, err := product.Result.SpecializeFunction(canonicalBounce, []Type{root.NamedTypes["Record"]}); err != nil {
		t.Fatal(err)
	}
	if typ, present := product.Result.LookupExprTypes(private); !present || typ != root.NamedTypes["Record"] {
		t.Fatal("nested publication lost concrete semantic view")
	}
	// Pre-memo resource gate: even an already memoized body cannot make an active
	// construction Analyzer a canonical starting point.
	root.analyzer.optimizationSymbolIdentityOverlay = &analyzerOptimizationSymbolIdentityOverlay{}
	if _, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["Record"]}); err == nil {
		t.Fatal("memo hit bypassed active identity resource")
	}
	root.analyzer.optimizationSymbolIdentityOverlay = nil
}

var optimizationSymbolIdentityCloneSink *Analyzer

func BenchmarkOptimizationSymbolIdentityOwnedConstruction(b *testing.B) {
	for _, size := range []int{0, 1024, 145742} {
		source := &Analyzer{optimizationSymbolIDs: map[*Symbol]string{}}
		for i := 0; i < size; i++ {
			source.optimizationSymbolIDs[&Symbol{Name: fmt.Sprint(i)}] = fmt.Sprintf("symbol@%d", i+1)
		}
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				worker := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
				if worker.optimizationSymbolIDs == nil || len(worker.optimizationSymbolIDs) != 0 {
					b.Fatal("eager copy")
				}
				optimizationSymbolIdentityCloneSink = worker
			}
			b.StopTimer()
			runtime.KeepAlive(source)
			runtime.KeepAlive(optimizationSymbolIdentityCloneSink)
		})
	}
}

func TestOptimizationSymbolIdentityOverlayDeclinedCopyStillCannotWriteCanonical(t *testing.T) {
	root, fn := privateSpecializationFixture(t)
	symbol := root.GlobalScope.Symbols["first"]
	delete(root.analyzer.optimizationSymbolIDs, symbol) // canonical setup before borrowing the stable base
	worker := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	context.syntax.err = fmt.Errorf("controlled unsupported syntax ownership")
	private := context.copyDecl(fn)
	if context.err == nil || private == fn {
		t.Fatal("fixture did not enter declined plain-rekey path")
	}
	before := maps.Clone(root.analyzer.optimizationSymbolIDs)
	value := worker.optimizationSymbolIdentity(symbol)
	if value == "" || !maps.Equal(before, root.analyzer.optimizationSymbolIDs) {
		t.Fatal("quiet work after declined syntax copy changed canonical identity map")
	}
	if got, present := worker.lookupOptimizationSymbolIdentity(symbol); !present || got != value {
		t.Fatal("declined-path local identity did not remain private/reusable")
	}
}

func TestOptimizationSymbolIdentityOverlayPublishedRealFactIDsRemainComplete(t *testing.T) {
	root := analyzeTreeTestSource(t, "identity-facts.elisa", `global values: i64[2] = [11, 22]
def pick[T](index: usize) -> i64:
    return values[index]
`)
	fn := root.File.Decls[1].(*ast.FuncDecl)
	original := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.IndexExpr)
	beforeFacts, present := root.LookupExprFacts(original.Object)
	if !present || beforeFacts.base == "" {
		t.Fatal("actual analyzed global object has no canonical base identity")
	}
	beforeIDs := maps.Clone(root.analyzer.optimizationSymbolIDs)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	private := product.Decl.Body[0].(*ast.ReturnStmt).Value.(*ast.IndexExpr)
	facts, present := product.Result.LookupExprFacts(private.Object)
	if !present || !reflect.DeepEqual(facts, beforeFacts) {
		t.Fatal("published real global facts lost full base/extent/flags")
	}
	if !maps.Equal(beforeIDs, root.analyzer.optimizationSymbolIDs) {
		t.Fatal("real fact publication wrote canonical identity map")
	}
	if product.Result.analyzer != nil {
		t.Fatal("construction resource escaped in published analyzer")
	}
}
