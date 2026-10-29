//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"
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

func TestStoreScanViewOnlyOverlayPositiveNegativeMissingAndIsolation(t *testing.T) {
	viewOnly := &BuiltinType{Name: "i64"}
	notViewOnly := &DArrayType{Elem: viewOnly}
	missing := &OptionalType{Value: viewOnly}
	root := &Analyzer{storeScanViewOnlyCache: map[Type]bool{viewOnly: true, notViewOnly: false}}
	private := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if private.storeScanViewOnlyCache == nil || len(private.storeScanViewOnlyCache) != 0 || private.noStorageOverlay == nil || private.noStorageOverlay.viewOnlyBase == nil {
		t.Fatal("specialization copied the view-only cache or lost its inherited base")
	}
	if !private.storeScanViewOnlyCached(viewOnly) || private.storeScanViewOnlyCached(notViewOnly) {
		t.Fatal("inherited positive/negative view-only results changed")
	}
	if len(private.storeScanViewOnlyCache) != 0 {
		t.Fatal("inherited lookup materialized local rows")
	}
	wantMissing := storeScanTypeIsViewOnly(missing)
	if got := private.storeScanViewOnlyCached(missing); got != wantMissing {
		t.Fatal("missing entry computed a different type-property result")
	}
	if len(private.storeScanViewOnlyCache) != 1 || private.storeScanViewOnlyCache[missing] != wantMissing {
		t.Fatal("missing result was not written to the private cache")
	}
	private.setStoreScanViewOnly(viewOnly, false)
	if private.storeScanViewOnlyCached(viewOnly) || !root.storeScanViewOnlyCache[viewOnly] {
		t.Fatal("private write mutated the canonical cache or failed to override the base")
	}

	ordinary := cloneAnalyzerValueForBodyWorker(root).Addr().Interface().(*Analyzer)
	if ordinary.noStorageOverlay != nil || len(ordinary.storeScanViewOnlyCache) != 2 {
		t.Fatal("ordinary body worker stopped owning a full view-only cache")
	}
	ordinary.storeScanViewOnlyCache[viewOnly] = false
	if !root.storeScanViewOnlyCache[viewOnly] {
		t.Fatal("ordinary body-worker mutation reached the source cache")
	}

	for _, source := range []map[Type]bool{nil, {}} {
		clone := cloneAnalyzerValueForSpecialization(&Analyzer{storeScanViewOnlyCache: source}).Addr().Interface().(*Analyzer).storeScanViewOnlyCache
		if (clone == nil) != (source == nil) {
			t.Fatal("nil/empty cache shape changed")
		}
	}
}

func TestStoreScanParamWriteOverlayPositiveNegativeAndIsolation(t *testing.T) {
	decl := &ast.FuncDecl{Name: "memoized"}
	positive := storeScanParamKey{decl: decl, index: 0, mode: storeScanModeWrite}
	negative := storeScanParamKey{decl: decl, index: 1, mode: storeScanModeWrite}
	missing := storeScanParamKey{decl: decl, index: 2, mode: storeScanModeWrite}
	root := &Analyzer{storeScanParamWriteCache: map[storeScanParamKey]bool{positive: true, negative: false}}
	worker := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if len(worker.storeScanParamWriteCache) != 0 || worker.noStorageOverlay == nil || len(worker.noStorageOverlay.paramWrites) != 2 {
		t.Fatal("specialization copied the reachability cache or lost its base")
	}
	if value, present := worker.lookupStoreScanParamWriteCache(positive); !present || !value {
		t.Fatal("inherited positive reachability lost")
	}
	if value, present := worker.lookupStoreScanParamWriteCache(negative); !present || value {
		t.Fatal("inherited negative reachability lost")
	}
	if value, present := worker.lookupStoreScanParamWriteCache(missing); present || value {
		t.Fatal("missing reachability became present")
	}
	if !worker.storeScanParamReaches(positive) || worker.storeScanParamReaches(negative) {
		t.Fatal("reachability query bypassed inherited memo rows")
	}
	if !worker.storeScanParamReaches(missing) {
		t.Fatal("reachability miss did not compute a private result")
	}
	worker.setStoreScanParamWriteCache(positive, false)
	if value, present := worker.lookupStoreScanParamWriteCache(positive); !present || value {
		t.Fatal("local false did not override inherited true")
	}
	if value, present := worker.lookupStoreScanParamWriteCache(missing); !present || !value {
		t.Fatal("private reachability write was not cached")
	}
	if !root.storeScanParamWriteCache[positive] || len(root.storeScanParamWriteCache) != 2 {
		t.Fatal("private reachability write mutated the canonical base")
	}
}

