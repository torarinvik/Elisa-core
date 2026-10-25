//go:build cgo

package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestPlainCoordinatesUseExactSourceFields(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := "def first(value: i32 = 1) -> i32:\n    return value\n"
	after := "\n\ndef first(value: i32 = 1) -> i32:\n    return value\n"
	result, state := plainDefaultProductFixture(t, before)
	live := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	fresh := retainedParse(t, after).Decls[0].(*ast.FuncDecl)
	if fresh == live {
		t.Fatal("fixture did not use distinct parsed roots")
	}
	if err := plainDefaultProductShape(state.a, fresh); err == nil {
		t.Fatal("canonical capture accepted foreign parsed ownership")
	}
	old := live.Params[0].Position
	plan, err := state.planPlainSourceCoordinates(map[*ast.FuncDecl]*ast.FuncDecl{live: fresh}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if live.Params[0].Position != old {
		t.Fatal("preflight mutated source")
	}
	plan.apply()
	analysis := state.a.functionAnalyses[live]
	freshCFG := ConstructCFG(fresh)
	populateBasicFlowInstrs(freshCFG)
	for i := range freshCFG.Blocks {
		if !reflect.DeepEqual(analysis.CFG.Blocks[i].Instrs, freshCFG.Blocks[i].Instrs) || analysis.CFG.Blocks[i].Terminator != freshCFG.Blocks[i].Terminator {
			t.Fatal("actual CFG producer positions differ")
		}
	}
	if !reflect.DeepEqual(analysis.FactExitSummary, buildFunctionFactExitSummary(analysis.FactTransforms)) {
		t.Fatal("rendered fact summary differs from relocated structured facts")
	}
	if !reflect.DeepEqual(live, fresh) {
		t.Fatal("complete syntax positions differ after explicit source pairing")
	}
	signature := state.a.funcDeclSymbols[live].Type.(*FuncType).SpecSignature
	if signature.Position != fresh.Params[0].Position || signature.Params[0].SourcePos != fresh.Params[0].Position || signature.Result.SourcePos != fresh.ReturnType.Pos() {
		t.Fatal("typed producer positions differ")
	}
}

func TestPlainCoordinatesRejectUnattributedBinderPosition(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := "def first(value: i32 = 1) -> i32:\n    return value\n"
	result, state := plainDefaultProductFixture(t, before)
	live := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	fresh := retainedParse(t, "\n"+before).Decls[0].(*ast.FuncDecl)
	signature := state.a.funcDeclSymbols[live].Type.(*FuncType).SpecSignature
	signature.Params[0].SourcePos.Offset++
	old := live.Position
	if _, err := state.planPlainSourceCoordinates(map[*ast.FuncDecl]*ast.FuncDecl{live: fresh}, nil); err == nil {
		t.Fatal("accepted binder position without exact source producer")
	}
	if live.Position != old {
		t.Fatal("failed preflight mutated source")
	}
}

func TestPlainCoordinatesRejectUnattributedFlowPosition(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := "def first(value: i32 = 1) -> i32:\n    return value\n"
	result, state := plainDefaultProductFixture(t, before)
	live := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	fresh := retainedParse(t, "\n"+before).Decls[0].(*ast.FuncDecl)
	state.a.functionAnalyses[live].CFG.Blocks[0].Instrs[0].Position.Offset++
	old := live.Position
	if _, err := state.planPlainSourceCoordinates(map[*ast.FuncDecl]*ast.FuncDecl{live: fresh}, nil); err == nil {
		t.Fatal("accepted flow position without exact instruction producer")
	}
	if live.Position != old {
		t.Fatal("failed flow preflight mutated source")
	}
}

func TestPlainCoordinatesRejectMalformedTransformProducers(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	for _, storage := range []string{"function", "block-summary", "cfg-block"} {
		for _, mutation := range []string{"zero-position", "missing", "duplicate"} {
			t.Run(storage+"/"+mutation, func(t *testing.T) {
				before := "def first(value: i32 = 1) -> i32:\n    return value\n"
				result, state := plainDefaultProductFixture(t, before)
				live := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
				fresh := retainedParse(t, "\n"+before).Decls[0].(*ast.FuncDecl)
				analysis := state.a.functionAnalyses[live]
				var values *[]FactTransform
				switch storage {
				case "function":
					values = &analysis.FactTransforms
				case "block-summary":
					values = &analysis.BlockFactTransforms[0].Transforms
				case "cfg-block":
					values = &analysis.CFG.Blocks[0].FactTransforms
				}
				index := -1
				for i, fact := range *values {
					if fact.SourceKind == FactSourceReturn {
						index = i
						break
					}
				}
				if index < 0 {
					t.Fatal("fixture has no actual return transform")
				}
				// Detach this storage to ensure each control independently reaches it.
				*values = append([]FactTransform(nil), (*values)...)
				switch mutation {
				case "zero-position":
					(*values)[index].SourcePos = lexer.Pos{}
				case "missing":
					*values = append((*values)[:index], (*values)[index+1:]...)
				case "duplicate":
					*values = append(*values, (*values)[index])
				}
				old := live.Position
				if _, err := state.planPlainSourceCoordinates(map[*ast.FuncDecl]*ast.FuncDecl{live: fresh}, nil); err == nil {
					t.Fatal("accepted malformed complete producer sequence")
				}
				if live.Position != old {
					t.Fatal("failed producer preflight mutated source")
				}
			})
		}
	}
}
