//go:build cgo

package semantic

import (
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func binderOverlayTestContext(source *Analyzer) (*Analyzer, *specializationContext) {
	worker := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	context := &specializationContext{base: &Result{analyzer: source}, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	return worker, context
}

func TestStoreScanBinderOverlayExactRekeyAndIndependentCanonicalAliases(t *testing.T) {
	a, b, c := &ast.FuncDecl{Name: "a"}, &ast.FuncDecl{Name: "b"}, &ast.FuncDecl{Name: "c"}
	typ := &BuiltinType{Name: "i64"}
	shared := map[string]storeScanBinderVerdict{"positive": {typ, true}, "negative": {}}
	source := &Analyzer{storeScanBinderTypes: map[*ast.FuncDecl]map[string]storeScanBinderVerdict{a: shared, b: shared, nil: nil}}
	reference := deepCloneValue(reflect.ValueOf(source.storeScanBinderTypes)).Interface().(map[*ast.FuncDecl]map[string]storeScanBinderVerdict)
	worker, _ := binderOverlayTestContext(source)
	if worker.storeScanBinderTypes == nil || len(worker.storeScanBinderTypes) != 0 {
		t.Fatal("eager inherited clone")
	}
	worker.rekeyFuncDeclEntries(a, c, false)
	reference[c] = reference[a]
	worker.mutableStoreScanBinderCache(c)["private"] = storeScanBinderVerdict{typ, true}
	reference[c]["private"] = storeScanBinderVerdict{typ, true}
	if len(worker.storeScanBinderTypes) != 2 {
		t.Fatal("rekey did not acquire FROM before installing alias")
	}
	if !reflect.DeepEqual(worker.mutableStoreScanBinderCache(a), reference[a]) || !reflect.DeepEqual(worker.mutableStoreScanBinderCache(b), reference[b]) {
		t.Fatal("complete prior alias/value semantics changed")
	}
	if _, present := worker.mutableStoreScanBinderCache(b)["private"]; present {
		t.Fatal("distinct inherited keys were incorrectly coalesced by inner-map identity")
	}
	if _, present := shared["private"]; present {
		t.Fatal("canonical inherited map was mutated")
	}
	worker.rekeyFuncDeclEntries(a, c, true)
	if _, present := worker.ownedStoreScanBinderRow(a); present {
		t.Fatal("moved base row resurrected")
	}

	worker.mutableStoreScanBinderCache(a)["new"] = storeScanBinderVerdict{}
	if len(worker.mutableStoreScanBinderCache(a)) != 1 || len(worker.mutableStoreScanBinderCache(c)) != 3 {
		t.Fatal("write after move revived/changed old alias group")
	}
	if row, present := worker.ownedStoreScanBinderRow(nil); !present || row != nil {
		t.Fatal("present nil row changed")
	}

}

func TestStoreScanBinderOverlaySuccessfulImportThenReadDeclRekey(t *testing.T) {
	fn := &ast.FuncDecl{Name: "first", Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}}}
	typ := &BuiltinType{Name: "i64"}
	original := map[string]storeScanBinderVerdict{"known": {typ, true}, "negative": {}}
	source := &Analyzer{storeScanBinderTypes: map[*ast.FuncDecl]map[string]storeScanBinderVerdict{fn: original}}
	worker, context := binderOverlayTestContext(source)
	worker.mutableStoreScanBinderCache(fn)["local"] = storeScanBinderVerdict{}
	copied := context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	// The old successful copyFact import replaces the destination with a fresh
	// complete source map, detaching it from the earlier plain-rekey alias.
	if _, present := worker.mutableStoreScanBinderCache(copied)["local"]; present {
		t.Fatal("copyFact import failed to replace just destination")
	}
	if got, present := worker.mutableStoreScanBinderCache(copied)["known"]; !present || got.t != typ || !got.ok {
		t.Fatal("inherited source facts omitted in generic import")
	}
	if _, present := worker.mutableStoreScanBinderCache(copied)["negative"]; !present {
		t.Fatal("present negative fact erased")
	}
	// readDecl intentionally rekeys again AFTER copyDecl, restoring source/shadow
	// aliasing. The typed hook must preserve this observable order too.
	shadow := worker.readDecl(fn)
	worker.mutableStoreScanBinderCache(shadow)["after"] = storeScanBinderVerdict{}
	if _, present := worker.mutableStoreScanBinderCache(fn)["after"]; !present {
		t.Fatal("readDecl's later rekey lost source/shadow alias")
	}
	if len(original) != 2 {
		t.Fatal("successful import/rekey mutated canonical maps")
	}
}

