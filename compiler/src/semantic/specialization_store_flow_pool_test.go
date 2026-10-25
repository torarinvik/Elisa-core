//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"testing"
)

func assertPooledSummaryShape(t *testing.T, source, current, reference *storeFlowSummary) {
	t.Helper()
	if (current == nil) != (reference == nil) {
		t.Fatal("nil summary changed")
	}
	if current == nil {
		return
	}
	if !reflect.DeepEqual(current, reference) {
		t.Fatal("complete summary value changed")
	}
	if reflect.ValueOf(current.conn).Pointer() != reflect.ValueOf(source.conn).Pointer() || reflect.ValueOf(current.retConn).Pointer() != reflect.ValueOf(source.retConn).Pointer() || reflect.ValueOf(current.written).Pointer() != reflect.ValueOf(source.written).Pointer() || reflect.ValueOf(current.reach).Pointer() != reflect.ValueOf(source.reach).Pointer() {
		t.Fatal("settled connectivity alias changed")
	}
	check := func(got, want, old []storeFlowEdgeKind) {
		if (got == nil) != (want == nil) || len(got) != len(want) || cap(got) != cap(want) {
			t.Fatal("scalar slice shape/cap changed")
		}
		if len(old) == 0 && reflect.ValueOf(got).Pointer() != reflect.ValueOf(want).Pointer() {
			t.Fatal("zero-length backing identity changed")
		}
		if len(got) > 0 && reflect.ValueOf(got).Pointer() == reflect.ValueOf(old).Pointer() {
			t.Fatal("mutable source kind backing shared")
		}
	}
	check(current.retKind, reference.retKind, source.retKind)
	if (current.reachKind == nil) != (reference.reachKind == nil) || cap(current.reachKind) != cap(reference.reachKind) {
		t.Fatal("outer kind slice shape/cap changed")
	}
	if len(source.reachKind) == 0 && reflect.ValueOf(current.reachKind).Pointer() != reflect.ValueOf(reference.reachKind).Pointer() {
		t.Fatal("zero outer backing identity changed")
	}
	for i, row := range current.reachKind {
		check(row, reference.reachKind[i], source.reachKind[i])
	}
}
func TestStoreFlowPoolExactOldNilEmptyAliasCapacityPolicy(t *testing.T) {
	zeroBacking := make([]storeFlowEdgeKind, 4)
	zeroOuter := make([][]storeFlowEdgeKind, 4)
	for _, table := range []map[*ast.FuncDecl]*storeFlowSummary{nil, {}} {
		got, ok := tryCloneStoreFlowSummaryPool(table)
		if !ok || (got == nil) != (table == nil) {
			t.Fatal("nil/empty map changed")
		}
	}
	first, alias, other, unknown, lazy := &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}, &ast.FuncDecl{}
	for _, n := range []int{0, 1, 2, 7, 8, 9, 17, 31, 43, 65} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			kinds := make([]storeFlowEdgeKind, n, n+9)
			for i := range kinds {
				kinds[i] = storeFlowEdgeElements
			}
			outer := make([][]storeFlowEdgeKind, n, n+7)
			for i := range outer {
				outer[i] = kinds
			}
			if n > 1 {
				outer[0] = nil
				outer[1] = zeroBacking[:0]
			}
			source := &storeFlowSummary{conn: [][]bool{{true}}, retConn: []bool{true}, written: []bool{true}, reach: [][]bool{{true}}, retKind: kinds, reachKind: outer}
			if n == 0 {
				source.retKind = zeroBacking[:0]
				source.reachKind = zeroOuter[:0]
			}
			distinct := *source
			table := map[*ast.FuncDecl]*storeFlowSummary{nil: nil, first: source, alias: source, other: &distinct, unknown: nil, lazy: {}}
			got, ok := tryCloneStoreFlowSummaryPool(table)
			if !ok {
				t.Fatal("small fixture declined")
			}
			want := cloneStoreFlowSummariesForBodyWorker(table)
			if len(got) != len(want) || got[first] != got[alias] || got[first] == source || got[first] == got[other] {
				t.Fatal("summary pointer alias groups changed")
			}
			for fn, value := range want {
				assertPooledSummaryShape(t, table[fn], got[fn], value)
			}
			if n > 2 {
				oldOther := append([]storeFlowEdgeKind(nil), got[other].retKind...)
				oldRow := append([]storeFlowEdgeKind(nil), got[first].reachKind[2]...)
				got[first].retKind[0] = storeFlowEdgeHeader
				if !reflect.DeepEqual(got[other].retKind, oldOther) || !reflect.DeepEqual(got[first].reachKind[2], oldRow) || source.retKind[0] != storeFlowEdgeElements {
					t.Fatal("independent mutable cells alias")
				}
				expanded := got[first].retKind[:cap(got[first].retKind)]
				for i := len(got[first].retKind); i < len(expanded); i++ {
					expanded[i] = storeFlowEdgeHeader
				}
				if !reflect.DeepEqual(got[other].retKind, oldOther) || !reflect.DeepEqual(got[first].reachKind[2], oldRow) {
					t.Fatal("reslice-to-cap crossed partition")
				}
				var nextRow []storeFlowEdgeKind
				if n > 3 {
					nextRow = append([]storeFlowEdgeKind(nil), got[first].reachKind[3]...)
				}
				row := got[first].reachKind[2]
				row = append(row, storeFlowEdgeHeader)
				_ = row
				if n > 3 && !reflect.DeepEqual(got[first].reachKind[3], nextRow) {
					t.Fatal("append crossed row partition")
				}
				headers := got[first].reachKind[:cap(got[first].reachKind)]
				for i := len(got[first].reachKind); i < len(headers); i++ {
					headers[i] = []storeFlowEdgeKind{storeFlowEdgeHeader}
				}
				if got[other].reachKind[0] != nil {
					t.Fatal("outer header capacity crossed summary partition")
				}
				if !reflect.DeepEqual(got[other].retKind, oldOther) {
					t.Fatal("append crossed summary partition")
				}
			}
		})
	}
}
func TestStoreFlowPoolInitializationAndBoundsFallback(t *testing.T) {
	fn, alias := &ast.FuncDecl{}, &ast.FuncDecl{}
	source := newStoreFlowSummary(1)
	got := cloneStoreFlowSummariesForSpecialization(map[*ast.FuncDecl]*storeFlowSummary{fn: source, alias: source})
	if got[fn] != got[alias] || got[fn].retKind != nil {
		t.Fatal("lazy readiness changed")
	}
	storeFlowInitKinds(got[fn])
	storeFlowLowerKinds(got[fn], storeFlowEdgeNone)
	if source.retKind != nil || source.reachKind != nil {
		t.Fatal("private initialization mutated source")
	}
	large := &storeFlowSummary{retKind: make([]storeFlowEdgeKind, specializationSummaryPoolKindElements+1)}
	large.retKind[len(large.retKind)-1] = storeFlowEdgeHeader
	table := map[*ast.FuncDecl]*storeFlowSummary{fn: large}
	if _, ok := tryCloneStoreFlowSummaryPool(table); ok {
		t.Fatal("pool size bound ignored")
	}
	actual := cloneStoreFlowSummariesForSpecialization(table)
	expected := cloneStoreFlowSummariesForBodyWorker(table)
	assertPooledSummaryShape(t, large, actual[fn], expected[fn])
}
func TestStoreFlowPoolActualConstructorAndPublicationBoundary(t *testing.T) {
	source := "def first[T](value: T) -> T:\n    return value\n\ndef keep(value: i64) -> i64:\n    return value\n"
	for i := 0; i < specializationSummaryPoolMinObjects; i++ {
		source += fmt.Sprintf("\ndef pad%d(value: i64) -> i64:\n    return value\n", i)
	}
	root := analyzeTreeTestSource(t, "private_pool_specialization.elisa", source)
	fn := root.File.Decls[0].(*ast.FuncDecl)
	for _, decl := range root.File.Decls {
		if plain, ok := decl.(*ast.FuncDecl); ok && plain != fn {
			root.analyzer.storeFlowKindsFor(plain)
		}
	}
	if len(root.analyzer.storeFlowSummaries) < specializationSummaryPoolMinObjects {
		t.Fatal("actual constructor fixture did not enter pooling", len(root.analyzer.storeFlowSummaries))
	}
	var settled *ast.FuncDecl
	for _, decl := range retainedWalkDecls(root.File) {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "keep" {
			settled = candidate
		}
	}
	if settled == nil {
		t.Fatal("missing real plain callee")
	}
	summary := root.analyzer.storeFlowKindsFor(settled)
	if summary == nil {
		t.Fatal("actual summary did not settle")
	}
	private := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	ordinary := cloneAnalyzerValueForBodyWorker(root.analyzer).Addr().Interface().(*Analyzer)
	expected := cloneStoreFlowSummariesForBodyWorker(root.analyzer.storeFlowSummaries)
	assertPooledSummaryShape(t, summary, private.storeFlowSummaries[settled], expected[settled])
	assertPooledSummaryShape(t, summary, ordinary.storeFlowSummaries[settled], expected[settled])
	// A summary is not a reviewed publication fact; no interior pool pointer may
	// escape through the existing sparse publication/schema boundary.
	if err := validateSparseFactValueType(reflect.ValueOf(private.storeFlowSummaries[settled])); err == nil {
		t.Fatal("private summary could enter publication fact closure")
	}
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"]})
	if err != nil || product == nil {
		t.Fatal(err)
	}
	if product.Result.analyzer != nil || len(product.Result.specializationQueries.types.storeFlowSummaries) != 0 {
		t.Fatal("publication retained private summary pool")
	}
	if root.analyzer.storeFlowSummaries[settled] != summary {
		t.Fatal("constructor changed canonical summary")
	}
}

var summaryPoolSink map[*ast.FuncDecl]*storeFlowSummary

func BenchmarkStoreFlowSummaryPool(b *testing.B) {
	for _, size := range []int{0, 1, 128, 11308} {
		source := map[*ast.FuncDecl]*storeFlowSummary{}
		for i := 0; i < size; i++ {
			summary := newStoreFlowSummary(3)
			storeFlowInitKinds(summary)
			source[&ast.FuncDecl{}] = summary
		}
		for _, mode := range []string{"old_exact", "pooled_exact"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if mode == "old_exact" {
						summaryPoolSink = cloneStoreFlowSummariesForBodyWorker(source)
					} else {
						summaryPoolSink = cloneStoreFlowSummariesForSpecialization(source)
					}
				}
			})
		}
	}
}
