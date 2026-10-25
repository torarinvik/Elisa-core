package semantic

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"elisacore/src/ast"
)

func foreignPredicateBodyWorker(t *testing.T, decl *ast.FuncDecl) *Analyzer {
	t.Helper()
	snapshot, err := ast.NewSyntaxSnapshot(&ast.File{Decls: []ast.Decl{decl}}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	scope := NewScope(nil)
	symbol := &Symbol{Name: decl.Name, Kind: SymbolFunc, Node: decl, Type: &FuncType{Name: decl.Name, Return: &BuiltinType{Name: "bool"}}}
	scope.Define(symbol)
	p := &parallelWorkerState{worker: 0,
		snapshots: []*ast.SyntaxSnapshot{snapshot}, at: map[*ast.FuncDecl][2]int{decl: {0, -1}},
		slots: make([]atomic.Int32, 1), original: map[*ast.FuncDecl]*ast.FuncDecl{},
	}
	p.slots[0].Store(1)
	return &Analyzer{globalScope: scope, funcDeclSymbols: map[*ast.FuncDecl]*Symbol{decl: symbol}, parallel: p}
}

func TestParallelPredicateScansUseForeignBodySnapshot(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.Ident{Name: "predicate"}}
	decl := &ast.FuncDecl{Name: "predicate", Body: []ast.Stmt{&ast.ReturnStmt{Value: call}}}
	worker := foreignPredicateBodyWorker(t, decl)
	view := worker.readDecl(decl)
	if view == decl {
		t.Fatal("foreign predicate did not get a private body view")
	}
	for _, target := range []*ast.FuncDecl{decl, view} {
		calls := worker.collectSelfRecursiveCallsIn(target)
		if len(calls) != 1 || calls[0] == call || !worker.predicateIsRecursive(target) || worker.predicateStructurallyWellFounded(target) {
			t.Fatal("private body changed canonical self-recursion/totality checks")
		}
		if len(worker.collectSelfRecursiveCalls(target)) != 1 || len(worker.collectSelfRecursiveCallSites(target)) != 1 {
			t.Fatal("numeric self-recursion scans lost the canonical callee")
		}
	}
	start := make(chan struct{})
	var tasks sync.WaitGroup
	tasks.Add(1)
	go func() {
		defer tasks.Done()
		<-start
		for i := 0; i < 1024; i++ {
			// This is the owner-only call-resolution mutation that raced with
			// the foreign structural predicate walk in the integration test.
			call.ResolvedArgsValid = i%2 == 0
			call.ResolvedArgs = []ast.Expr{&ast.BoolLit{Value: i%2 == 0}}
		}
	}()
	close(start)
	for i := 0; i < 256; i++ {
		if !worker.predicateIsRecursive(decl) || worker.predicateStructurallyWellFounded(decl) {
			t.Error("concurrent owner mutation affected private recursion scan")
			break
		}
		if expr, ok := worker.typedPureReturnExpr(decl); !ok || expr == call {
			t.Error("pure-return inspection read the live foreign expression")
			break
		}
		worker.collectSelfRecursiveCalls(decl)
		worker.collectSelfRecursiveCallSites(decl)
	}
	tasks.Wait()
}

func TestParallelPredicateBodyViewPreservesEquationsAndTypedImport(t *testing.T) {
	literal := &ast.BoolLit{Value: true}
	decl := &ast.FuncDecl{Name: "predicate", Body: []ast.Stmt{&ast.ReturnStmt{Value: literal}}}
	worker := foreignPredicateBodyWorker(t, decl)
	if !worker.boolPredicateEligible(decl) {
		t.Fatal("pure foreign predicate lost equation eligibility")
	}
	translator := worker.newSMTTranslator(nil)
	translator.emitStructuralPredicateEquation(&ast.CallExpr{Func: &ast.Ident{Name: decl.Name}}, decl, "predicate_result", nil, nil, "predicate-call")
	if !strings.Contains(strings.Join(translator.auxDecls, ""), "(assert (= predicate_result true))") {
		t.Fatal("private foreign body lost a previously available defining equation")
	}
	// Once imported, readDecl uses the completed original AST and its imported
	// type maps, rather than remaining stuck on an untyped pristine view.
	worker.parallel.imported = map[*ast.FuncDecl]bool{decl: true}
	worker.exprTypes = map[ast.Expr]Type{literal: &BuiltinType{Name: "bool"}}
	if expr, ok := worker.typedPureReturnExpr(decl); !ok || expr != literal || !IsBoolType(worker.exprTypes[expr]) {
		t.Fatal("completed predicate import failed to restore typed body nodes")
	}
	worker.implicitIntConvFuncs = map[*ast.FuncDecl]bool{decl: true}
	if _, ok := worker.typedPureReturnExpr(decl); ok {
		t.Fatal("body-view selection bypassed the implicit-conversion proof guard")
	}
}
