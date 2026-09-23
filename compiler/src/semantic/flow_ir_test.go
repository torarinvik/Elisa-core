package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestConstructCFGModelsNestedBreakAndContinueTargets(t *testing.T) {
	innerBreak := &ast.BreakStmt{}
	deadReturn := &ast.ReturnStmt{}
	innerContinue := &ast.ContinueStmt{}
	innerIf := &ast.IfStmt{
		Cond: &ast.Ident{Name: "take_break"},
		Then: []ast.Stmt{innerBreak, deadReturn},
		Else: []ast.Stmt{innerContinue},
	}
	innerLoop := &ast.ForStmt{Body: []ast.Stmt{innerIf}}
	outerContinue := &ast.ContinueStmt{}
	outerLoop := &ast.WhileStmt{
		Cond: &ast.Ident{Name: "keep_going"},
		Body: []ast.Stmt{innerLoop, outerContinue},
	}

	cfg := ConstructCFG(&ast.FuncDecl{Body: []ast.Stmt{outerLoop}})
	if err := VerifyCFG(cfg); err != nil {
		t.Fatalf("constructed CFG failed verification: %v", err)
	}

	blockFor := func(want ast.Node) *CFGBlock {
		t.Helper()
		for i := range cfg.Blocks {
			for _, node := range cfg.Blocks[i].Nodes {
				if node == want {
					return &cfg.Blocks[i]
				}
			}
		}
		t.Fatalf("node %T was not represented in CFG", want)
		return nil
	}

	innerHeader := blockFor(innerLoop)
	outerCondition := blockFor(outerLoop)
	breakBlock := blockFor(innerBreak)
	continueBlock := blockFor(innerContinue)
	outerContinueBlock := blockFor(outerContinue)
	if innerHeader.Terminator.Kind != CFGTerminatorLoopBranch {
		t.Fatalf("inner loop header terminator = %q, want loop branch", innerHeader.Terminator.Kind)
	}
	if breakBlock.Terminator.Kind != CFGTerminatorBreak || len(breakBlock.Edges) != 1 || breakBlock.Edges[0].To != blockFor(outerContinue).ID {
		t.Fatalf("inner break should jump to inner-loop exit before outer continue; block = %#v", breakBlock)
	}
	if continueBlock.Terminator.Kind != CFGTerminatorContinue || len(continueBlock.Edges) != 1 || continueBlock.Edges[0].To != innerHeader.ID {
		t.Fatalf("inner continue should target inner-loop header; block = %#v", continueBlock)
	}
	if outerContinueBlock.Terminator.Kind != CFGTerminatorContinue || len(outerContinueBlock.Edges) != 1 || outerContinueBlock.Edges[0].To != outerCondition.ID {
		t.Fatalf("outer continue should target outer condition; block = %#v", outerContinueBlock)
	}
	for _, block := range cfg.Blocks {
		for _, node := range block.Nodes {
			if node == deadReturn {
				t.Fatal("statement following break was incorrectly kept reachable")
			}
		}
	}
}

func TestVerifyCFGRejectsMalformedTerminatorSuccessors(t *testing.T) {
	cfg := &CFG{
		Entry:  0,
		Blocks: []CFGBlock{{ID: 0, Terminator: CFGTerminator{Kind: CFGTerminatorBreak}}},
	}
	if err := VerifyCFG(cfg); err == nil {
		t.Fatal("expected verifier to reject break terminator without a target edge")
	}
}
