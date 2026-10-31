package semantic

import (
	"elisacore/src/ast"
	"sync"
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

func TestProofOwnerIndexConcurrentFirstLookup(t *testing.T) {
	fn, other := &ast.FuncDecl{Name: "target"}, &ast.FuncDecl{Name: "other"}
	want := &ast.IndexExpr{}
	r := &Result{analyzer: &Analyzer{indexBoundsProofOwners: map[*ast.IndexExpr]ast.Node{want: fn, {}: other}}}
	r.BeginSharedCodegen()
	defer r.EndSharedCodegen()
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 100; j++ {
				got := r.indexProofsOwnedBy(fn)
				if len(got) != 1 || got[0] != want {
					t.Errorf("unexpected proof-owner index: %v", got)
					return
				}
			}
		}()
	}
	group.Wait()
}

func BenchmarkProofOwnerIndexWarm(b *testing.B) {
	fn := &ast.FuncDecl{Name: "fixture"}
	expr := &ast.IndexExpr{}
	r := &Result{analyzer: &Analyzer{indexBoundsProofOwners: map[*ast.IndexExpr]ast.Node{expr: fn}}}
	r.BeginSharedCodegen()
	defer r.EndSharedCodegen()
	if len(r.indexProofsOwnedBy(fn)) != 1 {
		b.Fatal("missing proof")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(r.indexProofsOwnedBy(fn)) != 1 {
			b.Fatal("missing proof")
		}
	}
}
