package semantic

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
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
					if name.Name != "exprTypes" && name.Name != "functionAnalyses" {
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
	dst := new(Analyzer)
	field := settable(reflect.ValueOf(src).Elem().FieldByName("exprFacts"))
	if !cloneSpecializationOpaqueMap(dst, src, "exprFacts", field) {
		t.Fatal("opaque OptimizationFacts not copied")
	}
	if dst.exprFacts[first].Extent != extent || dst.exprFacts[second].Extent != extent {
		t.Fatal("pointer alias policy changed")
	}
	delete(dst.exprFacts, first)
	if len(src.exprFacts) != 2 {
		t.Fatal("outer mutation reached parent")
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
		[0]int{}: data,
		[2]any{"name", (*int)(nil)}: nested,
		nil: nil,
		"typed-nil": nilPointer,
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
	if cloneSpecializationOpaqueMap(dst, src, "exprTypes", field) || cloneSpecializationOpaqueMap(dst, src, "functionAnalyses", field) {
		t.Fatal("specialization-specific overlay fields bypassed owner policy")
	}
	if cloneSpecializationOpaqueMap(dst, src, "exprFacts", reflect.ValueOf(map[string][]int{})) {
		t.Fatal("resource-bearing value accepted")
	}
}

func TestSpecializationOpaqueHookPreservesOrdinaryWorkersAndDeepFallback(t *testing.T) {
	source := &Analyzer{storeScanViewCache: map[*compilerast.FuncDecl]map[string]bool{
		nil: {"present": true},
	}}
	for _, specialization := range []bool{false, true} {
		cloned := cloneAnalyzerValueForParallelPolicy(source, specialization, specialization).Interface().(Analyzer)
		cloned.storeScanViewCache[nil]["present"] = false
		if !source.storeScanViewCache[nil]["present"] {
			t.Fatal("ordinary/specialization resource map lost reflective deep fallback")
		}
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
	declarationPolicy := strings.Index(body, "parallelDeclarationMapIsReadOnly(name)")
	fallback := strings.Index(body, "settable(cloned.Field(i)).Set(deepCloneValue(settable(field)))")
	if hook < declarationPolicy || hook < 0 || fallback < hook {
		t.Fatal("typed map hook moved ahead of owner-specific policies or lost specialization guard")
	}
}
