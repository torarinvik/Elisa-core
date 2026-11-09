//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
)

func TestNoStorageOverlayResourceGuards(t *testing.T) {
	const source = "def keep[T](x: T) -> T:\n    return x\n"
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	worker := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	worker.exprTypeOverlay = nil
	worker.exprFactsOverlay = nil
	worker.iterBinderElementOverlay = nil
	worker.optimizationSymbolIdentityOverlay = nil
	state.a.noStorageOverlay = worker.noStorageOverlay
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("active overlay persisted")
	}
	if err := (&AnalysisBuild{retained: state, file: result.File, loweredFile: result.LoweredFile}).validate(); err == nil || !strings.Contains(err.Error(), "no-storage overlay") {
		t.Fatal("active build admitted", err)
	}
	encoded, err := semanticStateRegistry().Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	if restored, err := RestoreAnalysisState(encoded); restored != nil || err == nil || !strings.Contains(err.Error(), "no-storage overlay") {
		t.Fatal("decoded overlay admitted", err)
	}
	state.a.noStorageOverlay = nil
	if err := validateSparseFactValueType(reflect.ValueOf(worker.noStorageOverlay)); err == nil {
		t.Fatal("resource entered public facts")
	}
	panicked := func() (p bool) {
		defer func() { p = recover() != nil }()
		cloneAnalyzerValueForSpecialization(worker)
		return false
	}()
	if !panicked {
		t.Fatal("nested specialization clone admitted")
	}
	fn := result.File.Decls[0].(*ast.FuncDecl)
	args := []Type{&BuiltinType{Name: "i64"}}
	key, keyed := specializationMemoKey(fn, args)
	if !keyed {
		t.Fatal("fixture lacks memo key")
	}
	construction := &Result{analyzer: worker, shared: &sharedCodegen{bodies: map[specializationKey]*SpecializedFunction{key: {Decl: fn}}}}
	if got, err := construction.SpecializeFunction(fn, args); got != nil || err == nil {
		t.Fatal("memo hit bypassed canonical resource guard")
	}
	if len(worker.finishProgramResult(result.File, result.LoweredFile, nil, AnalyzeOptions{}, nil).Errors()) == 0 {
		t.Fatal("canonical publication admitted")
	}
	mergeValue(reflect.ValueOf(state.a).Elem(), reflect.ValueOf(worker).Elem(), reflect.ValueOf(state.a).Elem(), nil)
	if len(state.a.diagnostics) == 0 {
		t.Fatal("canonical merge admitted")
	}
	before := len(worker.diagnostics)
	worker.analyzeDeclsParallel(nil, 2)
	if len(worker.diagnostics) <= before {
		t.Fatal("scheduler admitted")
	}
}

var noStorageCloneSink *Analyzer

func BenchmarkNoStorageOverlayConstruction(b *testing.B) {
	root := &Analyzer{storeScanNoStorageCache: map[Type]bool{}}
	for i := 0; i < 32768; i++ {
		root.storeScanNoStorageCache[&BuiltinType{Name: "i64"}] = i%2 == 0
	}
	for _, mode := range []string{"old_map_copy", "overlay"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "old_map_copy" {
					noStorageCloneSink = &Analyzer{storeScanNoStorageCache: maps.Clone(root.storeScanNoStorageCache)}
				} else {
					noStorageCloneSink = &Analyzer{storeScanNoStorageCache: map[Type]bool{}, noStorageOverlay: &analyzerNoStorageOverlay{base: root.storeScanNoStorageCache}}
				}
			}
		})
	}
}

var storeScanSharesCloneSink *Analyzer

func BenchmarkStoreScanSharesOverlayConstruction(b *testing.B) {
	root := &Analyzer{storeScanSharesCache: make(map[Type]bool, 32768)}
	for i := 0; i < 32768; i++ {
		root.storeScanSharesCache[&BuiltinType{Name: "i64"}] = i%2 == 0
	}
	for _, mode := range []string{"full_map_copy", "overlay"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "full_map_copy" {
					storeScanSharesCloneSink = cloneAnalyzerValueForParallelPolicy(root, true, false).Addr().Interface().(*Analyzer)
				} else {
					storeScanSharesCloneSink = cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
				}
			}
		})
	}
}

