package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
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
		{Kind: FlowInstrProduce, Location: "frozen", Source: "store", Note: "freeze produces frozen store"},
		{Kind: FlowInstrRebase, Location: "store", Source: "freeze", Note: "freeze rebases store provenance"},
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

func TestPopulateBasicFlowInstrsRecordsErrorPathFacts(t *testing.T) {
	cfg := &CFG{Blocks: []CFGBlock{{
		Nodes: []ast.Node{
			&ast.ExprStmt{Expr: &ast.RaiseExpr{Error: &ast.FieldExpr{Object: &ast.Ident{Name: "FileError"}, Field: "NotFound"}}},
			&ast.VarDeclStmt{Name: "value", Value: &ast.TryExpr{Value: &ast.CallExpr{Func: &ast.Ident{Name: "checked"}}}},
			&ast.VarDeclStmt{Name: "ptr", Value: &ast.UnwrapElseExpr{Value: &ast.Ident{Name: "maybe"}, Fallback: &ast.RaiseExpr{Error: &ast.FieldExpr{Object: &ast.Ident{Name: "FileError"}, Field: "NotFound"}}}},
		},
	}}}

	populateBasicFlowInstrs(cfg)

	var sawRaise bool
	var sawTry bool
	var sawElse bool
	for _, instr := range cfg.Blocks[0].Instrs {
		if instr.Kind != FlowInstrErrorExit {
			continue
		}
		switch instr.Note {
		case "raise error path":
			sawRaise = true
		case "try propagates error path":
			sawTry = true
		case "else fallback handles nullable path":
			sawElse = true
		}
	}
	if !sawRaise || !sawTry || !sawElse {
		t.Fatalf("expected raise/try/else error-path instructions, got %#v", cfg.Blocks[0].Instrs)
	}
}
