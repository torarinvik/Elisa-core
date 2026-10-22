package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"testing"
)

var parallelTaintCloneSink reflect.Value

func BenchmarkStoreScanTaintWorkerClone(b *testing.B) {
	decl := &ast.FuncDecl{Name: "callee"}
	params := map[string]bool{}
	for i := 0; i < 18; i++ {
		params[fmt.Sprintf("p%d", i)] = true
	}
	source := &Analyzer{storeScanTaintCache: map[storeScanTaintKey]storeScanTaintResult{}}
	for i := 0; i < 324; i++ {
		source.storeScanTaintCache[storeScanTaintKey{decl, i / 18, fmt.Sprintf("p%d", i%18)}] = storeScanTaintResult{
			params: params, header: map[string]bool{"source": true}, content: map[string]bool{"source": true, "local": true}, targets: map[string]bool{"target": true}, views: map[string]bool{"view": true}, holders: map[string]bool{},
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parallelTaintCloneSink = cloneAnalyzerValueForBodyWorker(source)
	}
}

func TestParallelBodyWorkerTaintCacheOwnership(t *testing.T) {
	for _, retained := range []*RetainedAnalysis{nil, {}} {
		decl := &ast.FuncDecl{Name: "callee", Params: []ast.ParamDecl{{Name: "first"}, {Name: "second"}}}
		key := storeScanTaintKey{decl, 0, "second"}
		published := storeScanTaintResult{params: map[string]bool{"first": true, "second": true}, header: map[string]bool{"first": true}, content: map[string]bool{"first": true}, targets: map[string]bool{"second": true}, views: map[string]bool{}, holders: map[string]bool{}}
		base := &Analyzer{retained: retained, storeScanTaintCache: map[storeScanTaintKey]storeScanTaintResult{key: published}}
		workers := []*Analyzer{
			cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer),
			cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer),
		}
		for _, worker := range workers {
			got := worker.storeScanTaintCache[key]
			originals := []map[string]bool{published.params, published.header, published.content, published.targets, published.views, published.holders}
			copies := []map[string]bool{got.params, got.header, got.content, got.targets, got.views, got.holders}
			for i := range originals {
				if fmt.Sprintf("%p", originals[i]) != fmt.Sprintf("%p", copies[i]) {
					t.Fatal("worker duplicated a published immutable set")
				}
			}
		}
		results := make(chan string, 2)
		for _, worker := range workers {
			go func(worker *Analyzer) {
				for repeat := 0; repeat < 100; repeat++ {
					params, header, content, targets, _ := worker.storeScanTaint(decl, 0, "second", nil)
					if !params["first"] || !params["second"] || !header["first"] || !content["first"] || !targets["second"] {
						results <- "published result changed"
						return
					}
					worker.storeScanTaint(decl, 1, "first", nil)
				}
				results <- ""
			}(worker)
		}
		for range workers {
			if err := <-results; err != "" {
				t.Fatal(err)
			}
		}
		if len(base.storeScanTaintCache) != 1 {
			t.Fatal("worker insertion changed parent cache")
		}
		delete(workers[0].storeScanTaintCache, key)
		if _, ok := base.storeScanTaintCache[key]; !ok {
			t.Fatal("worker deletion changed parent cache")
		}
		if _, ok := workers[1].storeScanTaintCache[key]; !ok {
			t.Fatal("worker deletion changed sibling cache")
		}
	}
	empty := cloneAnalyzerValueForBodyWorker(&Analyzer{}).Addr().Interface().(*Analyzer)
	if empty.storeScanTaintCache != nil {
		t.Fatal("nil taint cache became allocated")
	}
}