var storeScanViewOnlyCloneSink *Analyzer

func BenchmarkSpecializationStoreScanViewOnlyCacheClone(b *testing.B) {
	const entries = 32768
	cache := make(map[Type]bool, entries)
	for i := 0; i < entries; i++ {
		cache[&BuiltinType{Name: fmt.Sprintf("i64_%d", i)}] = i%2 == 0
	}
	root := &Analyzer{storeScanViewOnlyCache: cache}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		storeScanViewOnlyCloneSink = cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	}
}

var compilerBuiltinHelperCloneSink *Analyzer

func BenchmarkCompilerBuiltinHelperCallsOverlayConstruction(b *testing.B) {
	root := &Analyzer{compilerBuiltinHelperCalls: make(map[*ast.CallExpr]string, 32768)}
	for i := 0; i < 32768; i++ {
		root.compilerBuiltinHelperCalls[&ast.CallExpr{}] = "freeze"
	}
	for _, mode := range []string{"full_map_copy", "overlay"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "full_map_copy" {
					compilerBuiltinHelperCloneSink = cloneAnalyzerValueForParallelPolicy(root, true, false).Addr().Interface().(*Analyzer)
				} else {
					compilerBuiltinHelperCloneSink = cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
				}
			}
		})
	}
}

var returnElementUnknownCloneSink *Analyzer

func BenchmarkReturnElementUnknownOverlayConstruction(b *testing.B) {
	root := &Analyzer{returnElementStmtUnknown: make(map[*ast.ReturnStmt]bool, 32768)}
	for i := 0; i < 32768; i++ {
		root.returnElementStmtUnknown[&ast.ReturnStmt{}] = true
	}
	for _, mode := range []string{"full_map_copy", "overlay"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "full_map_copy" {
					returnElementUnknownCloneSink = cloneAnalyzerValueForParallelPolicy(root, true, false).Addr().Interface().(*Analyzer)
				} else {
					returnElementUnknownCloneSink = cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
				}
			}
		})
	}
}

func TestContainerRegionMemoOverlayIsolation(t *testing.T) {
	inherited := &DArrayType{Region: "outer"}
	empty := &BuiltinType{Name: "i64"}
	fresh := &DArrayType{Region: "local"}
	root := &Analyzer{typeShapesFrozen: true, containerRegionMemo: map[Type]string{inherited: "outer", empty: ""}, storeScanViewOnlyCache: map[Type]bool{empty: true}}
	worker := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if len(worker.containerRegionMemo) != 0 {
		t.Fatal("eagerly copied inherited rows")
	}
	if got := worker.containerRegionOf(inherited); got != "outer" {
		t.Fatalf("inherited region = %q", got)
	}
	if got := worker.containerRegionOf(empty); got != "" || len(worker.containerRegionMemo) != 0 {
		t.Fatal("empty inherited region not cached")
	}
	if got := worker.containerRegionOf(fresh); got != "local" {
		t.Fatalf("new region = %q", got)
	}
	worker.containerRegionMemo[inherited] = "private"
	if root.containerRegionMemo[inherited] != "outer" {
		t.Fatal("private write changed base")
	}
	nested := cloneAnalyzerValueForBodyWorker(worker).Addr().Interface().(*Analyzer)
	if got := nested.containerRegionOf(inherited); got != "private" {
		t.Fatalf("nested local = %q", got)
	}
	if got, ok := nested.lookupStoreScanViewOnly(empty); !ok || !got {
		t.Fatal("nested view-only base lost")
	}
	if got := nested.containerRegionOf(fresh); got != "local" {
		t.Fatalf("nested new = %q", got)
	}
	nested.typeShapesFrozen = false
	if got := nested.containerRegionOf(inherited); got != "outer" {
		t.Fatalf("unfrozen lookup used memo: %q", got)
	}
}

