//go:build cgo

package semantic

import (
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func allExprFactsOverlayTestFields() OptimizationFacts {
	return OptimizationFacts{
		Exclusive: true, ReadOnly: true, Contiguous: true, UnitStride: true,
		FrozenPackedStoreOnly: true, base: "base-with-provenance",
		PackedStoreProvenance: PackedStoreProvenance{
			HasPackedStoreDeps: true, HasFrozenPackedStoreDeps: true,
			HasNonFrozenPackedStoreDeps: true, HasNonStoreProvenance: true,
		},
		Extent: &OptimizationExtent{Kind: OptimizationExtentViewBounds,
			Begin: "0", End: "4", Size: "4", HasConstSize: true, ConstSize: 4},
	}
}

func TestAnalyzerExprFactsOverlayMatchesExactShallowReference(t *testing.T) {
	first, zero, absent := &ast.IntLit{Value: "1"}, &ast.IntLit{Value: "2"}, &ast.IntLit{Value: "3"}
	alias := &ast.IntLit{Value: "4"}
	facts := allExprFactsOverlayTestFields()
	base := map[ast.Expr]OptimizationFacts{first: facts, zero: {}, alias: facts}
	expected := deepCloneValue(reflect.ValueOf(base)).Interface().(map[ast.Expr]OptimizationFacts)
	source := &Analyzer{exprFacts: base}
	worker := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	logical := map[ast.Expr]OptimizationFacts{}
	worker.rangeExprFacts(func(expr ast.Expr, facts OptimizationFacts) bool { logical[expr] = facts; return true })
	if !reflect.DeepEqual(logical, expected) || worker.exprFactsLen() != len(expected) {
		t.Fatal("overlay changed complete fact values or membership")
	}
	if len(worker.exprFacts) != 0 || reflect.ValueOf(worker.exprFactsOverlay.base).Pointer() != reflect.ValueOf(base).Pointer() {
		t.Fatal("specialization copied/materialized the canonical fact table")
	}
	if got, present := worker.lookupExprFacts(zero); !present || got != (OptimizationFacts{}) {
		t.Fatal("present zero facts collapsed to absence")
	}
	if _, present := worker.lookupExprFacts(absent); present {
		t.Fatal("unknown/private key invented a base fact")
	}
	if worker.exprFact(first).Extent != facts.Extent || worker.exprFact(alias).Extent != facts.Extent {
		t.Fatal("prior Extent pointer alias semantics changed")
	}
	worker.deleteExprFacts(first)
	if _, present := worker.lookupExprFacts(first); present {
		t.Fatal("deletion resurrected base facts")
	}
	worker.setExprFacts(first, OptimizationFacts{})
	if got, present := worker.lookupExprFacts(first); !present || got != (OptimizationFacts{}) {
		t.Fatal("present-zero local write did not shadow a tombstone/base")
	}
	worker.deleteExprFacts(first)
	worker.setExprFacts(absent, facts)
	if len(base) != 3 || base[first] != facts {
		t.Fatal("private writes/deletes changed canonical map values")
	}
	calls := 0
	worker.rangeExprFacts(func(ast.Expr, OptimizationFacts) bool { calls++; return false })
	if calls != 1 {
		t.Fatal("logical range ignored early termination")
	}
	clone := cloneAnalyzerValueForBodyWorker(worker).Addr().Interface().(*Analyzer)
	clone.deleteExprFacts(alias)
	if _, present := worker.lookupExprFacts(alias); !present {
		t.Fatal("ordinary worker shared writable fact-overlay tombstones")
	}
	if reflect.ValueOf(clone.exprFactsOverlay.base).Pointer() != reflect.ValueOf(base).Pointer() {
		t.Fatal("ordinary worker materialized a construction base")
	}
	// Saving/restoring logical observations uses the exact full value, including
	// present-zero state. No existing production whole-map save/restore is present.
	saved, present := worker.lookupExprFacts(zero)
	worker.deleteExprFacts(zero)
	if present {
		worker.setExprFacts(zero, saved)
	}
	if got, exists := worker.lookupExprFacts(zero); !exists || got != saved {
		t.Fatal("logical save/restore changed zero membership")
	}
	final := map[ast.Expr]OptimizationFacts{}
	worker.rangeExprFacts(func(expr ast.Expr, facts OptimizationFacts) bool {
		if _, duplicate := final[expr]; duplicate {
			t.Fatal("logical range duplicated a shadowed base key")
		}
		final[expr] = facts
		return true
	})
	if want := map[ast.Expr]OptimizationFacts{zero: {}, alias: facts, absent: facts}; !reflect.DeepEqual(final, want) || worker.exprFactsLen() != len(want) {
		t.Fatal("logical range/length changed after local writes and tombstones")
	}
	for _, value := range []map[ast.Expr]OptimizationFacts{nil, {}} {
		cloned := cloneAnalyzerValueForSpecialization(&Analyzer{exprFacts: value}).Addr().Interface().(*Analyzer)
		if (value == nil) != (cloned.exprFacts == nil) || cloned.hasExprFactsStorage() != (value != nil) {
			t.Fatal("nil/empty storage distinction changed")
		}
	}
}

var specializationExprFactsCloneSink *Analyzer

func TestAnalyzerExprFactsRecomputationRetractsBaseWithoutResurrection(t *testing.T) {
	expr := &ast.IntLit{Value: "1"}
	base := allExprFactsOverlayTestFields()
	source := &Analyzer{exprFacts: map[ast.Expr]OptimizationFacts{expr: base}}
	worker := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	worker.recordExprOptimizationFacts(expr, &BuiltinType{Name: "i64"})
	if _, present := worker.lookupExprFacts(expr); present {
		t.Fatal("recording a scalar with no facts resurrected the old base facts")
	}
	if source.exprFacts[expr] != base {
		t.Fatal("fact retraction mutated the canonical producer table")
	}
}

func TestAnalyzerExprFactsOverlayConstructionDoesNotScaleWithCanonicalRows(t *testing.T) {
	makeSource := func(count int) *Analyzer {
		a := &Analyzer{exprFacts: map[ast.Expr]OptimizationFacts{}}
		facts := allExprFactsOverlayTestFields()
		for i := 0; i < count; i++ {
			a.exprFacts[&ast.IntLit{Value: fmt.Sprint(i)}] = facts
		}
		return a
	}
	small, large := makeSource(0), makeSource(8192)
	measure := func(a *Analyzer) float64 {
		return testing.AllocsPerRun(3, func() {
			specializationExprFactsCloneSink = cloneAnalyzerValueForSpecialization(a).Addr().Interface().(*Analyzer)
		})
	}
	if lo, hi := measure(small), measure(large); hi > lo+2 {
		t.Fatalf("constructor still traverses canonical fact rows: small=%v large=%v", lo, hi)
	}
	specializationExprFactsCloneSink = nil
}

func TestAnalyzerExprFactsOverlayRealPublicationAndSnapshotBoundaries(t *testing.T) {
	const source = `def keep[T](value: T) -> T:
    return value

def foreign(values: i32[4]) -> i32[4]:
    return values
`
	root, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(root.Errors()) != 0 {
		t.Fatal(root.Errors())
	}
	keep := retainedProofNamedDecl(t, root, "keep").(*ast.FuncDecl)
	foreign := retainedProofNamedDecl(t, root, "foreign").(*ast.FuncDecl)
	expr := foreign.Body[0].(*ast.ReturnStmt).Value
	arrayType := root.ValueExprTypes(expr)
	wantForeign, present := root.LookupExprFacts(expr)
	if !present || !wantForeign.HasExactExtent() {
		t.Fatal("real foreign array fact fixture did not establish an exact extent")
	}
	before := maps.Clone(state.a.exprFacts)
	root.BeginSharedCodegen()
	sessionBase := maps.Clone(state.a.exprFacts)
	product, err := root.SpecializeFunction(keep, []Type{arrayType})
	if err != nil {
		root.EndSharedCodegen()
		t.Fatal(err)
	}
	private := product.Decl.Body[0].(*ast.ReturnStmt).Value
	if facts, ok := product.Result.LookupExprFacts(private); !ok || !facts.HasExactExtent() {
		t.Fatal("owned concrete array facts were not imported/recomputed/published")
	}
	if got, ok := product.Result.LookupExprFacts(expr); !ok || got != wantForeign {
		t.Fatal("canonical foreign fact/value/pointer fallback changed")
	}
	if product.Result.analyzer != nil || product.Result.specializationQueries.types.exprFactsOverlay != nil {
		t.Fatal("published product retained construction fact overlay/base")
	}
	if !reflect.DeepEqual(sessionBase, state.a.exprFacts) {
		root.EndSharedCodegen()
		t.Fatal("construction wrote to its canonical session fact base")
	}
	root.EndSharedCodegen()
	if !reflect.DeepEqual(before, state.a.exprFacts) || state.a.exprFactsOverlay != nil {
		t.Fatal("canonical fact map/lifecycle changed after codegen")
	}
	encoded, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(encoded)
	if err != nil || restored.a.exprFactsOverlay != nil {
		t.Fatal("canonical snapshot roundtrip retained construction facts", err)
	}
	worker := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	state.a.exprFactsOverlay = worker.exprFactsOverlay
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("active construction fact overlay persisted")
	}
	build := &AnalysisBuild{retained: state, file: root.File, loweredFile: root.File}
	if err := build.validate(); err == nil || !strings.Contains(err.Error(), "expression-facts overlay") {
		t.Fatal("analysis build admitted an active construction fact resource", err)
	}
	state.a.exprFactsOverlay = nil
	if err := validateSparseFactValueType(reflect.ValueOf(worker.exprFactsOverlay)); err == nil {
		t.Fatal("construction fact resource entered a public fact closure")
	}
	// Isolate fact-resource guards from the already committed type overlay guards.
	worker.exprTypeOverlay = nil
	panicked := func() (panicked bool) {
		defer func() { panicked = recover() != nil }()
		cloneAnalyzerValueForSpecialization(worker)
		return false
	}()
	if !panicked {
		t.Fatal("nested clone accepted a construction fact-overlay root")
	}
	if product, err := (&Result{analyzer: worker}).SpecializeFunction(keep, []Type{arrayType}); err == nil || product != nil {
		t.Fatal("nested construction accepted a fact-overlay root")
	}
	publication := worker.finishProgramResult(root.File, root.LoweredFile, nil, AnalyzeOptions{}, nil)
	if len(publication.Errors()) == 0 {
		t.Fatal("construction fact view published as a canonical whole program")
	}
	mergeValue(reflect.ValueOf(state.a).Elem(), reflect.ValueOf(worker).Elem(), reflect.ValueOf(state.a).Elem(), nil)
	if len(state.a.diagnostics) == 0 || state.a.exprFactsOverlay != nil {
		t.Fatal("fact overlay silently merged into canonical state")
	}
	count := len(worker.diagnostics)
	worker.analyzeDeclsParallel(nil, 2)
	if len(worker.diagnostics) <= count {
		t.Fatal("parallel scheduling accepted an active fact overlay")
	}
}

