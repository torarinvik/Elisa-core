//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func assertStoreFlowKindsShared(t *testing.T, source, current *storeFlowSummary) {
	t.Helper()
	if !reflect.DeepEqual(current.retKind, source.retKind) || !reflect.DeepEqual(current.reachKind, source.reachKind) {
		t.Fatal("initial kind values changed")
	}
	if len(source.retKind) != 0 && reflect.ValueOf(current.retKind).Pointer() != reflect.ValueOf(source.retKind).Pointer() {
		t.Fatal("specialization eagerly copied return kinds")
	}
	if len(source.reachKind) != 0 && reflect.ValueOf(current.reachKind).Pointer() != reflect.ValueOf(source.reachKind).Pointer() {
		t.Fatal("specialization eagerly copied reach-kind headers")
	}
	for i, row := range source.reachKind {
		if len(row) != 0 && reflect.ValueOf(current.reachKind[i]).Pointer() != reflect.ValueOf(row).Pointer() {
			t.Fatal("specialization eagerly copied a reach-kind row")
		}
	}
}

func assertStoreFlowKindsDetached(t *testing.T, source, current *storeFlowSummary) {
	t.Helper()
	if !reflect.DeepEqual(current.retKind, source.retKind) || !reflect.DeepEqual(current.reachKind, source.reachKind) {
		t.Fatal("detaching changed kind values")
	}
	assertStoreFlowKindBackingDetached(t, source, current)
}

func assertStoreFlowKindBackingDetached(t *testing.T, source, current *storeFlowSummary) {
	t.Helper()
	if len(source.retKind) != 0 && reflect.ValueOf(current.retKind).Pointer() == reflect.ValueOf(source.retKind).Pointer() {
		t.Fatal("return kinds still share mutable backing")
	}
	if len(source.reachKind) != 0 && reflect.ValueOf(current.reachKind).Pointer() == reflect.ValueOf(source.reachKind).Pointer() {
		t.Fatal("reach-kind headers still share mutable backing")
	}
	for i, row := range source.reachKind {
		if len(row) != 0 && reflect.ValueOf(current.reachKind[i]).Pointer() == reflect.ValueOf(row).Pointer() {
			t.Fatal("reach-kind row still shares mutable backing")
		}
	}
}

func TestStoreFlowSpecializationCopiesHeadersAndPreservesAliases(t *testing.T) {
	for _, table := range []map[*ast.FuncDecl]*storeFlowSummary{nil, {}} {
		cloned := cloneStoreFlowSummariesForSpecialization(table)
		if (cloned == nil) != (table == nil) || len(cloned) != len(table) {
			t.Fatal("nil/empty summary map shape changed")
		}
	}
	first, alias, other, missing := &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}
	summary := newStoreFlowSummary(3)
	summary.retConn[0] = true
	storeFlowInitKinds(summary)
	summary.retKind[0] = storeFlowEdgeElements
	summary.reachKind[0][1] = storeFlowEdgeElements
	distinct := *summary
	root := map[*ast.FuncDecl]*storeFlowSummary{first: summary, alias: summary, other: &distinct, missing: nil}
	worker := cloneStoreFlowSummariesForSpecialization(root)
	if worker[first] == summary || worker[first] != worker[alias] || worker[first] == worker[other] || worker[missing] != nil {
		t.Fatal("private summary headers or source alias groups changed")
	}
	if !reflect.DeepEqual(worker[first], summary) || reflect.ValueOf(worker[first].conn).Pointer() != reflect.ValueOf(summary.conn).Pointer() || reflect.ValueOf(worker[first].retConn).Pointer() != reflect.ValueOf(summary.retConn).Pointer() || reflect.ValueOf(worker[first].written).Pointer() != reflect.ValueOf(summary.written).Pointer() || reflect.ValueOf(worker[first].reach).Pointer() != reflect.ValueOf(summary.reach).Pointer() {
		t.Fatal("immutable summary facts were copied or changed")
	}
	assertStoreFlowKindsShared(t, summary, worker[first])
	assertStoreFlowKindsShared(t, &distinct, worker[other])

	detachStoreFlowKindsForWrite(worker, []*ast.FuncDecl{first, alias, other})
	assertStoreFlowKindsDetached(t, summary, worker[first])
	assertStoreFlowKindsDetached(t, &distinct, worker[other])
	if worker[first] != worker[alias] {
		t.Fatal("kind detachment split a summary pointer alias")
	}
	worker[first].retKind[0] = storeFlowEdgeHeader
	if summary.retKind[0] != storeFlowEdgeElements {
		t.Fatal("private kind mutation reached canonical summary")
	}
}

func TestStoreFlowSpecializationHeaderBlocksPreserveAliasGroups(t *testing.T) {
	root := make(map[*ast.FuncDecl]*storeFlowSummary)
	var first, alias *ast.FuncDecl
	for index := 0; index < 384; index++ {
		decl := &ast.FuncDecl{Name: fmt.Sprintf("function_%d", index)}
		summary := newStoreFlowSummary(1)
		storeFlowInitKinds(summary)
		summary.retKind[0] = storeFlowEdgeElements
		root[decl] = summary
		if index == 0 {
			first, alias = decl, &ast.FuncDecl{Name: "alias"}
			root[alias] = summary
		}
	}
	root[&ast.FuncDecl{Name: "unknown"}] = nil
	worker := cloneStoreFlowSummariesForSpecialization(root)
	if len(worker) != len(root) {
		t.Fatal("header blocks changed the outer map")
	}
	groups := make(map[*storeFlowSummary]*storeFlowSummary)
	privateOwners := make(map[*storeFlowSummary]*storeFlowSummary)
	for decl, source := range root {
		private := worker[decl]
		if source == nil {
			if private != nil {
				t.Fatal("unknown summary acquired a header")
			}
			continue
		}
		if private == source || !reflect.DeepEqual(private, source) {
			t.Fatal("block header is shared with the source or changed its facts")
		}
		if previous := groups[source]; previous != nil && previous != private {
			t.Fatal("source alias group split across private headers")
		}
		if previous := privateOwners[private]; previous != nil && previous != source {
			t.Fatal("distinct source summaries share one private header")
		}
		groups[source], privateOwners[private] = private, source
		assertStoreFlowKindsShared(t, source, private)
	}
	detachStoreFlowKindsForWrite(worker, []*ast.FuncDecl{first, alias})
	worker[first].retKind[0] = storeFlowEdgeHeader
	if worker[first] != worker[alias] {
		t.Fatal("kind detachment split the aliased header")
	}
	for _, source := range root {
		if source != nil && source.retKind[0] != storeFlowEdgeElements {
			t.Fatal("block header mutation reached the canonical catalog")
		}
	}
}

