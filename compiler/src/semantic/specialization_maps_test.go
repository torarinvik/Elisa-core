package semantic

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	compilerast "elisacore/src/ast"
)

func TestSpecializationOpaqueMapSchemaAndPolicy(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	source, err := os.ReadFile(filepath.Join(filepath.Dir(file), "analyzer.go"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "analyzer.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		typ, ok := node.(*ast.TypeSpec)
		if !ok || typ.Name.Name != "Analyzer" {
			return true
		}
		for _, field := range typ.Type.(*ast.StructType).Fields.List {
			if _, ok := field.Type.(*ast.MapType); ok {
				for _, name := range field.Names {
					if name.Name != "exprTypes" && name.Name != "exprFacts" && name.Name != "iterBinderElementStates" && name.Name != "optimizationSymbolIDs" && name.Name != "storeScanBinderTypes" && name.Name != "storeScanParamFactsCache" && name.Name != "functionAnalyses" {
						expected = append(expected, name.Name)
					}
				}
			}
		}
		return false
	})
	sort.Strings(expected)
	if !reflect.DeepEqual(expected, specializationOpaqueMapFields[:]) {
		t.Fatal("Analyzer direct-map schema changed; regenerate specialization_maps_generated.go")
	}
	generated, err := os.ReadFile(filepath.Join(filepath.Dir(file), "specialization_maps_generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "field.Kind() != reflect.Map || typeHoldsMapInline(field.Type().Elem())") {
		t.Fatal("typed copies lost the exact existing runtime resource guard")
	}
}

func TestSpecializationOpaqueMapsMatchReflectiveNilEmptyAndValues(t *testing.T) {
	for _, name := range specializationOpaqueMapFields {
		t.Run(name, func(t *testing.T) {
			for _, size := range []int{-1, 0, 1} {
				src, dst := new(Analyzer), new(Analyzer)
				field := settable(reflect.ValueOf(src).Elem().FieldByName(name))
				if size >= 0 {
					field.Set(reflect.MakeMap(field.Type()))
					if size > 0 {
						field.SetMapIndex(reflect.Zero(field.Type().Key()), reflect.Zero(field.Type().Elem()))
					}
				}
				before := deepCloneValue(field)
				handled := cloneSpecializationOpaqueMap(dst, src, name, field)
				if handled == typeHoldsMapInline(field.Type().Elem()) {
					t.Fatal("typed guard disagrees with reflective resource policy")
				}
				if !handled {
					continue
				}
				got := settable(reflect.ValueOf(dst).Elem().FieldByName(name))
				if !reflect.DeepEqual(got.Interface(), before.Interface()) || got.IsNil() != field.IsNil() {
					t.Fatal("typed map copy differs from old nil/empty/entry semantics")
				}
				if !field.IsNil() && got.Pointer() == field.Pointer() {
					t.Fatal("outer map remained aliased")
				}
			}
		})
	}
}

func TestSpecializationOpaqueFactsPreservePointerAliasesAndOuterIsolation(t *testing.T) {
	extent := new(OptimizationExtent)
	first, second := new(compilerast.Ident), new(compilerast.Ident)
	facts := OptimizationFacts{Exclusive: true, ReadOnly: true, Extent: extent, base: "shared"}
	src := &Analyzer{exprFacts: map[compilerast.Expr]OptimizationFacts{first: facts, second: facts}}
	dst := cloneAnalyzerValueForSpecialization(src).Addr().Interface().(*Analyzer)
	if dst.exprFact(first).Extent != extent || dst.exprFact(second).Extent != extent {
		t.Fatal("pointer alias policy changed")
	}
	dst.deleteExprFacts(first)
	if len(src.exprFacts) != 2 {
		t.Fatal("private mutation reached parent")
	}
}

func TestSpecializationOpaqueRuntimePredicateEdges(t *testing.T) {
	cases := []struct {
		value any
		holds bool
	}{
		{[0]map[string]int{}, true}, // Existing predicate is conservative at len0.
		{[0][]int{}, true},
		{[0]int{}, false},
		{[2]*map[string]int{}, false},
		{struct{ Value any }{map[string]int{"x": 1}}, false},
		{struct{ Value []int }{}, true},
		{(*[]int)(nil), false},
	}
	for _, item := range cases {
		value := reflect.ValueOf(item.value)
		if typeHoldsMapInline(value.Type()) != item.holds {
			t.Fatalf("unexpected predicate for %v", value.Type())
		}
		if !item.holds && !reflect.DeepEqual(deepCloneValue(value).Interface(), item.value) {
			t.Fatal("opaque value changed under old reference")
		}
	}
	// NaN keys are nonreflexive but must retain every entry; both the old
	// reflection loop and maps.Clone insert keys unchanged, never normalizing.
	nan := math.Float64frombits(0x7ff8000000000042)
	values := map[float64]int{nan: 1}
	values[nan] = 2
	cloned := deepCloneValue(reflect.ValueOf(values)).Interface().(map[float64]int)
	typed := maps.Clone(values)
	if len(cloned) != len(values) || len(typed) != len(values) {
		t.Fatal("reference unexpectedly collapsed NaN keys")
	}
	var oldValues, typedValues []int
	for key, value := range cloned {
		if math.Float64bits(key) != math.Float64bits(nan) {
			t.Fatal("reference changed NaN key bits")
		}
		oldValues = append(oldValues, value)
	}
	for key, value := range typed {
		if math.Float64bits(key) != math.Float64bits(nan) {
			t.Fatal("typed clone changed NaN key bits")
		}
		typedValues = append(typedValues, value)
	}
	sort.Ints(oldValues)
	sort.Ints(typedValues)
	if !reflect.DeepEqual(oldValues, typedValues) {
		t.Fatal("nonreflexive-key entry multiplicities changed")
	}
}

func TestSpecializationOpaqueInterfacesAndComparableArrayKeys(t *testing.T) {
	data := []int{1, 2}
	nested := map[string]int{"x": 1}
	var nilPointer *int
	input := map[any]any{
		[0]int{}:                    data,
		[2]any{"name", (*int)(nil)}: nested,
		nil:                         nil,
		"typed-nil":                 nilPointer,
	}
	old := deepCloneValue(reflect.ValueOf(input)).Interface().(map[any]any)
	typed := maps.Clone(input)
	if !reflect.DeepEqual(old, typed) {
		t.Fatal("opaque interface/array-key content differs")
	}
	if reflect.ValueOf(typed[[0]int{}]).Pointer() != reflect.ValueOf(data).Pointer() || reflect.ValueOf(old[[0]int{}]).Pointer() != reflect.ValueOf(data).Pointer() {
		t.Fatal("interface-held slice identity changed")
	}
	key := [2]any{"name", (*int)(nil)}
	typed[key].(map[string]int)["x"] = 2
	if old[key].(map[string]int)["x"] != 2 || nested["x"] != 2 {
		t.Fatal("interface-held map alias policy changed")
	}
	delete(typed, nil)
	if _, ok := input[nil]; !ok {
		t.Fatal("outer map unexpectedly shared")
	}
}

func TestSpecializationOpaqueHelperUnknownAndResourceFallback(t *testing.T) {
	src, dst := new(Analyzer), new(Analyzer)
	field := reflect.ValueOf(map[string]int{})
	if cloneSpecializationOpaqueMap(dst, src, "futureUnknownMap", field) {
		t.Fatal("unknown field bypassed reflective fallback")
	}
	if cloneSpecializationOpaqueMap(dst, src, "exprTypes", field) || cloneSpecializationOpaqueMap(dst, src, "exprFacts", field) || cloneSpecializationOpaqueMap(dst, src, "iterBinderElementStates", field) || cloneSpecializationOpaqueMap(dst, src, "functionAnalyses", field) || cloneSpecializationOpaqueMap(dst, src, "optimizationSymbolIDs", field) || cloneSpecializationOpaqueMap(dst, src, "storeScanBinderTypes", field) || cloneSpecializationOpaqueMap(dst, src, "storeScanParamFactsCache", field) {
		t.Fatal("specialization-specific overlay fields bypassed owner policy")
	}
	if cloneSpecializationOpaqueMap(dst, src, "exprFacts", reflect.ValueOf(map[string][]int{})) {
		t.Fatal("resource-bearing value accepted")
	}
}

func TestSpecializationStoreScanBoolSetCachesShareRowsAndIsolateOuterMaps(t *testing.T) {
	decl := &compilerast.FuncDecl{Name: "cached"}
	newDecl := &compilerast.FuncDecl{Name: "private"}
	makeRow := func() map[string]bool { return map[string]bool{"present": true, "absent": false} }
	source := &Analyzer{
		storeScanViewCache:     map[*compilerast.FuncDecl]map[string]bool{decl: makeRow()},
		storeScanScalarCache:   map[*compilerast.FuncDecl]map[string]bool{decl: makeRow()},
		storeScanReadOnlyCache: map[*compilerast.FuncDecl]map[string]bool{decl: makeRow()},
	}
	specialization := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	ordinary := cloneAnalyzerValueForBodyWorker(source).Addr().Interface().(*Analyzer)

	check := func(name string, original, private, worker map[*compilerast.FuncDecl]map[string]bool) {
		t.Helper()
		if original == nil || private == nil || worker == nil {
			t.Fatalf("%s cache nil shape changed", name)
		}
		if reflect.ValueOf(original).Pointer() == reflect.ValueOf(private).Pointer() {
			t.Fatalf("%s specialization shares the mutable outer map", name)
		}
		if reflect.ValueOf(original[decl]).Pointer() != reflect.ValueOf(private[decl]).Pointer() {
			t.Fatalf("%s specialization failed to share the settled read-only row", name)
		}
		if reflect.ValueOf(original).Pointer() == reflect.ValueOf(worker).Pointer() || reflect.ValueOf(original[decl]).Pointer() == reflect.ValueOf(worker[decl]).Pointer() {
			t.Fatalf("%s ordinary worker lost deep map isolation", name)
		}
		private[newDecl] = map[string]bool{"private": true}
		delete(private, decl)
		if _, ok := original[newDecl]; ok {
			t.Fatalf("%s specialization insertion leaked to source", name)
		}
		if _, ok := original[decl]; !ok {
			t.Fatalf("%s specialization deletion leaked to source", name)
		}
		worker[decl]["present"] = false
		if !original[decl]["present"] {
			t.Fatalf("%s ordinary worker row mutation leaked to source", name)
		}
	}
	check("view", source.storeScanViewCache, specialization.storeScanViewCache, ordinary.storeScanViewCache)
	check("scalar", source.storeScanScalarCache, specialization.storeScanScalarCache, ordinary.storeScanScalarCache)
	check("read-only", source.storeScanReadOnlyCache, specialization.storeScanReadOnlyCache, ordinary.storeScanReadOnlyCache)

	// Concurrent private construction reads the same settled rows while each
	// specialization writes only to its own outer maps.
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			private := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
			if !private.storeScanViewCache[decl]["present"] || !private.storeScanScalarCache[decl]["present"] || !private.storeScanReadOnlyCache[decl]["present"] {
				t.Error("concurrent specialization failed to read a settled row")
			}
			private.storeScanViewCache[newDecl] = map[string]bool{"private": true}
			if _, leaked := source.storeScanViewCache[newDecl]; leaked {
				t.Error("concurrent specialization outer-map write leaked to source")
			}
		}()
	}
	readers.Wait()
}

