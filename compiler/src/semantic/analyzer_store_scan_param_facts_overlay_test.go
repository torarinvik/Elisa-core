//go:build cgo

package semantic

import (
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestStoreScanParamFactsOverlayInheritedCapAndLocalCapAreDistinct(t *testing.T) {
	fn, other := &ast.FuncDecl{Name: "source"}, &ast.FuncDecl{Name: "other"}
	key := storeScanParamKey{fn, 2, storeScanModeShareFrom + 3}
	localKey := storeScanParamKey{other, 2, storeScanModeDeep}
	node := &ast.Ident{Name: "origin"}
	for _, reach := range [][]storeScanParamKey{nil, make([]storeScanParamKey, 0, 7), make([]storeScanParamKey, 2, 9)} {
		sourceRow := storeScanParamFacts{why: "exact reason", at: node, writes: true, reach: reach}
		zero := storeScanParamKey{}
		source := &Analyzer{storeScanParamFactsCache: map[storeScanParamKey]storeScanParamFacts{key: sourceRow, zero: {}}}
		private := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
		if private.storeScanParamFactsOverlay == nil || private.storeScanParamFactsCache == nil || len(private.storeScanParamFactsCache) != 0 {
			t.Fatal("eager copy or map shape changed")
		}
		got, present := private.lookupStoreScanParamFacts(key)
		want := deepCloneValue(reflect.ValueOf(sourceRow)).Interface().(storeScanParamFacts)
		if !present || !reflect.DeepEqual(got, want) || cap(got.reach) != len(reach) || (got.reach == nil) != (reach == nil) {
			t.Fatal("inherited full row/header differs from old clone")
		}
		if reflect.ValueOf(got.reach).Pointer() != reflect.ValueOf(reach).Pointer() {
			t.Fatal("inherited backing pointer changed")
		}
		if got := private.storeScanParamLocalFacts(zero); got.writes {
			t.Fatal("known zero row recomputed as unknown no-body writer")
		}
		if _, present := private.lookupStoreScanParamFacts(storeScanParamKey{fn, 3, key.mode}); present {
			t.Fatal("composite key index ignored")
		}
		if _, present := private.lookupStoreScanParamFacts(storeScanParamKey{fn, 2, storeScanModeWrite}); present {
			t.Fatal("composite key mode ignored")
		}
		local := storeScanParamFacts{why: "newly computed shape", at: map[string]int{"opaque": 7}, reach: make([]storeScanParamKey, 1, 11)}
		private.setStoreScanParamFacts(localKey, local)
		again, present := private.lookupStoreScanParamFacts(localKey)
		if !present || !reflect.DeepEqual(again, local) || cap(again.reach) != 11 || reflect.ValueOf(again.reach).Pointer() != reflect.ValueOf(local.reach).Pointer() {
			t.Fatal("local row was capped/recloned instead of preserving computed value")
		}
		fork := cloneAnalyzerValueForBodyWorker(private).Addr().Interface().(*Analyzer)
		forked, present := fork.lookupStoreScanParamFacts(localKey)
		if !present || cap(forked.reach) != len(local.reach) || reflect.ValueOf(forked.reach).Pointer() != reflect.ValueOf(local.reach).Pointer() {
			t.Fatal("ordinary fork no longer applies old local slice policy")
		}
		inherited, present := fork.lookupStoreScanParamFacts(key)
		if !present || cap(inherited.reach) != len(reach) || !reflect.DeepEqual(inherited, want) {
			t.Fatal("ordinary fork lost inherited logical values")
		}
		if cap(source.storeScanParamFactsCache[key].reach) != cap(reach) || len(source.storeScanParamFactsCache) != 2 {
			t.Fatal("private lookup/write changed source")
		}
	}
}

func TestStoreScanParamFactsOverlayClosedFallbackAndNilShape(t *testing.T) {
	key := storeScanParamKey{&ast.FuncDecl{}, 3, 4}
	for _, source := range []map[storeScanParamKey]storeScanParamFacts{nil, {}, {key: {why: "reason", at: &ast.Ident{Name: "opaque"}, writes: true, reach: make([]storeScanParamKey, 2, 9)}}} {
		full := cloneStoreScanParamFactsConstructionMap(source, false)
		want := deepCloneValue(reflect.ValueOf(source)).Interface().(map[storeScanParamKey]storeScanParamFacts)
		if !reflect.DeepEqual(full, want) || (full == nil) != (source == nil) {
			t.Fatal("closed schema fallback did not clone complete old table")
		}
		if row, present := full[key]; present && cap(row.reach) != len(row.reach) {
			t.Fatal("fallback lost old cap policy")
		}
		private := cloneAnalyzerValueForSpecialization(&Analyzer{storeScanParamFactsCache: source}).Addr().Interface().(*Analyzer)
		ordinary := cloneAnalyzerValueForBodyWorker(&Analyzer{storeScanParamFactsCache: source}).Addr().Interface().(*Analyzer)
		if (private.storeScanParamFactsCache == nil) != (source == nil) || !reflect.DeepEqual(ordinary.storeScanParamFactsCache, want) || ordinary.storeScanParamFactsOverlay != nil {
			t.Fatal("nil/empty/ordinary policy changed")
		}
	}
}

func TestStoreScanParamFactsOverlayActualComputedSpareCapacity(t *testing.T) {
	root := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "param_overlay.elisa", `def leaf_a(values: mutable darray[i64]&) -> void:
    pass
def leaf_b(values: mutable darray[i64]&) -> void:
    pass
def leaf_c(values: mutable darray[i64]&) -> void:
    pass
def caller(values: mutable darray[i64]&) -> void:
    leaf_a(values)
    leaf_b(values)
    leaf_c(values)
`, AnalyzeOptions{})
	fn := storeScanFindFunc(t, root, "caller")
	worker, context := binderOverlayTestContext(root.analyzer)
	// Standalone store scans require the lexical resolution context that real
	// body analysis supplies; copying syntax alone does not enter that scope.
	worker.currentScope = worker.globalScope
	// Use the real private declaration key, so this row must be computed locally
	// rather than read from the canonical baseline.
	private := context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	calls := 0
	for _, node := range worker.storeScanIndex(private).flat {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			continue
		}
		callee, resolved := worker.resolveReadOnlyScanCallee(call)
		if !resolved || callee == nil || !storeFlowCallFitsParams(callee, call) {
			t.Fatal("real compute fixture call did not resolve with matching parameters")
		}
		if !worker.declParamIsWritableRef(callee, 0) || worker.declParamIsViewOrScalar(callee, 0) {
			t.Fatal("real compute fixture callee does not contribute a writable reference edge")
		}
		calls++
	}
	if calls != 3 {
		t.Fatal("real compute fixture must scan three distinct leaf calls", calls)
	}
	key := storeScanParamKey{private, 0, storeScanModeWrite}
	if _, present := worker.storeScanParamFactsCache[key]; present {
		t.Fatal("private fixture row was already computed before the measured getter")
	}
	first := worker.storeScanParamLocalFacts(key)
	if len(first.reach) < 3 || cap(first.reach) <= len(first.reach) {
		t.Fatal("real compute fixture did not produce spare reach capacity", len(first.reach), cap(first.reach), first.writes, first.why)
	}
	again := worker.storeScanParamLocalFacts(key)
	if !reflect.DeepEqual(first, again) || cap(first.reach) != cap(again.reach) || reflect.ValueOf(first.reach).Pointer() != reflect.ValueOf(again.reach).Pointer() {
		t.Fatal("local computed row capacity/backing was changed on get")
	}
	inherited := worker.storeScanParamLocalFacts(storeScanParamKey{fn, 0, storeScanModeWrite})
	_ = inherited // this separately exercises canonical composite keys if already observed
}

