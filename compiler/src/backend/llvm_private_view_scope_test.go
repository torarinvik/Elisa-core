//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestPrivateProofSpanDoesNotAuthorizeCanonicalForeignIndex(t *testing.T) {
	root := parseAndAnalyzeBackendTest(t, "private-proof-context.elisa", `def guarded[T](xs: darray[T]&, i: usize, fallback: T) -> T:
    trusted Unsafe.UncheckedIndex:
        if i < xs.count:
            return xs[i]
        return fallback

def foreign(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        return xs[i]
`)
	var generic *ast.FuncDecl
	var foreign *ast.IndexExpr
	for _, decl := range root.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name == "guarded" {
			generic = fn
		}
		if fn.Name == "foreign" {
			foreign = fn.Body[0].(*ast.CanStmt).Body[0].(*ast.ReturnStmt).Value.(*ast.IndexExpr)
		}
	}
	if generic == nil || foreign == nil {
		t.Fatal("missing source contexts")
	}
	product, err := root.SpecializeFunction(generic, []semantic.Type{root.NamedTypes["i32"]})
	if err != nil {
		t.Fatal(err)
	}
	var current *ast.IndexExpr
	product.Result.RangeIndexProofSpanAuthority(func(index *ast.IndexExpr, proven bool) bool {
		if proven {
			current = index
		}
		return true
	})
	if current == nil {
		t.Fatal("guarded concrete function has no index proof")
	}
	// Force the exact collision seen with source-preserving default/lowering
	// clones. This controls the lookup boundary, not an accepted source edit.
	current.Position = foreign.Position
	recorder := &irInputReadRecorder{}
	g := &llvmGenerator{result: product.Result, sharedLookups: newSharedCodegenLookups(product.Result), sharedResult: true, inputReads: recorder}
	state := &functionState{g: g}
	if state.indexBoundsProven(foreign) {
		t.Fatal("private current proof authorized a typed canonical foreign access")
	}
	var ownerObserved, denialObserved bool
	for _, read := range recorder.snapshot().Codegen {
		if read.Table != "IndexProofSpan" || read.Key != foreign {
			continue
		}
		if read.Operation == "owner" && read.Result == product.Result && read.Value == root {
			ownerObserved = true
		}
		if read.Operation == "decision" && read.Result == product.Result && read.Value == false {
			denialObserved = true
		}
	}
	if !ownerObserved || !denialObserved {
		t.Fatalf("missing foreign proof ownership or denial: owner=%v decision=%v", ownerObserved, denialObserved)
	}
	if !state.indexBoundsProven(&ast.IndexExpr{Position: current.Position}) {
		t.Fatal("current view lost its valid rebuilt-node proof")
	}
}

func TestCodegenResultContextRestoresProgramAndPrivateOverlays(t *testing.T) {
	root := parseAndAnalyzeBackendTest(t, "private-body-context.elisa", `def generic[T](value: T) -> T:
    return value

def plain(value: i64) -> i64:
    return value + 1
`)
	generic := root.File.Decls[0].(*ast.FuncDecl)
	plain := root.File.Decls[1].(*ast.FuncDecl)
	product, err := root.SpecializeFunction(generic, []semantic.Type{root.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	program := newSharedCodegenLookups(root)
	g := &llvmGenerator{result: root, sharedLookups: program, symbolsByNode: program.symbolsByNode}
	restorePrivate := g.installCodegenResult(product.Result)
	if g.sharedLookups.programLookups() != program {
		t.Fatal("private body copied the canonical program index")
	}
	if product.Result.CodegenDeclarationResult(plain) != root {
		t.Fatal("canonical imported body inherited private semantic context")
	}
	synthetic := &ast.FuncDecl{Name: "synthetic", Body: product.Decl.Body}
	if product.Result.CodegenDeclarationResult(synthetic) != product.Result {
		t.Fatal("synthetic closure lost its private semantic context")
	}
	node := &ast.IntLit{Value: "7"}
	g.exprOverlay = map[ast.Expr]semantic.Type{node: root.NamedTypes["i64"]}
	restoreRoot := g.installCodegenResult(root)
	if g.result != root || g.exprOverlay != nil {
		t.Fatal("canonical nested body borrowed private synthesized facts")
	}
	restoreRoot()
	if g.result != product.Result || g.exprOverlay[node] != root.NamedTypes["i64"] {
		t.Fatal("private parent view/overlay was not restored")
	}
	restorePrivate()
	if g.result != root || g.sharedLookups != program {
		t.Fatal("canonical program context was not restored")
	}
}