func TestStoreScanBinderOverlayDeclinedCopyOwnsBeforeCachedHitAndSentinel(t *testing.T) {
	fn := &ast.FuncDecl{Name: "first"}
	typ := &BuiltinType{Name: "i64"}
	original := map[string]storeScanBinderVerdict{"known": {typ, true}, "negative": {}}
	source := &Analyzer{storeScanBinderTypes: map[*ast.FuncDecl]map[string]storeScanBinderVerdict{fn: original}}
	worker, context := binderOverlayTestContext(source)
	context.syntax.err = fmt.Errorf("controlled unsupported syntax")
	copied := worker.readDecl(fn)
	if context.err == nil || copied == fn {
		t.Fatal("did not enter declined copy/rekey")
	}
	if got, ok := worker.storeScanPatternBinderType(copied, "known"); !ok || got != typ {
		t.Fatal("exact inherited positive lost")
	}
	if got, ok := worker.storeScanPatternBinderType(copied, "negative"); ok || got != nil {
		t.Fatal("exact inherited negative lost")
	}
	before := maps.Clone(original)
	// Unknown is written as a recursion sentinel before uncached resolution.
	worker.storeScanPatternBinderType(copied, "unknown")
	if !maps.Equal(before, original) {
		t.Fatal("declined quiet path/sentinel wrote canonical inner map")
	}
	if _, present := worker.mutableStoreScanBinderCache(fn)["unknown"]; !present {
		t.Fatal("declined source/shadow alias did not preserve sentinel result")
	}
}