var paramOverlayCloneSink *Analyzer

func BenchmarkStoreScanParamFactsOwnedConstruction(b *testing.B) {
	for _, count := range []int{0, 24477} {
		source := &Analyzer{storeScanParamFactsCache: map[storeScanParamKey]storeScanParamFacts{}}
		for i := 0; i < count; i++ {
			source.storeScanParamFactsCache[storeScanParamKey{&ast.FuncDecl{}, i, 0}] = storeScanParamFacts{why: "fixture", reach: make([]storeScanParamKey, 2, 8)}
		}
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				paramOverlayCloneSink = cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
				if paramOverlayCloneSink.storeScanParamFactsCache == nil || len(paramOverlayCloneSink.storeScanParamFactsCache) != 0 {
					b.Fatal("eager copy")
				}
			}
			b.StopTimer()
			runtime.KeepAlive(source)
			runtime.KeepAlive(paramOverlayCloneSink)
		})
	}
}

func TestStoreScanParamFactsOverlayResourceGuards(t *testing.T) {
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
	worker.storeScanBinderOverlay = nil
	worker.storeScanParamFactsOverlay.base = map[storeScanParamKey]storeScanParamFacts{}
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
	if got, err := view.SpecializeFunction(fn, args); got != nil || err == nil || !strings.Contains(err.Error(), "store-scan parameter-facts overlay") {
		t.Fatal("memo bypassed isolated binder guard", err)
	}
	worker.diagnostics = nil
	worker.reportedDiagnostics = nil
	state.a.storeScanParamFactsOverlay = worker.storeScanParamFactsOverlay
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("marshal accepted resource")
	}
	if err := (&AnalysisBuild{retained: state, file: result.File, loweredFile: result.LoweredFile}).validate(); err == nil || !strings.Contains(err.Error(), "store-scan parameter-facts overlay") {
		t.Fatal("build accepted resource", err)
	}
	encoded, err := semanticStateRegistry().Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	if restored, err := RestoreAnalysisState(encoded); restored != nil || err == nil || !strings.Contains(err.Error(), "store-scan parameter-facts overlay") {
		t.Fatal("restore independently encoded resource", err)
	}
	state.a.storeScanParamFactsOverlay = nil
	if err := validateSparseFactValueType(reflect.ValueOf(worker.storeScanParamFactsOverlay)); err == nil {
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

func TestStoreScanParamFactsOverlayConstructorKeepsClosedSchemaFallback(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "parallel_bodies.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "cloneStoreScanParamFactsConstructionMap(a.storeScanParamFactsCache, specializationParamFactsCopySchemaValid)") {
		t.Fatal("constructor bypasses exact value schema")
	}

	file, err := parser.ParseFile(token.NewFileSet(), "parallel_bodies.go", data, 0)
	if err != nil {
		t.Fatal(err)
	}
	total, gated := 0, 0
	count := func(node goast.Node) int {
		found := 0
		goast.Inspect(node, func(node goast.Node) bool {
			if assignment, ok := node.(*goast.AssignStmt); ok {
				for _, lhs := range assignment.Lhs {
					if field, ok := lhs.(*goast.SelectorExpr); ok && field.Sel.Name == "storeScanParamFactsOverlay" {
						found++
					}
				}
			}
			return true
		})
		return found
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*goast.FuncDecl)
		if !ok || function.Name.Name != "cloneAnalyzerValueForParallelPolicy" {
			continue
		}
		total = count(function.Body)
		goast.Inspect(function.Body, func(node goast.Node) bool {
			if condition, ok := node.(*goast.IfStmt); ok {
				if name, ok := condition.Cond.(*goast.Ident); ok && name.Name == "specializationParamFactsCopySchemaValid" {
					gated += count(condition.Body)
				}
			}
			return true
		})
	}
	if total != 1 || gated != 1 {
		t.Fatal("unknown schema can borrow base instead of full old clone", total, gated)
	}

}
