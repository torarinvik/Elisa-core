package semantic

import (
	"sync"
	"testing"

	"elisacore/src/ast"
)

func TestSpecializationStoreTargetsOverlay(t *testing.T) {
	inherited := &ast.FuncDecl{Name: "inherited"}
	overridden := &ast.FuncDecl{Name: "overridden"}
	missing := &ast.FuncDecl{Name: "missing"}
	privateDecl := &ast.FuncDecl{Name: "private copy"}
	base := map[*ast.FuncDecl][]map[int]bool{
		inherited:  {{1: true}},
		overridden: {{2: true}},
	}
	worker := &Analyzer{paramStoreTargets: map[*ast.FuncDecl][]map[int]bool{overridden: nil},
		parallel: &parallelWorkerState{original: map[*ast.FuncDecl]*ast.FuncDecl{privateDecl: inherited}, specialization: &specializationContext{storeTargets: base}}}
	if row, ok := worker.lookupParamStoreTargets(inherited); !ok || !row[0][1] {
		t.Fatal("missing inherited store target")
	}
	if row, ok := worker.lookupParamStoreTargets(privateDecl); !ok || !row[0][1] {
		t.Fatal("private declaration did not resolve its canonical store target")
	}
	if row, ok := worker.lookupParamStoreTargets(overridden); !ok || row != nil {
		t.Fatal("private nil row must shadow the inherited row")
	}
	if _, ok := worker.lookupParamStoreTargets(missing); ok {
		t.Fatal("missing row unexpectedly present")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			private := &Analyzer{paramStoreTargets: make(map[*ast.FuncDecl][]map[int]bool),
				parallel: &parallelWorkerState{specialization: &specializationContext{storeTargets: base}}}
			for j := 0; j < 100; j++ {
				if row, ok := private.lookupParamStoreTargets(inherited); !ok || !row[0][1] {
					t.Error("concurrent inherited lookup changed")
					return
				}
				private.paramStoreTargets[missing] = []map[int]bool{{j: true}}
			}
		}()
	}
	wg.Wait()
	if _, ok := base[missing]; ok {
		t.Fatal("private writes changed frozen table")
	}
}
