//go:build cgo

package semantic

import (
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzerExprTypeOverlayMembershipDeletionAndIteration(t *testing.T) {
	canonical := &ast.IntLit{Value: "1"}
	presentNil := &ast.IntLit{Value: "2"}
	typedNil := &ast.IntLit{Value: "3"}
	foreign := &ast.IntLit{Value: "4"}
	ownedMissing := &ast.IntLit{Value: "5"}
	unknown := &ast.IntLit{Value: "6"}
	i64 := &BuiltinType{Name: "i64"}
	u64 := &BuiltinType{Name: "u64"}
	var nilType *BuiltinType
	base := map[ast.Expr]Type{canonical: i64, presentNil: nil, typedNil: nilType, foreign: i64}
	worker := &Analyzer{exprTypes: map[ast.Expr]Type{}, exprTypeOverlay: &analyzerExprTypeOverlay{base: base}}
	for _, expr := range []ast.Expr{canonical, presentNil, typedNil, foreign} {
		got, present := worker.lookupExprType(expr)
		if !present || got != base[expr] {
			t.Fatal("canonical membership/value lost")
		}
	}
	for _, expr := range []ast.Expr{ownedMissing, unknown} {
		if _, present := worker.lookupExprType(expr); present {
			t.Fatal("unknown/private owned miss invented a base fact")
		}
	}
	worker.deleteExprType(canonical)
	if _, present := worker.lookupExprType(canonical); present {
		t.Fatal("explicit deletion resurrected canonical base value")
	}
	if base[canonical] != i64 {
		t.Fatal("deletion mutated immutable canonical base")
	}
	worker.exprTypes[canonical] = u64
	if got, present := worker.lookupExprType(canonical); !present || got != u64 {
		t.Fatal("private write did not override prior tombstone")
	}
	worker.deleteExprType(canonical)
	worker.exprTypes[ownedMissing] = nil
	observed := map[ast.Expr]Type{}
	worker.rangeExprTypes(func(expr ast.Expr, typ Type) bool {
		if _, duplicate := observed[expr]; duplicate {
			t.Fatal("logical iteration duplicated a key")
		}
		observed[expr] = typ
		return true
	})
	expected := map[ast.Expr]Type{presentNil: nil, typedNil: nilType, foreign: i64, ownedMissing: nil}
	if !reflect.DeepEqual(observed, expected) {
		t.Fatalf("logical iteration differs: got=%v want=%v", observed, expected)
	}
	calls := 0
	worker.rangeExprTypes(func(ast.Expr, Type) bool { calls++; return false })
	if calls != 1 {
		t.Fatal("iteration early stop ignored")
	}
	clone := cloneAnalyzerValueForBodyWorker(worker).Addr().Interface().(*Analyzer)
	clone.deleteExprType(foreign)
	if _, present := clone.lookupExprType(foreign); present {
		t.Fatal("cloned overlay deletion failed")
	}
	if _, present := worker.lookupExprType(foreign); !present {
		t.Fatal("cloned overlay shared mutable tombstones")
	}
	if reflect.ValueOf(clone.exprTypeOverlay.base).Pointer() != reflect.ValueOf(base).Pointer() {
		t.Fatal("worker clone copied read-only whole-program base")
	}
}

func TestAnalyzerExprTypeOverlayConstructionDoesNotScaleWithCanonicalRows(t *testing.T) {
	makeSource := func(count int) *Analyzer {
		source := &Analyzer{exprTypes: map[ast.Expr]Type{}}
		typ := &BuiltinType{Name: "i64"}
		for i := 0; i < count; i++ {
			source.exprTypes[&ast.IntLit{Value: fmt.Sprint(i)}] = typ
		}
		return source
	}
	small, large := makeSource(0), makeSource(8192)
	measure := func(source *Analyzer) float64 {
		return testing.AllocsPerRun(3, func() {
			specializationAnalysisCloneSink = cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
		})
	}
	smallAlloc, largeAlloc := measure(small), measure(large)
	if largeAlloc > smallAlloc+2 {
		t.Fatalf("whole expression map still traversed/cloned: small=%v large=%v", smallAlloc, largeAlloc)
	}
	cloned := specializationAnalysisCloneSink
	if len(cloned.exprTypes) != 0 || reflect.ValueOf(cloned.exprTypeOverlay.base).Pointer() != reflect.ValueOf(large.exprTypes).Pointer() {
		t.Fatal("constructor did not retain only an immutable base reference")
	}
	specializationAnalysisCloneSink = nil
}

func TestAnalyzerExprTypeOverlayRealSpecializationAndSnapshotBoundaries(t *testing.T) {
	const source = `def bounce[T](value: T, depth: i64 = 0) -> T:
    return value

def first[T](value: T) -> T:
    return bounce(value)

def unrelated(value: i64) -> i64:
    return value + 1
`
	root, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(root.Errors()) != 0 {
		t.Fatal(root.Errors())
	}
	first := retainedProofNamedDecl(t, root, "first").(*ast.FuncDecl)
	unrelated := retainedProofNamedDecl(t, root, "unrelated").(*ast.FuncDecl)
	foreignExpr := unrelated.Body[0].(*ast.ReturnStmt).Value
	expectedForeign, present := root.LookupExprTypes(foreignExpr)
	if !present {
		t.Fatal("missing real foreign fact")
	}
	before := make(map[ast.Expr]Type, len(state.a.exprTypes))
	for expr, typ := range state.a.exprTypes {
		before[expr] = typ
	}
	root.BeginSharedCodegen()
	sessionBase := map[ast.Expr]Type{}
	for expr, typ := range state.a.exprTypes {
		sessionBase[expr] = typ
	}
	product, err := root.SpecializeFunction(first, []Type{root.NamedTypes["i64"]})
	if err != nil {
		root.EndSharedCodegen()
		t.Fatal(err)
	}
	call := product.Decl.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if len(call.ResolvedArgs) != 2 || !SameType(product.Result.ValueExprTypes(call), root.NamedTypes["i64"]) {
		t.Fatal("nested/default specialized types changed")
	}
	if actual, ok := product.Result.LookupExprTypes(foreignExpr); !ok || actual != expectedForeign {
		t.Fatal("foreign canonical type fallback changed")
	}
	if product.Result.analyzer != nil || product.Result.specializationQueries.types.exprTypeOverlay != nil {
		t.Fatal("published product retained construction overlay/base")
	}
	nested, err := product.Result.SpecializeFunction(product.Decl, []Type{root.NamedTypes["i64"]})
	if err != nil || nested != product {
		t.Fatal("nested specialization root/memo changed", err)
	}
	if !reflect.DeepEqual(sessionBase, state.a.exprTypes) {
		root.EndSharedCodegen()
		t.Fatal("construction mutated its shared-session base")
	}
	root.EndSharedCodegen()
	if !reflect.DeepEqual(before, state.a.exprTypes) || state.a.exprTypeOverlay != nil {
		t.Fatal("specialization modified canonical types/installed a root overlay")
	}
	encoded, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(encoded)
	if err != nil || restored.a.exprTypeOverlay != nil {
		t.Fatal("canonical snapshot overlay lifecycle changed", err)
	}
	worker := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	state.a.exprTypeOverlay = worker.exprTypeOverlay
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("active construction resource persisted")
	}
	state.a.exprTypeOverlay = nil
	if err := validateSparseFactValueType(reflect.ValueOf(worker.exprTypeOverlay)); err == nil {
		t.Fatal("construction resource admitted in public fact graph")
	}
	publication := worker.finishProgramResult(root.File, root.LoweredFile, nil, AnalyzeOptions{}, nil)
	if len(publication.Errors()) == 0 {
		t.Fatal("partial construction overlay published as a whole canonical program")
	}
}

