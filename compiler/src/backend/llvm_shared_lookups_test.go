//go:build cgo

package backend

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

func TestSharedCodegenLookupsKeepSynthesizedProofsPrivate(t *testing.T) {
	pos := lexer.Pos{File: "proof.elisa", Line: 1, Col: 1, Offset: 0, EndLine: 1, EndCol: 4, EndOffset: 3}
	proven := &ast.IndexExpr{Position: pos}
	node := &ast.FuncDecl{Name: "original"}
	first := &semantic.Symbol{Name: "Alpha.original", Node: node}
	last := &semantic.Symbol{Name: "Zulu.alias", Node: node}
	result := &semantic.Result{
		GlobalScope:       &semantic.Scope{Symbols: map[string]*semantic.Symbol{"Alpha.original": first, "Zulu.alias": last}},
		NamedTypes:        map[string]semantic.Type{"Alpha.Number": &semantic.BuiltinType{Name: "i64"}, "Beta.Number": &semantic.BuiltinType{Name: "u64"}, "Alpha.Unique": &semantic.BuiltinType{Name: "bool"}},
		IndexBoundsProven: map[*ast.IndexExpr]bool{proven: true},
	}
	shared := newSharedCodegenLookups(result)
	if shared.symbolsByNode[node] != last {
		t.Fatal("shared-node symbol tie-break changed")
	}
	errors := make(chan error, 8)
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			g := &llvmGenerator{result: result, sharedLookups: shared, sharedResult: true}
			state := &functionState{g: g}
			if !state.indexBoundsProven(&ast.IndexExpr{Position: pos}) {
				errors <- fmt.Errorf("worker %d lost rebuilt-node proof", worker)
				return
			}
			adjacent := pos
			adjacent.EndOffset++
			if state.indexBoundsProven(&ast.IndexExpr{Position: adjacent}) {
				errors <- fmt.Errorf("worker %d accepted a different span", worker)
				return
			}
			own := lexer.Pos{File: "synthetic.elisa", Line: worker + 1, Offset: worker + 100, EndOffset: worker + 101}
			g.setResultIndexBoundsProven(&ast.IndexExpr{Position: own}, true)
			if !state.indexBoundsProven(&ast.IndexExpr{Position: own}) || shared.indexProofSpans()[own] {
				errors <- fmt.Errorf("worker %d leaked or lost a synthesized proof", worker)
				return
			}
			other := &functionState{g: &llvmGenerator{result: result, sharedLookups: shared, sharedResult: true}}
			if other.indexBoundsProven(&ast.IndexExpr{Position: own}) {
				errors <- fmt.Errorf("worker %d proof leaked to another generator", worker)
				return
			}
			if len(g.namedTypeSuffixIndex()["Number"]) != 2 || !reflect.DeepEqual(g.namedTypeSuffixIndex()["Unique"], []string{"Alpha.Unique"}) || !reflect.DeepEqual(g.globalSuffixIndex()["alias"], []string{"Zulu.alias"}) {
				errors <- fmt.Errorf("worker %d suffix lookup changed", worker)
			}
		}(worker)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if len(result.IndexBoundsProven) != 1 {
		t.Fatal("codegen changed the semantic proof map")
	}
}

func TestExternCPartsAvoidsUnsplitPlanAllocations(t *testing.T) {
	scalar := &semantic.BuiltinType{Name: "i64"}
	view := &semantic.ViewType{Elem: scalar}
	for _, fn := range []*semantic.FuncType{
		{Params: []semantic.Type{scalar, view}},
		{Params: []semantic.Type{scalar, scalar}, IsNativeExtern: true, CallConv: "c"},
	} {
		if allocations := testing.AllocsPerRun(1000, func() {
			if externCParts(fn) != nil {
				panic("unsplit function acquired a C parameter plan")
			}
		}); allocations != 0 {
			t.Fatalf("unsplit plan allocated %g objects", allocations)
		}
	}
	fn := &semantic.FuncType{Params: []semantic.Type{scalar, view}, IsNativeExtern: true, CallConv: "c"}
	want := []semantic.CParamPart{{Param: 0, Part: semantic.CParamWhole}, {Param: 1, Part: semantic.CParamViewPtr}, {Param: 1, Part: semantic.CParamViewLen}}
	if got := externCParts(fn); !reflect.DeepEqual(got, want) {
		t.Fatalf("split view parameter order changed: %v", got)
	}
	fn.IsNativeExtern = false
	fn.CParamPlan = want
	if got := externCParts(fn); !reflect.DeepEqual(got, want) {
		t.Fatal("explicit bounds plan was ignored")
	}
}