func TestStoreScanModeOverlayIsolation(t *testing.T) {
	original := &ast.FuncDecl{Name: "original"}
	inherited := storeScanKey{decl: original, other: 0, index: 1}
	zero := storeScanKey{decl: original, other: 0, index: 2}
	fresh := storeScanKey{decl: &ast.FuncDecl{Name: "fresh"}, other: 0, index: 1}
	root := &Analyzer{storeScanCache: map[storeScanKey]storeMode{inherited: storeHeader, zero: storeNone}}
	worker := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if len(worker.storeScanCache) != 0 {
		t.Fatal("eagerly copied store mode cache")
	}
	if got, ok := worker.lookupStoreScanMode(inherited); !ok || got != storeHeader {
		t.Fatal("inherited mode lost")
	}
	if got, ok := worker.lookupStoreScanMode(zero); !ok || got != storeNone {
		t.Fatal("inherited zero entry lost")
	}
	if _, ok := worker.lookupStoreScanMode(fresh); ok {
		t.Fatal("missing entry became present")
	}
	worker.cacheStoreScan(inherited, storeNone)
	worker.cacheStoreScan(fresh, storeHeader)
	if got, _ := worker.lookupStoreScanMode(inherited); got != storeNone || root.storeScanCache[inherited] != storeHeader {
		t.Fatal("private override leaked")
	}
	nested := cloneAnalyzerValueForBodyWorker(worker).Addr().Interface().(*Analyzer)
	if got, ok := nested.lookupStoreScanMode(zero); !ok || got != storeNone {
		t.Fatal("nested inherited entry lost")
	}
	if got, ok := nested.lookupStoreScanMode(inherited); !ok || got != storeNone {
		t.Fatal("nested private entry lost")
	}
}

func TestSpecializationStoreScanCacheSharesOnlyForeignModes(t *testing.T) {
	target := &ast.FuncDecl{Name: "specialized"}
	foreign := &ast.FuncDecl{Name: "foreign"}
	privateTarget := &ast.FuncDecl{Name: "specialized-private"}
	privateForeign := &ast.FuncDecl{Name: "foreign-private"}
	foreignKey := storeScanKey{decl: foreign, other: 0, index: 1}
	targetKey := storeScanKey{decl: target, other: 0, index: 1}
	shared := &sharedCodegen{storeScanModes: map[storeScanKey]storeMode{foreignKey: storeViews}}
	context := &specializationContext{base: &Result{shared: shared}, targetDecl: target}
	worker := &Analyzer{
		noStorageOverlay: &analyzerNoStorageOverlay{storeModes: map[storeScanKey]storeMode{
			foreignKey: storeElements,
			targetKey:  storeHeader,
		}},
		parallel: &parallelWorkerState{
			original:       map[*ast.FuncDecl]*ast.FuncDecl{privateTarget: target, privateForeign: foreign},
			specialization: context,
		},
	}
	if mode, ok := worker.lookupStoreScanMode(storeScanKey{decl: privateForeign, other: 0, index: 1}); !ok || mode != storeElements {
		t.Fatalf("foreign canonical overlay lookup = %v, %v", mode, ok)
	}
	if _, ok := worker.lookupStoreScanMode(storeScanKey{decl: privateTarget, other: 0, index: 1}); ok {
		t.Fatal("specialized root reused its canonical store mode")
	}
	newForeignKey := storeScanKey{decl: privateForeign, other: 1, index: 0}
	worker.publishSpecializationStoreScanMode(newForeignKey, storeHeader)
	if mode, ok := shared.lookupStoreScanMode(storeScanKey{decl: foreign, other: 1, index: 0}); !ok || mode != storeHeader {
		t.Fatalf("foreign specialization result was not published canonically: %v, %v", mode, ok)
	}
	worker.publishSpecializationStoreScanMode(storeScanKey{decl: privateTarget, other: 1, index: 0}, storeNone)
	if _, ok := shared.lookupStoreScanMode(storeScanKey{decl: target, other: 1, index: 0}); ok {
		t.Fatal("specialized root result leaked into the shared canonical cache")
	}
	worker.noteSpecializationStoreScanTarget(privateTarget)
	if !context.storeScanTargetHit {
		t.Fatal("read-only foreign store proof did not record dependence on the specialized root")
	}
}
