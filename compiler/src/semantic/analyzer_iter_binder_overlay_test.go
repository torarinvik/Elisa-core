//go:build cgo

package semantic

import (
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestIterBinderOverlayExactCopyAndMembership(t *testing.T) {
	symbol, other, missing := &Symbol{}, &Symbol{}, &Symbol{}
	shared := map[*Symbol]regionDependencyState{symbol: {Generation: 7, Valid: true, InvalidatedBy: "cause"}}
	backing := make([]uint64, 2, 8)
	backing[0] = 23
	value := regionRefState{Deps: shared, StoreDeps: map[*Symbol]packedStoreDependencyState{symbol: {}}, DirectParamDep: 4, HasDirectParamDep: true, ParamDeps: IntBitSet{extra: backing}, Fields: map[string]regionRefState{"nested": {Deps: shared}}, PackedStoreSummaryKnown: true, ParamOnlySummary: true}
	base := map[*Symbol]regionRefState{symbol: value, other: value, nil: {}}
	root := &Analyzer{iterBinderElementStates: base}
	worker := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if len(worker.iterBinderElementStates) != 0 || worker.iterBinderElementOverlay == nil {
		t.Fatal("eager construction copy")
	}
	reference := deepCloneValue(reflect.ValueOf(base)).Interface().(map[*Symbol]regionRefState)
	got, ok := worker.lookupIterBinderElementState(symbol)
	if !ok || !reflect.DeepEqual(got, reference[symbol]) {
		t.Fatal("full old-policy value mismatch")
	}
	if _, ok := worker.lookupIterBinderElementState(missing); ok || len(worker.iterBinderElementStates) != 1 {
		t.Fatal("missing became a present zero row")
	}
	if _, ok := worker.lookupIterBinderElementState(nil); !ok {
		t.Fatal("nil-key present zero row lost")
	}
	if cap(got.ParamDeps.extra) != len(backing) || &got.ParamDeps.extra[0] != &backing[0] {
		t.Fatal("old slice alias/cap policy changed")
	}
	got.Deps[symbol] = regionDependencyState{Generation: 99}
	got.Fields["added"] = regionRefState{}
	nested := got.Fields["nested"]
	nested.Deps[symbol] = regionDependencyState{Generation: 111}
	again, _ := worker.lookupIterBinderElementState(symbol)
	untouched, _ := worker.lookupIterBinderElementState(other)
	if again.Deps[symbol].Generation != 99 || len(again.Fields) != 2 || untouched.Deps[symbol].Generation != 7 || base[symbol].Deps[symbol].Generation != 7 {
		t.Fatal("cached identity or inline isolation violated")
	}
	if again.Deps[symbol].Generation == nested.Deps[symbol].Generation {
		t.Fatal("old independent nested-copy behavior changed")
	}
	worker.deleteIterBinderElementState(symbol)
	if _, ok := worker.lookupIterBinderElementState(symbol); ok {
		t.Fatal("deleted base row resurrected")
	}
	worker.setIterBinderElementState(symbol, regionRefState{})
	if _, ok := worker.lookupIterBinderElementState(symbol); !ok {
		t.Fatal("write after delete missing")
	}
	clone := cloneAnalyzerValueForBodyWorker(worker).Addr().Interface().(*Analyzer)
	clone.deleteIterBinderElementState(other)
	if _, ok := worker.lookupIterBinderElementState(other); !ok {
		t.Fatal("ordinary clone shares tombstones")
	}
}

func TestIterBinderOverlayNilAndOrdinaryClone(t *testing.T) {
	for _, table := range []map[*Symbol]regionRefState{nil, {}} {
		root := &Analyzer{iterBinderElementStates: table}
		worker := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
		ordinary := cloneAnalyzerValueForBodyWorker(root).Addr().Interface().(*Analyzer)
		if (worker.iterBinderElementStates == nil) != (table == nil) || (ordinary.iterBinderElementStates == nil) != (table == nil) || ordinary.iterBinderElementOverlay != nil {
			t.Fatal("map shape or ordinary policy changed")
		}
	}
	symbol := &Symbol{}
	root := &Analyzer{iterBinderElementStates: map[*Symbol]regionRefState{symbol: {Deps: map[*Symbol]regionDependencyState{symbol: {Generation: 1}}}}}
	ordinary := cloneAnalyzerValueForBodyWorker(root).Addr().Interface().(*Analyzer)
	row := ordinary.iterBinderElementStates[symbol]
	row.Deps[symbol] = regionDependencyState{Generation: 2}
	if root.iterBinderElementStates[symbol].Deps[symbol].Generation != 1 {
		t.Fatal("ordinary clone isolation changed")
	}
}

func TestIterBinderOverlayResourceGuards(t *testing.T) {
	const source = "def identity(x: i32) -> i32:\n    return x\n"
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	worker := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	worker.exprTypeOverlay = nil
	worker.exprFactsOverlay = nil
	worker.iterBinderElementOverlay.base = map[*Symbol]regionRefState{}
	state.a.iterBinderElementOverlay = worker.iterBinderElementOverlay
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("marshal accepted resource")
	}
	if err := (&AnalysisBuild{retained: state, file: result.File, loweredFile: result.LoweredFile}).validate(); err == nil || !strings.Contains(err.Error(), "iteration-binder overlay") {
		t.Fatal("build accepted resource", err)
	}
	encoded, err := semanticStateRegistry().Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	if restored, err := RestoreAnalysisState(encoded); restored != nil || err == nil || !strings.Contains(err.Error(), "iteration-binder overlay") {
		t.Fatal("restore independently encoded resource", err)
	}
	state.a.iterBinderElementOverlay = nil
	if err := validateSparseFactValueType(reflect.ValueOf(worker.iterBinderElementOverlay)); err == nil {
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

func TestIterBinderOverlayActualAnalyzedLoopRows(t *testing.T) {
	const source = `def length(name: sview) -> i64:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview] = [name]
        total: mutable i64 = 0
        for value in names |total|:
            total <- total + value.len
        return total
`
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatal(errors)
	}
	if len(state.a.iterBinderElementStates) == 0 {
		t.Fatalf("actual loop fixture produced no tracked iteration rows: %v", result.Errors())
	}
	worker := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	reference := deepCloneValue(reflect.ValueOf(state.a.iterBinderElementStates)).Interface().(map[*Symbol]regionRefState)
	for symbol, want := range reference {
		got, present := worker.lookupIterBinderElementState(symbol)
		if !present || !reflect.DeepEqual(got, want) {
			t.Fatal("actual analyzed loop provenance differs from old clone")
		}
	}
}

func TestIterBinderOverlayActualPrivateLoopConstruction(t *testing.T) {
	const source = `def gather[T](tag: T, name: sview) -> i64:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview] = [name]
        held: mutable darray[sview] = []
        for value in names |held| -> held:
            held <- held.push(value)
        return held[0].len
`
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	if len(state.a.iterBinderElementStates) == 0 {
		t.Fatal("canonical generic loop produced no tracked rows")
	}
	before := deepCloneValue(reflect.ValueOf(state.a.iterBinderElementStates)).Interface().(map[*Symbol]regionRefState)
	worker := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	isolateWorkerScopes(state.a, worker)
	context := &specializationContext{base: result, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	fn := retainedProofNamedDecl(t, result, "gather").(*ast.FuncDecl)
	private := context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	defer worker.closeSMT()
	worker.analyzeFuncWithTypeArgs(private, []Type{result.NamedTypes["i64"]})
	for _, diagnostic := range worker.diagnostics {
		if diagnostic.Severity == DiagnosticSeverityError {
			t.Fatal(diagnostic)
		}
	}
	if len(worker.iterBinderElementStates) == 0 {
		t.Fatal("actual concrete loop did not produce private tracked rows")
	}
	if !reflect.DeepEqual(state.a.iterBinderElementStates, before) {
		t.Fatal("concrete loop changed canonical provenance")
	}
}

func TestIterBindingSourceOverlayIsolation(t *testing.T) {
	inherited, missing, added := &Symbol{}, &Symbol{}, &Symbol{}
	expression := &ast.Ident{Name: "source"}
	root := &Analyzer{iterBindingSources: map[*Symbol]ast.Expr{inherited: expression, nil: nil}}
	worker := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if len(worker.iterBindingSources) != 0 {
		t.Fatal("specialization copied source rows")
	}
	if got, ok := worker.iterBindingSource(inherited); !ok || got != expression {
		t.Fatal("inherited source missing")
	}
	if got, ok := worker.iterBindingSource(nil); !ok || got != nil {
		t.Fatal("inherited nil row missing")
	}
	if _, ok := worker.iterBindingSource(missing); ok {
		t.Fatal("missing row present")
	}
	worker.iterBindingSources[inherited] = nil
	worker.iterBindingSources[added] = expression
	if got, ok := worker.iterBindingSource(inherited); !ok || got != nil {
		t.Fatal("nil override lost")
	}
	if root.iterBindingSources[inherited] != expression || len(root.iterBindingSources) != 2 {
		t.Fatal("private write changed canonical table")
	}
	ordinary := cloneAnalyzerValueForBodyWorker(root).Addr().Interface().(*Analyzer)
	if ordinary.iterBinderElementOverlay != nil || ordinary.iterBindingSources[inherited] != expression {
		t.Fatal("ordinary clone changed")
	}
}