func TestSpecializationOpaqueHookPreservesOrdinaryWorkersAndDeepFallback(t *testing.T) {
	source := &Analyzer{storeScanViewCache: map[*compilerast.FuncDecl]map[string]bool{
		nil: {"present": true},
	}}
	ordinary := cloneAnalyzerValueForBodyWorker(source).Interface().(Analyzer)
	ordinary.storeScanViewCache[nil]["present"] = false
	if !source.storeScanViewCache[nil]["present"] {
		t.Fatal("ordinary worker lost reflective deep fallback")
	}
	_, file, _, _ := runtime.Caller(0)
	text, err := os.ReadFile(filepath.Join(filepath.Dir(file), "parallel_bodies.go"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(text), "func cloneAnalyzerValueForParallelPolicy(")
	if start < 0 {
		t.Fatal("clone policy function missing")
	}
	end := strings.Index(string(text)[start:], "\nfunc parallelBodyWorkerRebuildMap(")
	if end < 0 {
		t.Fatal("clone policy source boundary changed; review hook policy")
	}
	body := string(text)[start : start+end]
	hook := strings.Index(body, "if ownedFunctionAnalyses && cloneSpecializationOpaqueMap(")
	storeScanHook := strings.Index(body, "if ownedFunctionAnalyses && cloneSpecializationStoreScanBoolSetCache(")
	declarationPolicy := strings.Index(body, "parallelDeclarationMapIsReadOnly(name)")
	fallback := strings.Index(body, "settable(cloned.Field(i)).Set(deepCloneValue(settable(field)))")
	if storeScanHook < declarationPolicy || storeScanHook < 0 || storeScanHook > hook || hook < 0 || fallback < hook {
		t.Fatal("typed map hook moved ahead of owner-specific policies or lost specialization guard")
	}
}

var specializationStoreScanSetCloneSink *Analyzer

func BenchmarkSpecializationStoreScanBoolSetCacheCopy(b *testing.B) {
	const rows = 2048
	names := [...]string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	makeRows := func() map[*compilerast.FuncDecl]map[string]bool {
		cache := make(map[*compilerast.FuncDecl]map[string]bool, rows)
		for i := 0; i < rows; i++ {
			row := make(map[string]bool, len(names))
			for j, name := range names {
				row[name] = (i+j)%2 == 0
			}
			cache[&compilerast.FuncDecl{}] = row
		}
		return cache
	}
	root := &Analyzer{
		storeScanViewCache:     makeRows(),
		storeScanScalarCache:   makeRows(),
		storeScanReadOnlyCache: makeRows(),
	}
	for _, mode := range []string{"ordinary_worker_deep_clone", "specialization_clone"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if mode == "ordinary_worker_deep_clone" {
					specializationStoreScanSetCloneSink = cloneAnalyzerValueForBodyWorker(root).Addr().Interface().(*Analyzer)
				} else {
					specializationStoreScanSetCloneSink = cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
				}
			}
		})
	}
}