func TestCompilerBuiltinHelperCallsOverlayAndCopyDeclRekey(t *testing.T) {
	originalCall, originalMissing := &ast.CallExpr{}, &ast.CallExpr{}
	rootAnalyzer := &Analyzer{compilerBuiltinHelperCalls: map[*ast.CallExpr]string{originalCall: "freeze"}}
	worker := cloneAnalyzerValueForSpecialization(rootAnalyzer).Addr().Interface().(*Analyzer)
	if len(worker.compilerBuiltinHelperCalls) != 0 || worker.noStorageOverlay == nil || len(worker.noStorageOverlay.helperCalls) != 1 {
		t.Fatal("specialization copied the whole helper table or lost its canonical base")
	}
	if !worker.isCompilerBuiltinHelperCall(originalCall, "freeze") || worker.isCompilerBuiltinHelperCall(originalMissing, "freeze") {
		t.Fatal("canonical inherited helper lookup changed")
	}

	fn := &ast.FuncDecl{Name: "target", Body: []ast.Stmt{
		&ast.ExprStmt{Expr: originalCall},
		&ast.ExprStmt{Expr: originalMissing},
	}}
	base := &Result{analyzer: rootAnalyzer}
	ctx := &specializationContext{base: base, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{specialization: ctx, original: map[*ast.FuncDecl]*ast.FuncDecl{}}
	privateDecl := ctx.copyDecl(fn)
	if ctx.err != nil || privateDecl == nil {
		t.Fatal("copyDecl failed", ctx.err)
	}
	privateCall := privateDecl.Body[0].(*ast.ExprStmt).Expr.(*ast.CallExpr)
	privateMissing := privateDecl.Body[1].(*ast.ExprStmt).Expr.(*ast.CallExpr)
	if privateCall == originalCall || !worker.isCompilerBuiltinHelperCall(privateCall, "freeze") {
		t.Fatal("copyDecl did not rekey the inherited helper fact onto private syntax")
	}
	if worker.isCompilerBuiltinHelperCall(privateMissing, "freeze") {
		t.Fatal("an owned missing helper fact fell back to a canonical identity")
	}
	worker.recordCompilerBuiltinHelperCall(privateCall, "private-freeze")
	if !worker.isCompilerBuiltinHelperCall(privateCall, "private-freeze") || !rootAnalyzer.isCompilerBuiltinHelperCall(originalCall, "freeze") {
		t.Fatal("private override mutated or failed to shadow canonical helper facts")
	}

	nested := cloneAnalyzerValueForBodyWorker(worker).Addr().Interface().(*Analyzer)
	if nested.noStorageOverlay == worker.noStorageOverlay || !nested.isCompilerBuiltinHelperCall(privateCall, "private-freeze") || !nested.isCompilerBuiltinHelperCall(originalCall, "freeze") {
		t.Fatal("nested worker lost local/base helper lookup or shared overlay metadata")
	}
	nested.recordCompilerBuiltinHelperCall(privateCall, "nested")
	if !worker.isCompilerBuiltinHelperCall(privateCall, "private-freeze") {
		t.Fatal("nested worker write reached its parent")
	}
}

func TestRetainedBodyEditInvalidatesStoreScanParamWriteCache(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const before = `def touch(xs: mutable darray[i64]&) -> void:
    return
`
	const after = `def touch(xs: mutable darray[i64]&) -> void:
    can Abort.Panic:
        xs[0] <- 1
`
	result, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	fn := retainedProofNamedDecl(t, result, "touch").(*ast.FuncDecl)
	if state.a.storeScanParamMayBeWritten(fn, 0) {
		t.Fatal("no-op function unexpectedly writes its parameter")
	}
	if len(state.a.storeScanParamWriteCache) == 0 || len(state.a.storeScanParamFactsCache) == 0 {
		t.Fatal("fixture did not populate the reachability memo")
	}
	updated, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Errors()) != 0 {
		t.Fatal(updated.Errors())
	}
	fn = retainedProofNamedDecl(t, updated, "touch").(*ast.FuncDecl)
	if !state.a.storeScanParamMayBeWritten(fn, 0) {
		t.Fatal("edited function retained the no-write verdict from its old body")
	}
}

