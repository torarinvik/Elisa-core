package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestStorageViewRecursiveWriteSummaryKeepsFieldSubset(t *testing.T) {
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "recursive_field_write.elisa", `struct Pair:
    left: mutable darray[u8] = []
    right: mutable darray[u8] = []

def first(pair: mutable Pair&) -> void:
    second(pair)

def second(pair: mutable Pair&) -> void:
    if pair.left.count > 0:
        first(pair)
    pair.left.clear()
`, AnalyzeOptions{})
	a := r.analyzer
	a.currentScope = a.globalScope
	a.storageViewCalleeWriteCache = nil
	for _, name := range []string{"first", "second"} {
		decl := storeScanFindFunc(t, r, name)
		writes := a.storageViewCalleeWritesFor([]*ast.FuncDecl{decl}, 0)
		if writes.All || !writes.Fields["left"] || writes.Fields["right"] {
			t.Fatalf("%s: recursive summary lost its field subset: %+v", name, writes)
		}
		var caller storageViewCalleeWrites
		caller.union(writes)
		caller.write("right")
		cached := a.storageViewCalleeWritesFor([]*ast.FuncDecl{decl}, 0)
		if cached.All || !cached.Fields["left"] || cached.Fields["right"] {
			t.Fatalf("%s: caller's union changed the cached summary: %+v", name, cached)
		}
	}
}

func TestStorageViewSpareUnionsImmutableCalleeSummaries(t *testing.T) {
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_write_union.elisa", `struct Pair:
    left: mutable darray[u8] = []
    right: mutable darray[u8] = []
module Left:
    public:
        def step(pair: mutable Pair&) -> void:
            pair.left.clear()
module Right:
    public:
        def step(pair: mutable Pair&) -> void:
            pair.right.clear()
`, AnalyzeOptions{})
	a := r.analyzer
	a.currentScope = a.globalScope
	arg := &ast.Ident{Name: "pair"}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "step"}, Args: []ast.Expr{arg}}
	spare := a.storageViewCalleeWriteSpare(call, arg)
	if spare == nil || spare("pair.left", "pair") || spare("pair.right", "pair") || !spare("pair.other", "pair") {
		t.Fatal("possible callees must invalidate both written fields and spare the rest")
	}
	for _, writes := range a.storageViewCalleeWriteCache {
		if writes.All || len(writes.Fields) != 1 {
			t.Fatalf("joining possible callees mutated an immutable cached summary: %+v", writes)
		}
	}
}