func TestStoreScanBinderOverlayResourceGuards(t *testing.T) {
	const source = "def identity[T](x: T) -> T:\n    return x\n"
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	worker := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	worker.exprTypeOverlay = nil
	worker.exprFactsOverlay = nil
	worker.iterBinderElementOverlay = nil
	worker.noStorageOverlay = nil
	worker.optimizationSymbolIdentityOverlay = nil
	worker.storeScanBinderOverlay.base = map[*ast.FuncDecl]map[string]storeScanBinderVerdict{}
	worker.diagnostics = nil
	worker.reportedDiagnostics = nil
	fn := result.File.Decls[0].(*ast.FuncDecl)
	args := []Type{&BuiltinType{Name: "i32"}}
	key, keyed := specializationMemoKey(fn, args)
	if !keyed {
		t.Fatal("missing real generic memo key")
	}
	cached := &SpecializedFunction{Decl: fn, Result: result}
	view := &Result{analyzer: worker, shared: &sharedCodegen{bodies: map[specializationKey]*SpecializedFunction{key: cached}}}
	if got, err := view.SpecializeFunction(fn, args); got != nil || err == nil || !strings.Contains(err.Error(), "store-scan binder overlay") {
		t.Fatal("memo bypassed isolated binder guard", err)
	}
	worker.diagnostics = nil
	worker.reportedDiagnostics = nil
	state.a.storeScanBinderOverlay = worker.storeScanBinderOverlay
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("marshal accepted resource")
	}
	if err := (&AnalysisBuild{retained: state, file: result.File, loweredFile: result.LoweredFile}).validate(); err == nil || !strings.Contains(err.Error(), "store-scan binder overlay") {
		t.Fatal("build accepted resource", err)
	}
	encoded, err := semanticStateRegistry().Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	if restored, err := RestoreAnalysisState(encoded); restored != nil || err == nil || !strings.Contains(err.Error(), "store-scan binder overlay") {
		t.Fatal("restore independently encoded resource", err)
	}
	state.a.storeScanBinderOverlay = nil
	if err := validateSparseFactValueType(reflect.ValueOf(worker.storeScanBinderOverlay)); err == nil {
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
	worker.diagnostics = nil
	worker.reportedDiagnostics = nil
	if len(worker.finishProgramResult(result.File, result.LoweredFile, nil, AnalyzeOptions{}, nil).Errors()) == 0 {
		t.Fatal("canonical publication accepted construction root")
	}
	worker.diagnostics = nil
	worker.reportedDiagnostics = nil
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

func TestStoreScanBinderOverlayActualInheritedAndRecursiveNegativeRows(t *testing.T) {
	root := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_binder_overlay.elisa", storeScanBinderSource, AnalyzeOptions{})
	fn := storeScanFindFunc(t, root, "label_of_block")
	typ, ok := root.analyzer.storeScanPatternBinderType(fn, "block_clause")
	if !ok || typ == nil {
		t.Fatal("actual canonical positive missing")
	}
	root.analyzer.storeScanPatternBinderType(fn, "absent")
	before := deepCloneValue(reflect.ValueOf(root.analyzer.storeScanBinderTypes)).Interface().(map[*ast.FuncDecl]map[string]storeScanBinderVerdict)
	worker, _ := binderOverlayTestContext(root.analyzer)
	got, ok := worker.storeScanPatternBinderType(fn, "block_clause")
	if !ok || got != typ {
		t.Fatal("actual inherited type changed")
	}
	if got, ok := worker.storeScanPatternBinderType(fn, "absent"); ok || got != nil {
		t.Fatal("actual inherited negative changed")
	}
	if !reflect.DeepEqual(before, root.analyzer.storeScanBinderTypes) {
		t.Fatal("real inherited queries wrote canonical rows")
	}
	cycle := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_binder_cycle.elisa", `enum Box:
    Item(value: i64)
def cycle() -> void:
    match item:
        Box.Item(item):
            pass
`, AnalyzeOptions{})
	cycle.analyzer.storeScanBinderTypes = nil // canonical fixture setup, before cloning
	cycleFn := storeScanFindFunc(t, cycle, "cycle")
	cyclic, _ := binderOverlayTestContext(cycle.analyzer)
	if typ, ok := cyclic.storeScanPatternBinderType(cycleFn, "item"); ok || typ != nil {
		t.Fatal("recursive subject should remain unknown")
	}
	if len(cycle.analyzer.storeScanBinderTypes) != 0 {
		t.Fatal("recursive sentinel reached canonical cache")
	}
}

var binderOverlayCloneSink *Analyzer

func BenchmarkStoreScanBinderOwnedConstruction(b *testing.B) {
	for _, count := range []int{0, 5013} {
		source := &Analyzer{storeScanBinderTypes: map[*ast.FuncDecl]map[string]storeScanBinderVerdict{}}
		for i := 0; i < count; i++ {
			source.storeScanBinderTypes[&ast.FuncDecl{Name: fmt.Sprint(i)}] = map[string]storeScanBinderVerdict{"known": {&BuiltinType{Name: "i64"}, true}, "negative": {}}
		}
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				binderOverlayCloneSink = cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
				if binderOverlayCloneSink.storeScanBinderTypes == nil || len(binderOverlayCloneSink.storeScanBinderTypes) != 0 {
					b.Fatal("eager inherited copy")
				}
			}
			b.StopTimer()
			runtime.KeepAlive(source)
			runtime.KeepAlive(binderOverlayCloneSink)
		})
	}
}

func TestStoreScanBinderOverlayNilEmptyAndOrdinaryIsolation(t *testing.T) {
	for _, table := range []map[*ast.FuncDecl]map[string]storeScanBinderVerdict{nil, {}} {
		root := &Analyzer{storeScanBinderTypes: table}
		owned := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
		ordinary := cloneAnalyzerValueForBodyWorker(root).Addr().Interface().(*Analyzer)
		if (owned.storeScanBinderTypes == nil) != (table == nil) || (ordinary.storeScanBinderTypes == nil) != (table == nil) || ordinary.storeScanBinderOverlay != nil {
			t.Fatal("nil/empty or ordinary policy changed")
		}
	}
	fn := &ast.FuncDecl{Name: "first"}
	source := &Analyzer{storeScanBinderTypes: map[*ast.FuncDecl]map[string]storeScanBinderVerdict{fn: {"negative": {}}}}
	ordinary := cloneAnalyzerValueForBodyWorker(source).Addr().Interface().(*Analyzer)
	ordinary.mutableStoreScanBinderCache(fn)["ordinary"] = storeScanBinderVerdict{}
	if len(source.storeScanBinderTypes[fn]) != 1 {
		t.Fatal("ordinary worker nested map was shared")
	}
}
