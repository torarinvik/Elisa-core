//go:build cgo

package backend

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

const retainedIndexProofSpanSource = `def read(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        if i < xs.count:
            return xs[i]
        return 0
`

func retainedSpanFixtureIndex(t *testing.T, result *semantic.Result) (*ast.FuncDecl, *ast.IndexExpr) {
	t.Helper()
	var find func([]ast.Stmt) *ast.IndexExpr
	find = func(body []ast.Stmt) *ast.IndexExpr {
		for _, stmt := range body {
			switch n := stmt.(type) {
			case *ast.CanStmt:
				if index := find(n.Body); index != nil {
					return index
				}
			case *ast.IfStmt:
				if index := find(n.Then); index != nil {
					return index
				}
				if index := find(n.Else); index != nil {
					return index
				}
			case *ast.ReturnStmt:
				if index, ok := n.Value.(*ast.IndexExpr); ok {
					return index
				}
			}
		}
		return nil
	}
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "read" {
			if index := find(fn.Body); index != nil {
				return fn, index
			}
		}
	}
	t.Fatal("fixture live AST index not found")
	return nil, nil
}

// This exercises actual retained Update, not a manufactured stale Result map.
// Its assertions establish the proof retraction and exact-span collision before
// checking consumers. A declined edit or unproved baseline fails explicitly.
func TestRetainedIndexProofSpanDoesNotReviveDetachedProof(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := retainedIndexProofSpanSource
	after := strings.Replace(before, "if i < xs.count:", "if i > xs.count:", 1)
	parse := func(source string) *ast.File {
		return parseRetainedDisjointCacheSource(t, "retained-proof-span.elisa", source)
	}
	initial, retained := semantic.AnalyzeRetained(parse(before), parse(before), []byte(before), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatalf("baseline fixture rejected: %v", initial.Errors())
	}
	_, oldIndex := retainedSpanFixtureIndex(t, initial)
	if !initial.IndexBoundsProven[oldIndex] {
		t.Fatal("baseline bounded access not actually proved")
	}
	updated, err := retained.Update(parse(after), parse(after), []byte(after))
	if err != nil {
		t.Fatalf("actual retained body edit declined: %v", err)
	}
	if retained.LastStats.Fallback != "" || len(updated.Errors()) != 0 {
		t.Fatalf("edited fixture invalid/fell back: %+v errors=%v", retained.LastStats, updated.Errors())
	}
	fn, liveIndex := retainedSpanFixtureIndex(t, updated)
	if liveIndex == oldIndex || liveIndex.Position != oldIndex.Position {
		t.Fatalf("fixture did not replace index at exact same span: old=%p %v live=%p %v", oldIndex, oldIndex.Position, liveIndex, liveIndex.Position)
	}
	if updated.IndexBoundsProven[liveIndex] {
		t.Fatal("new out-of-bounds branch unexpectedly has a direct live proof")
	}
	cold := semantic.AnalyzeWithOptions(parse(after), semantic.AnalyzeOptions{})
	if len(cold.Errors()) != 0 {
		t.Fatalf("cold edited fixture rejected: %v", cold.Errors())
	}
	_, coldIndex := retainedSpanFixtureIndex(t, cold)
	if cold.IndexBoundsProven[coldIndex] {
		t.Fatal("cold edited access unexpectedly proved")
	}
	_, liveEntry := updated.IndexBoundsProven[liveIndex]
	t.Logf("detached old proof retained=%v; current-key present=%v; old/new exact span=%v", updated.IndexBoundsProven[oldIndex], liveEntry, liveIndex.Position)

	for _, shared := range []bool{false, true} {
		name := "local-lookup"
		if shared {
			name = "shared-lookup"
		}
		t.Run(name, func(t *testing.T) {
			g := &llvmGenerator{result: updated}
			if shared {
				g.sharedLookups = newSharedCodegenLookups(updated)
			}
			state := &functionState{g: g}
			if state.indexBoundsProven(liveIndex) {
				t.Error("backend span fallback revived a proof absent for the live current node")
			}
			rebuilt := *liveIndex
			if state.indexBoundsProven(&rebuilt) {
				t.Error("rebuilt unproved index inherited a detached historical proof by source span")
			}
			if state.allIndexedAccessesProven(fn.Body) {
				t.Error("autovec eligibility accepted an unproved current access")
			}
		})
	}
}
