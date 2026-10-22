package semantic

import (
	"reflect"
	"sync/atomic"
	"testing"

	"elisacore/src/ast"
)

func TestParallelWorkerRemapsWarmCallGraphCachesToReadShadows(t *testing.T) {
	call := func(name string) ast.Stmt {
		return &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: name}}}
	}
	owner := &ast.FuncDecl{Name: "owner", Body: []ast.Stmt{call("callee")}}
	callee := &ast.FuncDecl{Name: "callee", Body: []ast.Stmt{call("owner")}}
	base := &Analyzer{
		funcDeclSymbols: map[*ast.FuncDecl]*Symbol{
			owner:  {Name: "owner"},
			callee: {Name: "callee"},
		},
	}
	if edges := base.callEdgesOf(owner); len(edges) != 1 || edges[0] != callee {
		t.Fatalf("serial cache setup got edges %#v", edges)
	}
	if component := base.storeFlowComponent(owner); len(component) != 2 {
		t.Fatalf("serial cache setup got SCC %#v", component)
	}

	worker := cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer)
	snapshot := func(fn *ast.FuncDecl) *ast.SyntaxSnapshot {
		s, err := ast.NewSyntaxSnapshot(&ast.File{Decls: []ast.Decl{fn}}, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	slots := make([]atomic.Int32, 2)
	slots[0].Store(0)
	slots[1].Store(1)
	worker.parallel = &parallelWorkerState{
		worker:      0,
		snapshots:   []*ast.SyntaxSnapshot{snapshot(owner), snapshot(callee)},
		at:          map[*ast.FuncDecl][2]int{owner: {0, -1}, callee: {1, -1}},
		slots:       slots,
		shadows:     map[*ast.FuncDecl]*ast.FuncDecl{},
		readShadows: map[*ast.FuncDecl]*ast.FuncDecl{},
		original:    map[*ast.FuncDecl]*ast.FuncDecl{},
		imported:    map[*ast.FuncDecl]bool{},
	}

	edges := worker.callEdgesOf(owner)
	if len(edges) != 1 || edges[0] == callee || worker.parallel.original[edges[0]] != callee {
		t.Fatalf("warm edge cache returned canonical foreign callee instead of its worker shadow: %#v", edges)
	}
	component := worker.storeFlowComponent(owner)
	foundShadow, foundCanonical := false, false
	for _, member := range component {
		foundShadow = foundShadow || worker.parallel.original[member] == callee
		foundCanonical = foundCanonical || member == callee
	}
	if !foundShadow || foundCanonical {
		t.Fatalf("warm SCC cache was not remapped to worker identities: %#v", component)
	}
	// Importing a completed unit switches reads to its finished declaration.
	// Previously cached shadows must resolve to that same identity as well.
	shadow := edges[0]
	worker.parallel.imported[callee] = true
	if got := worker.readDecl(shadow); got != callee {
		t.Fatal("a cached read-shadow kept its old identity after the callee was imported")
	}
	if got := worker.callEdgesOf(owner); len(got) != 1 || got[0] != callee {
		t.Fatal("cached call edges did not follow the imported declaration")
	}
	for _, member := range worker.storeFlowComponent(owner) {
		if member == shadow {
			t.Fatal("cached SCC retained an obsolete read-shadow after import")
		}
	}
	if base.callEdgeCache[owner][0] != callee {
		t.Fatal("remapping a worker cache changed its canonical base")
	}
}

func TestParallelWorkerKeepsStorageViewWriteSummariesLocal(t *testing.T) {
	decl := &ast.FuncDecl{Name: "callee"}
	shadow := &ast.FuncDecl{Name: "callee-shadow"}
	base := &Analyzer{storageViewCalleeWriteCache: map[storageViewCalleeWriteKey]storageViewCalleeWrites{
		{decl: decl, param: 0}: {Fields: map[string]bool{"left": true}},
	}}
	worker := cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer)
	baseSummary := base.storageViewCalleeWriteCache[storageViewCalleeWriteKey{decl: decl, param: 0}]
	workerSummary := worker.storageViewCalleeWriteCache[storageViewCalleeWriteKey{decl: decl, param: 0}]
	workerSummary.Fields["worker-only"] = true
	if baseSummary.Fields["worker-only"] {
		t.Fatal("worker mutated the cloned summary payload in the base analyzer")
	}
	worker.storageViewCalleeWriteCache[storageViewCalleeWriteKey{decl: shadow, param: 0}] = storageViewCalleeWrites{Fields: map[string]bool{"right": true}}

	dst := &Analyzer{storageViewCalleeWriteCache: map[storageViewCalleeWriteKey]storageViewCalleeWrites{
		{decl: decl, param: 0}: baseSummary,
	}}
	baseValue := reflect.ValueOf(dst).Elem()
	baseSnapshot := reflect.New(reflect.TypeOf(Analyzer{})).Elem()
	baseSnapshot.Set(baseValue)
	p := &parallelWorkerState{original: map[*ast.FuncDecl]*ast.FuncDecl{shadow: decl}}
	mergeValue(baseValue, reflect.ValueOf(worker).Elem(), baseSnapshot, p)
	if len(dst.storageViewCalleeWriteCache) != 1 || dst.storageViewCalleeWriteCache[storageViewCalleeWriteKey{decl: decl, param: 0}].Fields["worker-only"] {
		t.Fatal("worker-local storage-view summaries leaked into the root analyzer")
	}

	for pathIndex, path := range exportDeclMapPaths() {
		name := reflect.TypeOf(Analyzer{}).Field(path[0]).Name
		if name == "storageViewCalleeWriteCache" {
			t.Errorf("composite-key worker-local cache is included in unit exports (path %d)", pathIndex)
		}
	}
}