func TestAnalyzerExprFactAccessUsesClosedOverlayAPI(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	directory := filepath.Dir(filename)
	policy, err := os.ReadFile(filepath.Join(directory, "parallel_bodies.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(policy), "useExprFactsOverlay := ownedFunctionAnalyses && !typeHoldsMapInline(reflect.TypeFor[OptimizationFacts]())") {
		t.Fatal("fact overlay lost its exact old opaque-value schema guard")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	boundary := map[string]map[string]bool{
		"analyzer_expr_facts_overlay.go": {"lookupExprFacts": true, "setExprFacts": true, "deleteExprFacts": true, "rangeExprFacts": true, "exprFactsLen": true, "hasExprFactsStorage": true},
		"parallel_bodies.go":             {"cloneAnalyzerValueForParallelPolicy": true},
		"analyzer.go":                    {"finishProgramResult": true},
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parents := map[goast.Node]goast.Node{}
		var stack []goast.Node
		goast.Inspect(file, func(node goast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			if len(stack) != 0 {
				parents[node] = stack[len(stack)-1]
			}
			stack = append(stack, node)
			return true
		})
		goast.Inspect(file, func(node goast.Node) bool {
			function := ""
			for parent := node; parent != nil; parent = parents[parent] {
				if fn, ok := parent.(*goast.FuncDecl); ok {
					function = fn.Name.Name
					break
				}
			}
			if literal, ok := node.(*goast.BasicLit); ok && literal.Kind == token.STRING {
				value, _ := strconv.Unquote(literal.Value)
				if value == "exprFacts" && !exprFactsNamedAccessHasClosedPolicy(entry.Name(), function) {
					t.Errorf("named/reflection fact-table access needs explicit policy at %s", set.Position(literal.Pos()))
				}
			}
			if pair, ok := node.(*goast.KeyValueExpr); ok {
				if key, ok := pair.Key.(*goast.Ident); ok && key.Name == "exprFacts" && !(entry.Name() == "analyzer.go" && function == "analyzeWithLookupRecorder") {
					t.Errorf("Analyzer fact-map initializer needs explicit canonical-owner policy at %s", set.Position(pair.Pos()))
				}
			}
			selector, ok := node.(*goast.SelectorExpr)
			if !ok || selector.Sel.Name != "exprFacts" {
				return true
			}
			if !boundary[entry.Name()][function] {
				t.Errorf("raw Analyzer.exprFacts read/write/delete/range/len/alias bypasses overlay at %s (%s)", set.Position(selector.Pos()), function)
			}
			return true
		})
	}
}

func BenchmarkSpecializationExprFactsOverlayConstruction(b *testing.B) {
	// Measures only construction; no Stage1/end-to-end gain is implied.
	for _, rows := range []int{0, 8192, 65536} {
		b.Run(fmt.Sprintf("rows_%d", rows), func(b *testing.B) {
			a := &Analyzer{exprFacts: map[ast.Expr]OptimizationFacts{}}
			facts := allExprFactsOverlayTestFields()
			for i := 0; i < rows; i++ {
				a.exprFacts[&ast.IntLit{Value: fmt.Sprint(i)}] = facts
			}
			b.Run("ordinary_worker", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					specializationExprFactsCloneSink = cloneAnalyzerValueForBodyWorker(a).Addr().Interface().(*Analyzer)
				}
			})
			b.Run("overlay_specialization", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					specializationExprFactsCloneSink = cloneAnalyzerValueForSpecialization(a).Addr().Interface().(*Analyzer)
				}
			})
			specializationExprFactsCloneSink = nil
		})
	}
}

