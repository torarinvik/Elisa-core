package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
	"testing"
)

func TestRetainedIndexProofClearedBeforeSameNodeReanalysis(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `def read(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        if i < xs.count:
            return xs[i]
        return 0
`
	result, retained := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	fn := result.File.Decls[0].(*ast.FuncDecl)
	guard := fn.Body[0].(*ast.CanStmt).Body[0].(*ast.IfStmt)
	index := guard.Then[0].(*ast.ReturnStmt).Value.(*ast.IndexExpr)
	if !result.IndexBoundsProven[index] || retained.a.indexBoundsProofOwners[index] != fn {
		t.Fatal("initial proof ownership not recorded")
	}
	// Model a caller rechecked in place after a dependency's facts change:
	// the IndexExpr is deliberately the exact same pointer, not a body splice.
	guard.Cond.(*ast.BinaryExpr).Op = lexer.TOKEN_GT
	retained.indexProofsUpdating = true
	retained.indexProofsByFunc = map[*ast.FuncDecl]map[*ast.IndexExpr]bool{}
	for expr, owner := range retained.a.indexBoundsProofOwners {
		retained.rememberFunctionIndexProof(owner, expr)
	}
	defer func() { retained.indexProofsUpdating = false; retained.indexProofsByFunc = nil }()
	retained.a.analyzeFunc(fn)
	if result.IndexBoundsProven[index] {
		t.Fatal("same-pointer reanalysis reused its old bounds proof")
	}
	// Rechecking once more with the original condition must establish a fresh
	// proof, not permanently suppress a valid optimization.
	guard.Cond.(*ast.BinaryExpr).Op = lexer.TOKEN_LT
	retained.a.analyzeFunc(fn)
	if !result.IndexBoundsProven[index] {
		t.Fatal("same-pointer valid proof was not re-established")
	}
}

func TestRetainedIndexProofOwnershipIncludesDetachedNodes(t *testing.T) {
	fn, unaffected := &ast.FuncDecl{Name: "folded"}, &ast.FuncDecl{Name: "unaffected"}
	detached, kept := &ast.IndexExpr{}, &ast.IndexExpr{}
	retained := &RetainedAnalysis{indexProofsUpdating: true, indexProofsByFunc: map[*ast.FuncDecl]map[*ast.IndexExpr]bool{}}
	a := &Analyzer{retained: retained, currentFuncDecl: fn}
	retained.a = a
	a.recordIndexBoundsProof(detached)
	a.currentFuncDecl = unaffected
	a.recordIndexBoundsProof(kept)
	// Neither index belongs to either body's current AST. Ownership must survive
	// folding/static reconstruction; walking only the replacement body is wrong.
	a.invalidateRetainedFunctionIndexBoundsProofs(fn)
	if a.indexBoundsProven[detached] || a.indexBoundsProofOwners[detached] != nil {
		t.Fatal("detached function proof/ownership survived invalidation")
	}
	if !a.indexBoundsProven[kept] || a.indexBoundsProofOwners[kept] != unaffected {
		t.Fatal("unaffected proof/ownership was removed")
	}
}

func TestRetainedIndexProofKeepsGlobalAndUnchangedHeaderOwnership(t *testing.T) {
	fn := &ast.FuncDecl{Name: "read"}
	body, header, global := &ast.IndexExpr{}, &ast.IndexExpr{}, &ast.IndexExpr{}
	retained := &RetainedAnalysis{indexProofsUpdating: true, indexProofsByFunc: map[*ast.FuncDecl]map[*ast.IndexExpr]bool{}}
	a := &Analyzer{retained: retained}
	retained.a = a
	a.recordIndexBoundsProof(global)
	if owner, present := a.indexBoundsProofOwners[global]; !present || owner != nil || a.indexBoundsProofHeaders[global] {
		t.Fatal("global proof was not explicitly distinguished from missing ownership")
	}
	a.withIndexProofHeaderOwner(fn, func() *FuncType { a.recordIndexBoundsProof(header); return nil })
	a.currentFuncDecl = fn
	a.recordIndexBoundsProof(body)
	a.invalidateRetainedFunctionIndexBoundsProofs(fn)
	if a.indexBoundsProven[body] {
		t.Fatal("old body proof survived recheck")
	}
	if !a.indexBoundsProven[header] || !a.indexBoundsProven[global] {
		t.Fatal("body recheck discarded valid untouched header/global proofs")
	}
	a.clearFunctionIndexBoundsProofs(fn, true)
	if a.indexBoundsProven[header] || !a.indexBoundsProven[global] {
		t.Fatal("header replacement failed to retract only its declaration's proof")
	}
}

func TestRetainedIndexProofMissingOwnershipRejectsUpdate(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const before = "def value() -> i64:\n    return 1\n"
	const after = "def value() -> i64:\n    return 2\n"
	result, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	// Model an incompatible older state or unauthorized backend mutation. A
	// missing marker must not be mistaken for a legitimate global initializer.
	unknown := &ast.IndexExpr{}
	result.IndexBoundsProven[unknown] = true
	if _, err := retained.Update(retainedParse(t, after), retainedParse(t, after), []byte(after)); err == nil {
		t.Fatal("unowned proof silently bypassed required invalidation")
	}
	if retained.indexProofsUpdating || retained.indexProofsByFunc != nil {
		t.Fatal("rejected admission retained invocation-local proof index")
	}
}

func TestRetainedIndexProofParallelOwnersAreCanonical(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "4")
	template := `def NAME(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        if i < xs.count:
            return xs[i]
        return 0

`
	source := ""
	for _, name := range []string{"first", "second", "third", "fourth"} {
		source += strings.Replace(template, "NAME", name, 1)
	}
	result, retained := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	for _, declaration := range result.File.Decls {
		fn := declaration.(*ast.FuncDecl)
		index := fn.Body[0].(*ast.CanStmt).Body[0].(*ast.IfStmt).Then[0].(*ast.ReturnStmt).Value.(*ast.IndexExpr)
		if !result.IndexBoundsProven[index] {
			t.Fatalf("parallel %s proof missing", fn.Name)
		}
		if retained.a.indexBoundsProofOwners[index] != fn {
			t.Fatalf("parallel %s proof owner is not the current canonical function", fn.Name)
		}
	}
	data, err := retained.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(source, "if i < xs.count:", "if i > xs.count:", 1)
	updated, err := restored.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range updated.File.Decls {
		fn := declaration.(*ast.FuncDecl)
		index := fn.Body[0].(*ast.CanStmt).Body[0].(*ast.IfStmt).Then[0].(*ast.ReturnStmt).Value.(*ast.IndexExpr)
		want := fn.Name != "first"
		if updated.IndexBoundsProven[index] != want {
			t.Fatalf("parallel restored %s proof=%v want=%v", fn.Name, updated.IndexBoundsProven[index], want)
		}
	}
}
