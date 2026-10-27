package semantic

import (
	"sync/atomic"
	"testing"

	"elisacore/src/ast"
)

func TestParallelReturnInferenceAlwaysUsesPrivateSyntax(t *testing.T) {
	file := parseIncrementalSessionFile(t, `def leaf(n: i64) -> i64:
    return n

def target_fn(n: i64) -> i64:
    return leaf(n)
`)
	fn := testFuncDeclByName(t, file, "target_fn")
	snapshot, err := ast.NewSyntaxSnapshot(&ast.File{Decls: []ast.Decl{fn}}, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	result := Analyze(file)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	slots := make([]atomic.Int32, 1)
	slots[0].Store(0) // Even this worker's own previously published body is shared.
	a := result.analyzer
	a.parallel = &parallelWorkerState{worker: 0, at: map[*ast.FuncDecl][2]int{fn: {0, -1}}, slots: slots, snapshots: []*ast.SyntaxSnapshot{snapshot}, frozen: make([]ast.Decl, 1), shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}}
	private, release, ok := a.privateReturnInferenceDecl(fn)
	if !ok || private == nil || private == fn {
		t.Fatal("owned shared body was not isolated")
	}
	defer release()
	originalCall := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	copiedCall := private.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if copiedCall == originalCall {
		t.Fatal("private body retained a shared call expression")
	}
	prior := originalCall.ResolvedImplicitArgsValid
	copiedCall.ResolvedImplicitArgsValid = !prior
	if originalCall.ResolvedImplicitArgsValid != prior {
		t.Fatal("mutating private call changed the published body")
	}
	if a.canonicalFuncDecl(private) != fn {
		t.Fatal("private body lost canonical declaration identity")
	}
	nested, releaseNested, ok := a.privateReturnInferenceDecl(private)
	if !ok || nested == private || nested == fn {
		t.Fatal("nested inference reused active syntax")
	}
	releaseNested()
}

func TestParallelReturnInferenceDeclinesMissingSnapshot(t *testing.T) {
	file := parseIncrementalSessionFile(t, "def target_fn(n: i64) -> i64:\n    return n\n")
	result := Analyze(file)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	fn := testFuncDeclByName(t, file, "target_fn")
	a := result.analyzer
	a.parallel = &parallelWorkerState{worker: 0, at: map[*ast.FuncDecl][2]int{}, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}}
	a.suppressDiagnostics = true // Failing isolation must not disappear in a probe.
	before := len(a.diagnostics)
	_, release, ok := a.privateReturnInferenceDecl(fn)
	release()
	if ok || len(a.diagnostics) != before+1 || a.diagnostics[len(a.diagnostics)-1].Severity != DiagnosticSeverityError {
		t.Fatal("missing snapshot did not fail closed")
	}
}

func TestParallelScalarReturnInferenceDoesNotRequireSnapshot(t *testing.T) {
	file := parseIncrementalSessionFile(t, "def scalar_fn(n: i64) -> i64:\n    return n\n")
	result := Analyze(file)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	fn := testFuncDeclByName(t, file, "scalar_fn")
	a := result.analyzer
	a.parallel = &parallelWorkerState{worker: 0, at: map[*ast.FuncDecl][2]int{}, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}}
	typ := &FuncType{Return: &BuiltinType{Name: "i64"}}
	before := len(a.diagnostics)
	a.inferFuncReturnProvenance(fn, typ)
	a.inferFuncReturnBorrowedOwnerRefs(fn, typ)
	if !typ.ReturnProvenanceKnown || !typ.ReturnBorrowedOwnerRefsKnown || len(a.diagnostics) != before {
		t.Fatal("scalar summary needlessly required unavailable syntax snapshot")
	}
}