func TestStoreFlowSpecializationDetachesKindsForEverySolvedComponentMember(t *testing.T) {
	makeDecl := func(name string) *ast.FuncDecl {
		return &ast.FuncDecl{
			Name:   name,
			Params: []ast.ParamDecl{{Name: "value"}},
			Body:   []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "value"}}},
		}
	}
	first, second := makeDecl("first"), makeDecl("second")
	unsolved := newStoreFlowSummary(1)
	unsolved.retConn[0] = true
	solved := newStoreFlowSummary(1)
	solved.retConn[0] = true
	storeFlowInitKinds(solved)
	solved.retKind[0] = storeFlowEdgeElements
	solved.reachKind[0][0] = storeFlowEdgeElements
	root := map[*ast.FuncDecl]*storeFlowSummary{first: unsolved, second: solved}
	worker := &Analyzer{
		storeFlowSummaries: root,
		storeFlowSCC:       map[*ast.FuncDecl][]*ast.FuncDecl{first: {first, second}},
		callEdgeCache:      map[*ast.FuncDecl][]*ast.FuncDecl{first: nil, second: nil},
	}
	worker.storeFlowSummaries = cloneStoreFlowSummariesForSpecialization(root)
	worker.parallel = &parallelWorkerState{
		specialization: &specializationContext{},
		original:       map[*ast.FuncDecl]*ast.FuncDecl{first: first, second: second},
	}
	if got := worker.storeFlowKindsFor(first); got == nil {
		t.Fatal("private component solve returned no summary")
	}
	if worker.storeFlowSummaries[first] == unsolved || worker.storeFlowSummaries[second] == solved {
		t.Fatal("specialization retained canonical summary headers")
	}
	if unsolved.retKind != nil || unsolved.reachKind != nil {
		t.Fatal("solving the unsolved private member changed canonical readiness")
	}
	if solved.retKind[0] != storeFlowEdgeElements || solved.reachKind[0][0] != storeFlowEdgeElements {
		t.Fatal("solving a mixed component changed canonical solved kinds")
	}
	assertStoreFlowKindBackingDetached(t, solved, worker.storeFlowSummaries[second])
}

func TestStoreFlowSpecializationPublicationBoundaryAndOrdinaryClone(t *testing.T) {
	const source = "def first[T](value: T) -> T:\n    return value\n\ndef keep(value: i64) -> i64:\n    return value\n"
	root := analyzeTreeTestSource(t, "private_store_flow_specialization.elisa", source)
	fn := root.File.Decls[0].(*ast.FuncDecl)
	var settled *ast.FuncDecl
	for _, decl := range root.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "keep" {
			settled = candidate
			break
		}
	}
	if settled == nil {
		t.Fatal("missing plain callee")
	}
	summary := root.analyzer.storeFlowKindsFor(settled)
	if summary == nil {
		t.Fatal("actual summary did not settle")
	}
	private := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	ordinary := cloneAnalyzerValueForBodyWorker(root.analyzer).Addr().Interface().(*Analyzer)
	if private.storeFlowSummaries[settled] == summary {
		t.Fatal("specialization shared a mutable summary header")
	}
	assertStoreFlowKindsShared(t, summary, private.storeFlowSummaries[settled])
	if ordinary.storeFlowSummaries[settled] == summary {
		t.Fatal("ordinary worker clone lost its private summary")
	}
	assertStoreFlowKindsDetached(t, summary, ordinary.storeFlowSummaries[settled])
	if err := validateSparseFactValueType(reflect.ValueOf(private.storeFlowSummaries[settled])); err == nil {
		t.Fatal("private summary could enter publication facts")
	}
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"]})
	if err != nil || product == nil {
		t.Fatal(err)
	}
	if product.Result.analyzer != nil || len(product.Result.specializationQueries.types.storeFlowSummaries) != 0 {
		t.Fatal("publication retained private summaries")
	}
	if root.analyzer.storeFlowSummaries[settled] != summary {
		t.Fatal("specialization changed the canonical summary header")
	}
}

var summaryCloneSink map[*ast.FuncDecl]*storeFlowSummary

func BenchmarkStoreFlowSummaryClone(b *testing.B) {
	for _, size := range []int{0, 1, 128, 11308} {
		source := make(map[*ast.FuncDecl]*storeFlowSummary, size)
		for i := 0; i < size; i++ {
			summary := newStoreFlowSummary(3)
			storeFlowInitKinds(summary)
			source[&ast.FuncDecl{}] = summary
		}
		for _, mode := range []string{"body_worker", "specialization_headers"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if mode == "body_worker" {
						summaryCloneSink = cloneStoreFlowSummariesForBodyWorker(source)
					} else {
						summaryCloneSink = cloneStoreFlowSummariesForSpecialization(source)
					}
				}
			})
		}
	}
}
