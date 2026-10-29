package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestTypedSpecializationRegionCloneParity(t *testing.T) {
	sym := &Symbol{}
	packedType := &PackedEnumStoreType{}
	first, second := &ast.FuncDecl{}, &ast.FuncDecl{}
	shared := map[string]regionRefState{"leaf": {ParamDeps: intBitSetOf(130)}}
	state := regionRefState{Deps: map[*Symbol]regionDependencyState{sym: {Valid: true}}, StoreDeps: map[*Symbol]packedStoreDependencyState{sym: {Type: packedType}}, ParamDeps: intBitSetOf(130), Fields: map[string]regionRefState{"a": {Fields: shared}, "b": {Fields: shared}, "empty": {Fields: map[string]regionRefState{}}}}
	for _, src := range []map[*ast.FuncDecl]regionRefState{nil, {}, {first: state, second: {}}} {
		got := cloneSpecializationReturnElementSummaries(src)
		fallback := cloneSpecializationReturnElementSummariesGuarded(src, false)
		want := deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl]regionRefState)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(fallback, want) {
			t.Fatal("typed clone differs from reflective clone")
		}
		if src == nil || len(src) == 0 {
			continue
		}
		x := got[first]
		if x.StoreDeps[sym].Type != packedType {
			t.Fatal("packed type pointer identity changed")
		}
		empty := cloneSpecializationReturnElementSummaries(map[*ast.FuncDecl]regionRefState{first: {Deps: map[*Symbol]regionDependencyState{}, StoreDeps: map[*Symbol]packedStoreDependencyState{}}})[first]
		if empty.Deps == nil || empty.StoreDeps == nil || len(empty.Deps) != 0 || len(empty.StoreDeps) != 0 {
			t.Fatal("non-nil empty dependency maps changed")
		}
		delete(x.Deps, sym)
		delete(x.StoreDeps, sym)
		delete(x.Fields, "empty")
		x.Fields["a"].Fields["changed"] = regionRefState{}
		if len(src[first].Deps) != 1 || len(src[first].StoreDeps) != 1 || len(src[first].Fields) != 3 || len(shared) != 1 {
			t.Fatal("clone mutated canonical maps")
		}
		if _, ok := x.Fields["b"].Fields["changed"]; ok {
			t.Fatal("sibling maps no longer follow reflective alias policy")
		}
		if cap(x.ParamDeps.extra) != len(x.ParamDeps.extra) || &x.ParamDeps.extra[0] != &src[first].ParamDeps.extra[0] {
			t.Fatal("slice backing/capacity policy changed")
		}
	}
}
func TestTypedSpecializationRegionCloneCycle(t *testing.T) {
	cyclic := map[string]regionRefState{}
	cyclic["self"] = regionRefState{Fields: cyclic}
	src := map[*ast.FuncDecl]regionRefState{&ast.FuncDecl{}: {Fields: cyclic}}
	for _, v := range cloneSpecializationReturnElementSummaries(src) {
		v.Fields["self"].Fields["new"] = regionRefState{}
		if _, ok := v.Fields["new"]; !ok {
			t.Fatal("cycle not preserved")
		}
		if _, ok := cyclic["new"]; ok {
			t.Fatal("cycle aliases original")
		}
	}
}

func TestSpecializationRegionCloneSchemaGuard(t *testing.T) {
	if !specializationRegionSchema() {
		t.Fatal("current schema unexpectedly rejected")
	}
	type original struct {
		A int
		B []uint64
	}
	type added struct {
		A int
		B []uint64
		C map[int]bool
	}
	type changed struct {
		A int
		B []string
	}
	schema := []specializationCloneField{{"A", reflect.TypeFor[int]()}, {"B", reflect.TypeFor[[]uint64]()}}
	if !specializationCloneSchemaMatches(reflect.TypeFor[original](), schema) || specializationCloneSchemaMatches(reflect.TypeFor[added](), schema) || specializationCloneSchemaMatches(reflect.TypeFor[changed](), schema) {
		t.Fatal("schema guard accepted an incompatible shape")
	}
}

var regionCloneBenchmarkSink map[*ast.FuncDecl]regionRefState

func BenchmarkSpecializationReturnElementSummaryClone(b *testing.B) {
	src := make(map[*ast.FuncDecl]regionRefState, 133)
	for i := 0; i < 133; i++ {
		src[&ast.FuncDecl{}] = regionRefState{Fields: map[string]regionRefState{"nested": {Fields: map[string]regionRefState{"leaf": {ParamDeps: intBitSetOf(130)}}}}}
	}
	b.Run("reflect", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			regionCloneBenchmarkSink = deepCloneValue(reflect.ValueOf(src)).Interface().(map[*ast.FuncDecl]regionRefState)
		}
	})
	b.Run("typed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			regionCloneBenchmarkSink = cloneSpecializationReturnElementSummaries(src)
		}
	})
	b.Run("frozen", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			regionCloneBenchmarkSink = cloneSpecializationFrozenReturnElementSummaries(src)
		}
	})
}

func TestSpecializationFrozenReturnSummaryRowsArePrivate(t *testing.T) {
	first, other := &ast.FuncDecl{Name: "first"}, &ast.FuncDecl{Name: "other"}
	nested := regionRefState{Fields: map[string]regionRefState{"leaf": {ParamDeps: intBitSetOf(130)}}}
	base := map[*ast.FuncDecl]regionRefState{first: nested}
	worker := cloneSpecializationFrozenReturnElementSummaries(base)
	delete(worker, first)
	worker[other] = regionRefState{}
	if _, ok := base[first]; !ok || len(base) != 1 || !base[first].Fields["leaf"].ParamDeps.Contains(130) {
		t.Fatal("private row replacement changed the published summary")
	}
}
