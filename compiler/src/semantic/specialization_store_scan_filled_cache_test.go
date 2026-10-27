package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

var specializationFilledCacheCloneSink *Analyzer

func TestSpecializationStoreScanFilledCacheOuterIsolation(t *testing.T) {
	decl := &ast.FuncDecl{Name: "filled"}
	key := storeScanKey{decl: decl, other: 1, index: -4}
	row := map[string]bool{"alpha": true, "beta": false}
	root := &Analyzer{storeScanFilledCache: map[storeScanKey]map[string]bool{key: row}}
	private := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if private.storeScanFilledCache == nil || len(private.storeScanFilledCache) != 1 {
		t.Fatal("specialization lost cached filled-local rows")
	}
	privateRow := private.storeScanFilledCache[key]
	if reflect.ValueOf(private.storeScanFilledCache).Pointer() == reflect.ValueOf(root.storeScanFilledCache).Pointer() {
		t.Fatal("specialization shared the mutable outer cache")
	}
	if reflect.ValueOf(privateRow).Pointer() != reflect.ValueOf(row).Pointer() {
		t.Fatal("specialization copied a settled read-only result row")
	}
	if !privateRow["alpha"] || privateRow["beta"] {
		t.Fatal("cached positive/negative values changed")
	}

	newKey := storeScanKey{decl: &ast.FuncDecl{Name: "new"}, other: 2, index: -4}
	private.storeScanFilledCache[newKey] = map[string]bool{"local": true}
	private.storeScanFilledCache[key] = map[string]bool{"replacement": true}
	delete(private.storeScanFilledCache, key)
	rootRow, stillPresent := root.storeScanFilledCache[key]
	if len(root.storeScanFilledCache) != 1 || !stillPresent || reflect.ValueOf(rootRow).Pointer() != reflect.ValueOf(row).Pointer() {
		t.Fatal("specialization outer-map writes reached the canonical cache")
	}

	bodyWorker := cloneAnalyzerValueForBodyWorker(root).Addr().Interface().(*Analyzer)
	bodyRow := bodyWorker.storeScanFilledCache[key]
	if reflect.ValueOf(bodyWorker.storeScanFilledCache).Pointer() == reflect.ValueOf(root.storeScanFilledCache).Pointer() || reflect.ValueOf(bodyRow).Pointer() == reflect.ValueOf(row).Pointer() {
		t.Fatal("ordinary body worker did not deeply isolate filled-local cache rows")
	}
	bodyRow["worker-only"] = true
	if row["worker-only"] {
		t.Fatal("ordinary body-worker row mutation reached the source cache")
	}

	for _, source := range []map[storeScanKey]map[string]bool{nil, {}} {
		clone := cloneAnalyzerValueForSpecialization(&Analyzer{storeScanFilledCache: source}).Addr().Interface().(*Analyzer).storeScanFilledCache
		if (clone == nil) != (source == nil) {
			t.Fatal("nil/empty cache shape changed")
		}
	}
}

func BenchmarkSpecializationStoreScanFilledCacheClone(b *testing.B) {
	const rows = 2048
	names := [...]string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	cache := make(map[storeScanKey]map[string]bool, rows)
	for i := 0; i < rows; i++ {
		row := make(map[string]bool, len(names))
		for j, name := range names {
			row[name] = (i+j)%2 == 0
		}
		cache[storeScanKey{decl: &ast.FuncDecl{Name: fmt.Sprintf("f%d", i)}, index: -4}] = row
	}
	root := &Analyzer{storeScanFilledCache: cache}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		specializationFilledCacheCloneSink = cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	}
}
