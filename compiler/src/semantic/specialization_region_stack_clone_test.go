//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func specializationRegionStackFixture(t *testing.T) (*Result, *ast.FuncDecl, *ast.RegionStmt, *ast.RegionStmt) {
	t.Helper()
	root := analyzeTreeTestSource(t, "specialization_region_stacks.elisa", `def region_copy_value[T](value: T) -> T:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        owned: mutable darray[i64] = []
        owned.push(1)
        return value

def unrelated() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        foreign: mutable darray[i64] = []
        foreign.push(2)
`)
	var fn *ast.FuncDecl
	for _, decl := range root.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "region_copy_value" {
			fn = candidate
		}
	}
	var owned, foreign *ast.RegionStmt
	for region, assignment := range root.RegionStacks {
		if _, ok := assignment.StackOf["owned"]; ok {
			owned = region
		}
		if _, ok := assignment.StackOf["foreign"]; ok {
			foreign = region
		}
	}
	if fn == nil || owned == nil || foreign == nil {
		t.Fatal("fixture lacks a generic target and distinct owned/foreign region facts")
	}
	return root, fn, owned, foreign
}

func TestSpecializationRegionStacksImportOnlyOwnedFacts(t *testing.T) {
	for _, source := range []map[*ast.RegionStmt]RegionStackAssignment{nil, {}} {
		worker := cloneAnalyzerValueForSpecialization(&Analyzer{regionStacks: source}).Addr().Interface().(*Analyzer)
		if (source == nil) != (worker.regionStacks == nil) || len(worker.regionStacks) != 0 {
			t.Fatal("region stack clone changed nil/empty shape")
		}
	}
	root, fn, owned, foreign := specializationRegionStackFixture(t)
	worker := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	if worker.regionStacks == nil || len(worker.regionStacks) != 0 {
		t.Fatal("construction copied canonical region rows")
	}
	isolateWorkerScopes(root.analyzer, worker)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	private, ok := context.syntax.nodes[owned].(*ast.RegionStmt)
	if !ok {
		t.Fatal("owned region was not isolated")
	}
	assignment, ok := worker.regionStacks[private]
	if !ok || !reflect.DeepEqual(assignment, root.RegionStacks[owned]) {
		t.Fatal("owned region assignment was not imported")
	}
	if _, ok := worker.regionStacks[owned]; ok {
		t.Fatal("canonical owned row remained in construction")
	}
	if _, ok := worker.regionStacks[foreign]; ok {
		t.Fatal("foreign row was imported")
	}
	assignment.StackOf["private_only"] = 99
	if _, ok := root.RegionStacks[owned].StackOf["private_only"]; ok {
		t.Fatal("owned nested fact map aliases the canonical row")
	}
	ordinary := cloneAnalyzerValueForBodyWorker(root.analyzer).Addr().Interface().(*Analyzer)
	if len(ordinary.regionStacks) != len(root.RegionStacks) {
		t.Fatal("ordinary worker lost region rows")
	}
	row := ordinary.regionStacks[foreign]
	row.StackOf["ordinary_only"] = 99
	if _, ok := root.RegionStacks[foreign].StackOf["ordinary_only"]; ok {
		t.Fatal("ordinary nested map isolation changed")
	}
}

func TestSpecializationRegionStacksPublicationPreservesForeignAndMasksOwnedMiss(t *testing.T) {
	root, fn, owned, foreign := specializationRegionStackFixture(t)
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	private, ok := product.Result.specializationNodes[owned].(*ast.RegionStmt)
	if !ok {
		t.Fatal("published view has no owned region mapping")
	}
	assignment, ok := product.Result.LookupRegionStacks(owned)
	if !ok || len(assignment.StackOf) == 0 {
		t.Fatal("current owned region assignment is missing")
	}
	if reflect.ValueOf(assignment.StackOf).Pointer() == reflect.ValueOf(root.RegionStacks[owned].StackOf).Pointer() {
		t.Fatal("published current assignment shares the canonical map")
	}
	unrelated, ok := product.Result.LookupRegionStacks(foreign)
	if !ok || reflect.ValueOf(unrelated.StackOf).Pointer() != reflect.ValueOf(root.RegionStacks[foreign].StackOf).Pointer() {
		t.Fatal("foreign canonical fallback changed")
	}
	// An owned absence must not resurrect the canonical template assignment.
	delete(product.Result.RegionStacks, private)
	if _, present := product.Result.LookupRegionStacks(owned); present {
		t.Fatal("owned miss exposed a stale canonical fact")
	}
	product.Result.RangeRegionStacks(func(region *ast.RegionStmt, _ RegionStackAssignment) bool {
		if region == owned || region == private {
			t.Error("range exposed an absent owned assignment")
		}
		return true
	})
	if _, present := root.RegionStacks[owned]; !present {
		t.Fatal("publication mutation reached the root")
	}
}

func BenchmarkSpecializationRegionStackClone(b *testing.B) {
	for _, count := range []int{0, 2571} {
		root := &Analyzer{regionStacks: map[*ast.RegionStmt]RegionStackAssignment{}}
		for i := 0; i < count; i++ {
			root.regionStacks[&ast.RegionStmt{}] = RegionStackAssignment{StackOf: map[string]int{"items": 1}, StackKind: map[int]string{1: "growable"}, StackStrategy: map[int]string{1: "chained"}, StackCount: 2}
		}
		b.Run(fmt.Sprintf("foreign_%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				specializationAnalysisCloneSink = cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
			}
		})
	}
}
