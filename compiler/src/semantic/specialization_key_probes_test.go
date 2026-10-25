//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestSpecializationCopyDeclClosesFactChainsInAnyTableOrder(t *testing.T) {
	root := analyzeTreeTestSource(t, "specialization-key-probes.elisa", `def first[T](value: i64) -> i64:
    return value + 1

def second[T](value: i64) -> i64:
    return value + 2
`)
	first := root.File.Decls[0].(*ast.FuncDecl)
	second := root.File.Decls[1].(*ast.FuncDecl)
	firstBinary := first.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	secondBinary := second.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	firstCall := &ast.CallExpr{Func: &ast.Ident{Name: "first_fact"}}
	lastIdent := &ast.Ident{Name: "last_fact"}
	lastCall := &ast.CallExpr{Func: lastIdent}
	// lawIsCalls occurs after safeCalls and exprTypes in Analyzer. Reaching a
	// synthetic call through it must restart typed probes at every table. The
	// second safe-call edge cycles back, proving fixed-point termination too.
	root.analyzer.lawIsCalls = map[*ast.BinaryExpr]*ast.CallExpr{firstBinary: firstCall, secondBinary: firstCall}
	root.analyzer.safeCalls[firstCall] = &SafeCallInfo{TransformFunc: lastCall}
	root.analyzer.safeCalls[lastCall] = &SafeCallInfo{TransformFunc: firstCall}
	root.analyzer.exprTypes[lastCall] = root.NamedTypes["i64"]
	root.analyzer.resolvedValueNames[lastIdent] = "Scoped.last_fact"
	worker := cloneAnalyzerValueForBodyWorker(root.analyzer).Addr().Interface().(*Analyzer)
	isolateWorkerScopes(root.analyzer, worker)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	private := context.copyDecl(first)
	if context.err != nil {
		t.Fatal(context.err)
	}
	privateBinary := private.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	privateFirst := worker.lawIsCalls[privateBinary]
	if privateFirst == nil || privateFirst == firstCall {
		t.Fatal("primary fact syntax not isolated")
	}
	privateLast, ok := worker.safeCalls[privateFirst].TransformFunc.(*ast.CallExpr)
	if !ok || privateLast == lastCall {
		t.Fatal("earlier-table synthetic chain not isolated")
	}
	if worker.safeCalls[privateLast].TransformFunc != privateFirst {
		t.Fatal("synthetic fact cycle identity lost")
	}
	if worker.exprTypes[privateLast] != root.NamedTypes["i64"] {
		t.Fatal("fact-chain terminal type not rekeyed")
	}
	privateIdent := privateLast.Func.(*ast.Ident)
	if worker.resolvedValueNames[privateIdent] != "Scoped.last_fact" {
		t.Fatal("fact-chain terminal name not rekeyed")
	}
	// A later declaration may reference the same original fact-held syntax; it
	// must reuse the existing private identity without recopying canonical facts
	// over concrete observations already made in this construction context.
	worker.exprTypes[privateLast] = root.NamedTypes["bool"]
	privateSecond := context.copyDecl(second)
	if context.err != nil {
		t.Fatal(context.err)
	}
	binary := privateSecond.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	if worker.lawIsCalls[binary] != privateFirst {
		t.Fatal("existing private fact identity not reused")
	}
	if worker.exprTypes[privateLast] != root.NamedTypes["bool"] {
		t.Fatal("prior private observation overwritten")
	}
	if root.analyzer.safeCalls[firstCall].TransformFunc != lastCall || root.analyzer.exprTypes[lastCall] != root.NamedTypes["i64"] {
		t.Fatal("canonical fact graph mutated")
	}
}
