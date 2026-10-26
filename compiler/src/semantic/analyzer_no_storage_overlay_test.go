//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"maps"
	"reflect"
	"strings"
	"testing"
)

func TestNoStorageOverlayPositiveNegativeMissingAndIsolation(t *testing.T) {
	scalar := &BuiltinType{Name: "i64"}
	storage := &DArrayType{Elem: scalar}
	missing := &BuiltinType{Name: "u32"}
	root := &Analyzer{storeScanNoStorageCache: map[Type]bool{scalar: true, storage: false}}
	private := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if len(private.storeScanNoStorageCache) != 0 || private.noStorageOverlay == nil {
		t.Fatal("eager cache copy")
	}
	for typ, want := range root.storeScanNoStorageCache {
		got, present := private.lookupStoreScanNoStorage(typ)
		if !present || got != want {
			t.Fatal("inherited bool/membership lost")
		}
	}
	if _, present := private.lookupStoreScanNoStorage(missing); present {
		t.Fatal("missing entry became false")
	}
	if !private.storeScanHoldsNoRegionStorage(scalar) || private.storeScanHoldsNoRegionStorage(storage) {
		t.Fatal("actual producer changed inherited values")
	}
	if len(private.storeScanNoStorageCache) != 0 {
		t.Fatal("inherited lookup materialized hot rows")
	}
	private.setStoreScanNoStorage(scalar, false)
	if got, present := private.lookupStoreScanNoStorage(scalar); !present || got {
		t.Fatal("local false lost")
	}
	private.setStoreScanNoStorage(missing, true)
	if len(root.storeScanNoStorageCache) != 2 || !root.storeScanNoStorageCache[scalar] {
		t.Fatal("private write mutated base")
	}
	ordinary := cloneAnalyzerValueForBodyWorker(root).Addr().Interface().(*Analyzer)
	want := deepCloneValue(reflect.ValueOf(root.storeScanNoStorageCache)).Interface().(map[Type]bool)
	if ordinary.noStorageOverlay != nil || !reflect.DeepEqual(ordinary.storeScanNoStorageCache, want) {
		t.Fatal("ordinary policy changed")
	}
	ordinary.storeScanNoStorageCache[scalar] = false
	if !root.storeScanNoStorageCache[scalar] {
		t.Fatal("ordinary outer alias")
	}
	nested := cloneAnalyzerValueForBodyWorker(private).Addr().Interface().(*Analyzer)
	if nested.noStorageOverlay == private.noStorageOverlay {
		t.Fatal("overlay metadata shared")
	}
	nested.setStoreScanNoStorage(scalar, true)
	if got, _ := private.lookupStoreScanNoStorage(scalar); got {
		t.Fatal("nested local cache shared")
	}
	for _, table := range []map[Type]bool{nil, {}} {
		worker := cloneAnalyzerValueForSpecialization(&Analyzer{storeScanNoStorageCache: table}).Addr().Interface().(*Analyzer)
		if (worker.storeScanNoStorageCache == nil) != (table == nil) {
			t.Fatal("nil/empty shape changed")
		}
	}
}

func TestStoreScanSharesOverlayPositiveNegativeMissingAndIsolation(t *testing.T) {
	scalar := &BuiltinType{Name: "i64"}
	container := &DArrayType{Elem: scalar}
	newScalar := &BuiltinType{Name: "u32"}
	newContainer := &DictType{Key: scalar, Value: scalar}
	root := &Analyzer{storeScanSharesCache: map[Type]bool{scalar: false, container: true}}
	private := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if len(private.storeScanSharesCache) != 0 || private.noStorageOverlay == nil || private.noStorageOverlay.sharesBase == nil {
		t.Fatal("specialization copied the cache or lost its inherited base")
	}
	for typ, want := range root.storeScanSharesCache {
		got := private.storeScanSharesCached(typ)
		if got != want {
			t.Fatal("inherited positive/negative result changed")
		}
	}
	if len(private.storeScanSharesCache) != 0 {
		t.Fatal("inherited lookup materialized local rows")
	}
	if private.storeScanSharesCached(newScalar) || !private.storeScanSharesCached(newContainer) {
		t.Fatal("miss computation changed type-property results")
	}
	if len(private.storeScanSharesCache) != 2 || !private.storeScanSharesCache[newContainer] {
		t.Fatal("misses were not written to the private cache")
	}
	private.setStoreScanShares(newContainer, false)
	if !root.storeScanSharesCache[container] || len(root.storeScanSharesCache) != 2 {
		t.Fatal("private write mutated canonical cache")
	}

	nested := cloneAnalyzerValueForBodyWorker(private).Addr().Interface().(*Analyzer)
	if nested.noStorageOverlay == private.noStorageOverlay {
		t.Fatal("nested worker shared overlay metadata")
	}
	nested.setStoreScanShares(newContainer, true)
	if private.storeScanSharesCache[newContainer] || !nested.storeScanSharesCached(container) {
		t.Fatal("nested worker lost local isolation or inherited base lookup")
	}
}

func TestNoStorageOverlayActualMissAndCanonicalInvalidation(t *testing.T) {
	const source = "def keep(x: i64) -> i64:\n    return x\n"
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	types := []Type{&BuiltinType{Name: "i64"}, &DArrayType{Elem: &BuiltinType{Name: "i64"}}, &OptionalType{Value: &BuiltinType{Name: "i64"}}}
	worker := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	reference := &Analyzer{}
	for _, typ := range types {
		want := reference.storeScanHoldsNoRegionStorage(typ)
		got := worker.storeScanHoldsNoRegionStorage(typ)
		if got != want {
			t.Fatal("real producer result differs")
		}
	}
	worker.storeScanBinderOverlay = nil // isolate the no-storage invalidation boundary
	before := len(worker.diagnostics)
	worker.clearRetainedTypeMemos()
	if len(worker.diagnostics) <= before {
		t.Fatal("active overlay type invalidation admitted")
	}
	state.a.clearRetainedTypeMemos()
	if len(state.a.storeScanNoStorageCache) != 0 {
		t.Fatal("canonical invalidation changed")
	}
}
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
