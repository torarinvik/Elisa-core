package semantic

import (
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
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

func TestConstructCFGRejectsUnmappedStatementKinds(t *testing.T) {
	cfg := ConstructCFG(&ast.FuncDecl{Body: []ast.Stmt{nil}})
	err := VerifyCFG(cfg)
	if err == nil || !strings.Contains(err.Error(), "unsupported statement") {
		t.Fatalf("expected an unmapped statement to fail closed, got %v", err)
	}
}

func TestEveryASTStatementKindHasCFGDisposition(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate CFG test source")
	}
	astDir := filepath.Join(filepath.Dir(testFile), "..", "ast")
	astFiles, err := filepath.Glob(filepath.Join(astDir, "*.go"))
	if err != nil {
		t.Fatalf("list AST source files: %v", err)
	}
	if len(astFiles) == 0 {
		t.Fatalf("no AST source files found under %s", astDir)
	}

	statementKinds := map[string]bool{}
	for _, filename := range astFiles {
		file, err := goparser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatalf("parse AST source %s: %v", filename, err)
		}
		for _, decl := range file.Decls {
			method, ok := decl.(*goast.FuncDecl)
			if !ok || method.Name.Name != "stmtTag" || method.Recv == nil || len(method.Recv.List) != 1 {
				continue
			}
			receiver := method.Recv.List[0].Type
			if pointer, ok := receiver.(*goast.StarExpr); ok {
				receiver = pointer.X
			}
			if name, ok := receiver.(*goast.Ident); ok {
				statementKinds[name.Name] = true
			}
		}
	}
	if len(statementKinds) == 0 {
		t.Fatal("no sealed AST statement kinds found")
	}

	flowFile := filepath.Join(filepath.Dir(testFile), "flow_ir.go")
	file, err := goparser.ParseFile(token.NewFileSet(), flowFile, nil, 0)
	if err != nil {
		t.Fatalf("parse CFG source %s: %v", flowFile, err)
	}
	mappedKinds := map[string]bool{}
	for _, decl := range file.Decls {
		function, ok := decl.(*goast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		switch function.Name.Name {
		case "buildStmtList", "flowStmtTerminator", "flowStmtIsExplicitlyLinear":
			goast.Inspect(function.Body, func(node goast.Node) bool {
				clause, ok := node.(*goast.CaseClause)
				if !ok {
					return true
				}
				for _, expr := range clause.List {
					star, ok := expr.(*goast.StarExpr)
					if !ok {
						continue
					}
					selector, ok := star.X.(*goast.SelectorExpr)
					if !ok || selector.Sel.Name == "" {
						continue
					}
					mappedKinds[selector.Sel.Name] = true
				}
				return true
			})
		}
	}

	for kind := range statementKinds {
		if !mappedKinds[kind] {
			t.Errorf("AST statement %s has no explicit CFG disposition", kind)
		}
	}
}