// Only these exact named consumers are permitted. This does not permit any raw
// selector access: capturePlainGlobalHeaderRows rejects construction resources,
// inventories unknown canonical rows fail-closed, and reads values logically;
// ownedRows only accounts for already captured immutable receipt labels.
func exprFactsNamedAccessHasClosedPolicy(file, function string) bool {
	switch file {
	case "parallel_bodies.go":
		return function == "cloneAnalyzerValueForParallelPolicy"
	case "specialization_accessors_gen.go":
		return function == ""
	case "retained_plain_identities.go":
		return function == "ownedRows" || function == "capturePlainGlobalHeaderRows"
	}
	return false
}

func TestExprFactsNamedInventoryPolicyRemainsFunctionScoped(t *testing.T) {
	if !exprFactsNamedAccessHasClosedPolicy("retained_plain_identities.go", "ownedRows") || !exprFactsNamedAccessHasClosedPolicy("retained_plain_identities.go", "capturePlainGlobalHeaderRows") {
		t.Fatal("inventoried immutable/canonical consumers lack policy")
	}
	for _, test := range [][2]string{{"retained_plain_identities.go", "arbitraryHelper"}, {"other.go", "capturePlainGlobalHeaderRows"}, {"retained_plain_identities.go", ""}} {
		if exprFactsNamedAccessHasClosedPolicy(test[0], test[1]) {
			t.Fatal("named table policy widened outside exact consumers")
		}
	}
}