func TestAnalyzerExprTypeOverlayRejectsCanonicalMerge(t *testing.T) {
	source := &Analyzer{exprTypes: map[ast.Expr]Type{}}
	worker := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	mergeValue(reflect.ValueOf(source).Elem(), reflect.ValueOf(worker).Elem(), reflect.ValueOf(source).Elem(), nil)
	if len(source.diagnostics) == 0 || source.exprTypeOverlay != nil {
		t.Fatal("construction overlay silently merged into canonical state")
	}
	before := len(worker.diagnostics)
	worker.analyzeDeclsParallel(nil, 2)
	if len(worker.diagnostics) <= before {
		t.Fatal("parallel construction scheduling not explicitly declined")
	}
}

func TestAnalyzerExprTypeReadsUseClosedOverlayAPI(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing source location")
	}
	directory := filepath.Dir(filename)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	boundary := map[string]map[string]bool{
		"analyzer_expr_type_overlay.go": {"lookupExprType": true, "deleteExprType": true, "rangeExprTypes": true},
		"parallel_bodies.go":            {"cloneAnalyzerValueForParallelPolicy": true},
		"analyzer.go":                   {"finishProgramResult": true},
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
		stack := []goast.Node{}
		goast.Inspect(file, func(node goast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			if len(stack) > 0 {
				parents[node] = stack[len(stack)-1]
			}
			stack = append(stack, node)
			return true
		})
		goast.Inspect(file, func(node goast.Node) bool {
			selector, ok := node.(*goast.SelectorExpr)
			if !ok || selector.Sel.Name != "exprTypes" {
				return true
			}
			function := ""
			for current := goast.Node(selector); current != nil; current = parents[current] {
				if fn, ok := current.(*goast.FuncDecl); ok {
					function = fn.Name.Name
					break
				}
			}
			if boundary[entry.Name()][function] {
				return true
			}
			if index, ok := parents[selector].(*goast.IndexExpr); ok && index.X == selector {
				if assignment, ok := parents[index].(*goast.AssignStmt); ok {
					for _, left := range assignment.Lhs {
						if left == index {
							return true
						}
					}
				}
			}
			t.Errorf("raw Analyzer.exprTypes read/iteration/alias bypasses overlay API at %s (%s)", set.Position(selector.Pos()), function)
			return true
		})
	}
}