func TestCompilerBuiltinHelperCallsOverlayConcurrentPrivateMaps(t *testing.T) {
	baseCall := &ast.CallExpr{}
	root := &Analyzer{compilerBuiltinHelperCalls: map[*ast.CallExpr]string{baseCall: "freeze"}}
	const workers = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			worker := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
			local := &ast.CallExpr{}
			worker.recordCompilerBuiltinHelperCall(local, "local")
			if !worker.isCompilerBuiltinHelperCall(baseCall, "freeze") || !worker.isCompilerBuiltinHelperCall(local, "local") {
				t.Error("concurrent private helper lookup failed")
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(root.compilerBuiltinHelperCalls) != 1 {
		t.Fatal("concurrent private helper writes reached the canonical base")
	}
}

func TestReturnElementUnknownOverlayAndCopyDeclRekey(t *testing.T) {
	original := &ast.ReturnStmt{Value: &ast.Ident{Name: "value"}}
	originalMissing := &ast.ReturnStmt{Value: &ast.Ident{Name: "other"}}
	rootAnalyzer := &Analyzer{returnElementStmtUnknown: map[*ast.ReturnStmt]bool{original: true}}
	worker := cloneAnalyzerValueForSpecialization(rootAnalyzer).Addr().Interface().(*Analyzer)
	if len(worker.returnElementStmtUnknown) != 0 || worker.noStorageOverlay == nil || len(worker.noStorageOverlay.returnUnknown) != 1 {
		t.Fatal("specialization copied all return rows or lost their canonical base")
	}
	if unknown, present := worker.lookupReturnElementStmtUnknown(original); !present || !unknown {
		t.Fatal("inherited unknown return row lost")
	}
	if unknown, present := worker.lookupReturnElementStmtUnknown(originalMissing); present || unknown {
		t.Fatal("missing return row became present")
	}

	fn := &ast.FuncDecl{Name: "target", Body: []ast.Stmt{original, originalMissing}}
	ctx := &specializationContext{base: &Result{analyzer: rootAnalyzer}, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{specialization: ctx, original: map[*ast.FuncDecl]*ast.FuncDecl{}}
	privateDecl := ctx.copyDecl(fn)
	if ctx.err != nil || privateDecl == nil {
		t.Fatal("copyDecl failed", ctx.err)
	}
	private := privateDecl.Body[0].(*ast.ReturnStmt)
	privateMissing := privateDecl.Body[1].(*ast.ReturnStmt)
	if private == original {
		t.Fatal("return syntax was not detached")
	}
	if unknown, present := worker.lookupReturnElementStmtUnknown(private); !present || !unknown {
		t.Fatal("copyDecl did not rekey the inherited return fact")
	}
	if unknown, present := worker.lookupReturnElementStmtUnknown(privateMissing); present || unknown {
		t.Fatal("owned missing return fact inherited an unrelated row")
	}
	worker.clearReturnElementStmtUnknown(private)
	if unknown, present := worker.lookupReturnElementStmtUnknown(private); present || unknown {
		t.Fatal("clearing a copied return row resurrected it from the base")
	}
	worker.clearReturnElementStmtUnknown(original)
	if unknown, present := worker.lookupReturnElementStmtUnknown(original); !present || unknown {
		t.Fatal("local false tombstone failed to mask an inherited true row")
	}
	if !rootAnalyzer.returnElementStmtUnknown[original] {
		t.Fatal("local clear mutated the canonical return map")
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
