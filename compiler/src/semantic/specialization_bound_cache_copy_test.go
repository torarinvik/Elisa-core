//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"maps"
	"reflect"
	"testing"
)

func TestSpecializationBoundCacheSettledSetsAndOuterIsolation(t *testing.T) {
	for _, table := range []map[*ast.FuncDecl]map[string]bool{nil, {}} {
		root := &Analyzer{storeScanBoundCache: table}
		private := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
		if (private.storeScanBoundCache == nil) != (table == nil) {
			t.Fatal("nil/empty shape changed")
		}
	}
	const source = "def count(x: i64) -> i64:\n    local: i64 = x\n    return local\n"
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	fn := state.pristine.Decls[0].(*ast.FuncDecl)
	want := state.a.storeScanBoundNames(fn)
	if !want["local"] {
		t.Fatal("fixture lacks actual bound local")
	}
	private := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	got := private.storeScanBoundNames(fn)
	if !reflect.DeepEqual(got, want) || reflect.ValueOf(got).Pointer() != reflect.ValueOf(want).Pointer() {
		t.Fatal("settled set not shared")
	}
	ordinary := cloneAnalyzerValueForBodyWorker(state.a).Addr().Interface().(*Analyzer)
	if reflect.ValueOf(ordinary.storeScanBoundCache[fn]).Pointer() == reflect.ValueOf(want).Pointer() {
		t.Fatal("ordinary worker policy changed")
	}
	fresh := retainedParse(t, "def fresh() -> i64:\n    other: i64 = 1\n    return other\n").Decls[0].(*ast.FuncDecl)
	before := len(state.a.storeScanBoundCache)
	if !private.storeScanBoundNames(fresh)["other"] || len(state.a.storeScanBoundCache) != before {
		t.Fatal("new private row leaked")
	}
	delete(private.storeScanBoundCache, fn)
	if _, ok := state.a.storeScanBoundCache[fn]; !ok {
		t.Fatal("outer deletion leaked")
	}
}

var boundCopySink *Analyzer

func BenchmarkSpecializationBoundCacheCopy(b *testing.B) {
	root := &Analyzer{storeScanBoundCache: map[*ast.FuncDecl]map[string]bool{}}
	for i := 0; i < 2048; i++ {
		root.storeScanBoundCache[&ast.FuncDecl{}] = map[string]bool{"local": true, "other": true, "shadow": false}
	}
	for _, mode := range []string{"old_reflect", "settled_specialization"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "old_reflect" {
					boundCopySink = &Analyzer{storeScanBoundCache: deepCloneValue(reflect.ValueOf(root.storeScanBoundCache)).Interface().(map[*ast.FuncDecl]map[string]bool)}
				} else {
					boundCopySink = &Analyzer{storeScanBoundCache: maps.Clone(root.storeScanBoundCache)}
				}
			}
		})
	}
}
