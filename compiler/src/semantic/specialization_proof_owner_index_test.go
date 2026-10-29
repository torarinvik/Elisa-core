package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestProofOwnerIndexSessionIsolation(t *testing.T) {
	a, b := &ast.FuncDecl{Name: "a"}, &ast.FuncDecl{Name: "b"}
	x, y, z := &ast.IndexExpr{}, &ast.IndexExpr{}, &ast.IndexExpr{}
	r := &Result{analyzer: &Analyzer{indexBoundsProofOwners: map[*ast.IndexExpr]ast.Node{x: a, y: b}}}
	r.BeginSharedCodegen()
	first := r.indexProofsOwnedBy(a)
	if len(first) != 1 || first[0] != x {
		t.Fatal("incorrect first session", first)
	}
	sh := r.shared
	r.EndSharedCodegen()
	if _, ok := sharedProofOwnerIndexes.Load(sh); ok {
		t.Fatal("index retained after session")
	}
	r.analyzer.indexBoundsProofOwners[z] = a
	delete(r.analyzer.indexBoundsProofOwners, x)
	r.BeginSharedCodegen()
	second := r.indexProofsOwnedBy(a)
	if len(second) != 1 || second[0] != z {
		t.Fatal("stale index in next session", second)
	}
	r.EndSharedCodegen()
	r.analyzer.indexBoundsProofOwners[x] = a
	if got := r.indexProofsOwnedBy(a); len(got) != 2 {
		t.Fatal("serial lookup did not read current facts")
	}
}
