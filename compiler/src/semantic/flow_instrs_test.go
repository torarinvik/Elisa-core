package semantic

import (
	"reflect"
	"testing"

	"llcontext/src/ast"
)

func TestPopulateBasicFlowInstrsRecordsStatementLevelFacts(t *testing.T) {
	cfg := &CFG{Blocks: []CFGBlock{{
		Nodes: []ast.Node{
			&ast.VarDeclStmt{Name: "alias", Value: &ast.Ident{Name: "first"}},
			&ast.AssignStmt{
				Target: &ast.FieldExpr{Object: &ast.Ident{Name: "player"}, Field: "health"},
				Value:  &ast.IntLit{Value: "1"},
			},
			&ast.RestoreStmt{RegionName: "scratch", MarkName: "cp"},
			&ast.ResetStmt{Name: "scratch"},
			&ast.DestroyStmt{Name: "scratch"},
			&ast.ReturnStmt{Value: &ast.CallExpr{
				Func: &ast.Ident{Name: "join"},
				Args: []ast.Expr{&ast.MoveExpr{Operand: &ast.Ident{Name: "thread"}}},
			}},
		},
	}}}

	populateBasicFlowInstrs(cfg)

	want := []FlowInstr{
		{Kind: FlowInstrAlias, Location: "alias", Source: "first", Note: "var init alias"},
		{Kind: FlowInstrMutate, Location: "player.health", Note: "assign"},
		{Kind: FlowInstrMutate, Location: "player", Note: "assign"},
		{Kind: FlowInstrInvalidate, Location: "scratch", Source: "cp", Note: "restore region checkpoint"},
		{Kind: FlowInstrInvalidate, Location: "scratch", Note: "reset region"},
		{Kind: FlowInstrInvalidate, Location: "scratch", Note: "destroy region"},
		{Kind: FlowInstrConsume, Location: "thread", Note: "explicit move"},
		{Kind: FlowInstrReturn, Note: "return"},
	}
	if !reflect.DeepEqual(cfg.Blocks[0].Instrs, want) {
		t.Fatalf("unexpected flow instructions:\nwant %#v\n got %#v", want, cfg.Blocks[0].Instrs)
	}
}

func TestPopulateBasicFlowInstrsRecordsProduceAndRebaseFacts(t *testing.T) {
	cfg := &CFG{Blocks: []CFGBlock{{
		Nodes: []ast.Node{
			&ast.VarDeclStmt{Name: "region_node", Value: &ast.AllocExpr{Owner: &ast.Ident{Name: "scratch"}}},
			&ast.VarDeclStmt{Name: "tree_node", Value: &ast.AllocExpr{Owner: &ast.Ident{Name: "store"}, NodeSugar: true}},
			&ast.VarDeclStmt{Name: "frozen", Value: &ast.CallExpr{
				Func: &ast.Ident{Name: "freeze"},
				Args: []ast.Expr{&ast.MoveExpr{Operand: &ast.Ident{Name: "store"}}},
			}},
		},
	}}}

	populateBasicFlowInstrs(cfg)

	want := []FlowInstr{
		{Kind: FlowInstrProduce, Location: "region_node", Source: "scratch", Note: "allocation produces value"},
		{Kind: FlowInstrProduce, Location: "tree_node", Source: "store", Note: "node construction"},
		{Kind: FlowInstrProduce, Location: "frozen", Note: "freeze produces frozen store"},
		{Kind: FlowInstrRebase, Location: "store", Note: "freeze rebases store provenance"},
		{Kind: FlowInstrConsume, Location: "store", Note: "explicit move"},
	}
	if !reflect.DeepEqual(cfg.Blocks[0].Instrs, want) {
		t.Fatalf("unexpected flow instructions:\nwant %#v\n got %#v", want, cfg.Blocks[0].Instrs)
	}
}

func TestAppendFlowInstrUniqueDedupesExactInstruction(t *testing.T) {
	block := &CFGBlock{}
	instr := FlowInstr{Kind: FlowInstrConsume, Location: "thread", Note: "explicit move"}
	appendFlowInstrUnique(block, instr)
	appendFlowInstrUnique(block, instr)
	appendFlowInstrUnique(block, FlowInstr{Kind: FlowInstrConsume, Location: "thread", Note: "different reason"})

	want := []FlowInstr{
		{Kind: FlowInstrConsume, Location: "thread", Note: "explicit move"},
		{Kind: FlowInstrConsume, Location: "thread", Note: "different reason"},
	}
	if !reflect.DeepEqual(block.Instrs, want) {
		t.Fatalf("unexpected deduped instructions:\nwant %#v\n got %#v", want, block.Instrs)
	}
}
