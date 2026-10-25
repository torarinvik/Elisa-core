//go:build cgo

package backend

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func retainedProofFunctionIndex(t *testing.T, result *semantic.Result, name string) (*ast.FuncDecl, *ast.IndexExpr) {
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
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == name {
			if index := find(fn.Body); index != nil {
				return fn, index
			}
		}
	}
	t.Fatalf("live index in %s not found", name)
	return nil, nil
}

func TestRetainedIndexProofRetractionRevertAndDiskRoundTrip(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := retainedIndexProofSpanSource + `
def unaffected(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        if i < xs.count:
            return xs[i]
        return 0
`
	after := strings.Replace(before, "if i < xs.count:", "if i > xs.count:", 1)
	parse := func(source string) *ast.File {
		return parseRetainedDisjointCacheSource(t, "retained-proof-lifetime.elisa", source)
	}
	result, state := semantic.AnalyzeRetained(parse(before), parse(before), []byte(before), semantic.AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	for step, source := range []string{after, before, after, before} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatalf("step%d snapshot: %v", step, err)
		}
		state, err = semantic.RestoreAnalysisState(data)
		if err != nil {
			t.Fatalf("step%d restore: %v", step, err)
		}
		old, ok := state.ResultForSource([]byte(before))
		if step%2 == 1 {
			old, ok = state.ResultForSource([]byte(after))
		}
		if !ok {
			t.Fatalf("step%d lost previous revision", step)
		}
		_, oldIndex := retainedProofFunctionIndex(t, old, "read")
		_, unaffected := retainedProofFunctionIndex(t, old, "unaffected")
		if !old.IndexBoundsProven[unaffected] {
			t.Fatal("unaffected baseline proof missing")
		}
		updated, err := state.Update(parse(source), parse(source), []byte(source))
		if err != nil || state.LastStats.Fallback != "" {
			t.Fatalf("step%d update: %v %+v", step, err, state.LastStats)
		}
		if len(updated.Errors()) != 0 {
			t.Fatal(updated.Errors())
		}
		_, current := retainedProofFunctionIndex(t, updated, "read")
		_, kept := retainedProofFunctionIndex(t, updated, "unaffected")
		if kept != unaffected || !updated.IndexBoundsProven[kept] {
			t.Fatal("unaffected function identity/proof was discarded")
		}
		if oldIndex != current && updated.IndexBoundsProven[oldIndex] {
			t.Fatal("detached proof survives function invalidation")
		}
		want := step%2 == 1
		if updated.IndexBoundsProven[current] != want {
			t.Fatalf("step%d new proof=%v want=%v", step, updated.IndexBoundsProven[current], want)
		}
		cold := semantic.AnalyzeWithOptions(parse(source), semantic.AnalyzeOptions{})
		_, fresh := retainedProofFunctionIndex(t, cold, "read")
		if len(cold.Errors()) != 0 || cold.IndexBoundsProven[fresh] != want {
			t.Fatal("cold oracle differs from expected proof")
		}
		for _, shared := range []bool{false, true} {
			g := &llvmGenerator{result: updated}
			if shared {
				g.sharedLookups = newSharedCodegenLookups(updated)
			}
			state := &functionState{g: g}
			rebuilt := *current
			if state.indexBoundsProven(&rebuilt) != want {
				t.Fatalf("step%d shared=%v valid rebuilt proof fallback differs", step, shared)
			}
			rebuiltKept := *kept
			if !state.indexBoundsProven(&rebuiltKept) {
				t.Fatal("unaffected rebuilt proof fallback lost")
			}
		}
		result = updated
	}
}
