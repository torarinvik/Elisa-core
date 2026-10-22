package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestStoreScanIdentifierCountsPreserveOccurrenceRules(t *testing.T) {
	shared := &ast.Ident{Name: "shared"}
	expr := &ast.ListLitExpr{Elems: []ast.Expr{
		shared, shared, &ast.Ident{Name: "shared"},
		&ast.Ident{Name: "root.member"},
		&ast.FieldExpr{Object: &ast.Ident{Name: "root"}, Field: "selector"},
	}}
	body := []ast.Stmt{&ast.ReturnStmt{Value: expr}}
	idx := &storeScanDeclIndex{}
	for name, want := range map[string]int{"shared": 2, "root": 2, "root.member": 1, "member": 0, "selector": 0, "absent": 0} {
		if got := idx.identCount(expr, name); got != want {
			t.Errorf("expression %s: got %d, want %d", name, got, want)
		}
		if got := idx.bodyIdentCount(body, name); got != want {
			t.Errorf("body %s: got %d, want %d", name, got, want)
		}
	}
	// A separately queried subtree has its own pointer-deduplication scope.
	if got := idx.identCount(shared, "shared"); got != 1 {
		t.Fatalf("subtree count: %d", got)
	}
	if got := idx.identCount((*ast.Ident)(nil), "shared"); got != 0 {
		t.Fatalf("typed nil count: %d", got)
	}
}

func TestParallelBodyWorkerRebuildsMutableStoreScanIndexes(t *testing.T) {
	decl := &ast.FuncDecl{Name: "callee"}
	for _, retained := range []*RetainedAnalysis{nil, {}} {
		base := &Analyzer{retained: retained, storeScanIndexCache: map[*ast.FuncDecl]*storeScanDeclIndex{decl: {}}}
		worker := cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer)
		if worker.storeScanIndexCache != nil {
			t.Fatal("worker inherited mutable scan-index pointers")
		}
		if len(base.storeScanIndexCache) != 1 {
			t.Fatal("worker clone changed the source index")
		}
	}
}

func TestParallelBodyWorkerRebuildsMutableStoreFlowCaches(t *testing.T) {
	decl := &ast.FuncDecl{Name: "callee", Params: []ast.ParamDecl{{Name: "value"}}}
	for _, retained := range []*RetainedAnalysis{nil, {}} {
		index := buildStoreFlowBinderIndex(decl)
		summary := newStoreFlowSummary(1)
		summary.retConn[0] = true
		storeFlowInitKinds(summary)
		base := &Analyzer{
			retained:               retained,
			storeFlowBinderIndexes: map[*ast.FuncDecl]*storeFlowBinderIndex{decl: index},
			storeFlowSummaries:     map[*ast.FuncDecl]*storeFlowSummary{decl: summary},
			storeFlowInProgress:    map[*ast.FuncDecl]*storeFlowSummary{decl: summary},
			storeFlowActive:        map[*ast.FuncDecl]bool{decl: true},
		}
		first := cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer)
		second := cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer)
		for _, worker := range []*Analyzer{first, second} {
			if worker.storeFlowBinderIndexes != nil || worker.storeFlowInProgress != nil || worker.storeFlowActive != nil {
				t.Fatal("worker inherited mutable summary pointers or an active solve")
			}
			if private := worker.storeFlowSummaries[decl]; private == nil || private == summary {
				t.Fatal("worker did not retain a private settled summary")
			}
		}
		firstSummary, secondSummary := first.storeFlowSummaries[decl], second.storeFlowSummaries[decl]
		if firstSummary == secondSummary || &firstSummary.retConn[0] != &summary.retConn[0] {
			t.Fatal("summary objects must be private while settled connectivity stays shared")
		}
		firstSummary.retKind[0] = storeFlowEdgeNone
		firstSummary.reachKind[0][0] = storeFlowEdgeNone
		if summary.retKind[0] != storeFlowEdgeHeader || secondSummary.retKind[0] != storeFlowEdgeHeader ||
			summary.reachKind[0][0] != storeFlowEdgeHeader || secondSummary.reachKind[0][0] != storeFlowEdgeHeader {
			t.Fatal("edge-kind mutation reached another analyzer")
		}
		firstIndex := first.storeFlowBinderIndexFor(decl)
		secondIndex := second.storeFlowBinderIndexFor(decl)
		if firstIndex == index || secondIndex == index || firstIndex == secondIndex {
			t.Fatal("workers share a lazy binder index")
		}
		firstIndex.binderNames()["worker_only"] = true
		if index.allNames != nil || secondIndex.binderNames()["worker_only"] {
			t.Fatal("lazy cache write reached another analyzer")
		}
		if base.storeFlowSummaries[decl] != summary || base.storeFlowInProgress[decl] != summary || !base.storeFlowActive[decl] {
			t.Fatal("cloning a worker changed the source caches")
		}
	}
}

func TestStoreFlowWorkerSummaryClonePreservesReadinessAndAliases(t *testing.T) {
	first, alias, lazy, missing := &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}
	ready := newStoreFlowSummary(0)
	storeFlowInitKinds(ready)
	uncomputed := newStoreFlowSummary(1)
	src := map[*ast.FuncDecl]*storeFlowSummary{first: ready, alias: ready, lazy: uncomputed, missing: nil}
	cloned := cloneStoreFlowSummariesForBodyWorker(src)
	if cloned[first] == ready || cloned[first] != cloned[alias] || cloned[first].retKind == nil || cloned[first].reachKind == nil {
		t.Fatal("clone changed aliasing or a completed zero-parameter summary's readiness")
	}
	if cloned[missing] != nil || cloned[lazy].retKind != nil || cloned[lazy].reachKind != nil {
		t.Fatal("clone changed a missing or lazy summary")
	}
	storeFlowInitKinds(cloned[lazy])
	if uncomputed.retKind != nil || uncomputed.reachKind != nil {
		t.Fatal("initializing private kinds changed the source summary")
	}
}
