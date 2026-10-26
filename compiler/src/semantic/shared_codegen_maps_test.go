package semantic

import (
	"elisacore/src/ast"
	"sync"
	"testing"
)

func TestSharedCodegenMapCopiesIsolateAndRestore(t *testing.T) {
	node := &ast.Ident{Name: "value"}
	helperCall := &ast.CallExpr{}
	oldType := &BuiltinType{Name: "i64"}
	original := map[ast.Expr]Type{node: oldType}
	private := map[string]bool{"private": true}
	helperCalls := map[*ast.CallExpr]string{helperCall: "clone"}
	a := &Analyzer{exprTypes: original, programRegions: private, compilerBuiltinHelperCalls: helperCalls}
	r := &Result{ExprTypes: original, analyzer: a}
	r.BeginSharedCodegen()
	if !r.IsProgramRegion("private") || !r.IsCompilerBuiltinHelperCall(helperCall, "clone") {
		t.Fatal("shared codegen snapshots missed analyzed query rows")
	}
	replacement := &BuiltinType{Name: "bool"}
	a.exprTypes[node] = replacement
	added := &ast.Ident{Name: "added"}
	a.exprTypes[added] = replacement
	if r.ExprTypes[node] != oldType || r.ExprTypes[added] != nil {
		t.Fatal("analyzer writes leaked to shared result")
	}
	a.programRegions["same-private-map"] = true
	if !private["same-private-map"] {
		t.Fatal("unshared map was copied")
	}
	a.programRegions["private"] = false
	a.compilerBuiltinHelperCalls[helperCall] = "mutated"
	if !r.IsProgramRegion("private") || r.IsProgramRegion("same-private-map") || !r.IsCompilerBuiltinHelperCall(helperCall, "clone") || r.IsCompilerBuiltinHelperCall(helperCall, "mutated") {
		t.Fatal("shared codegen queries did not retain their immutable snapshots")
	}
	r.EndSharedCodegen()
	if a.exprTypes[node] != oldType || a.exprTypes[added] != nil {
		t.Fatal("original analyzer map was not restored")
	}
	a.exprTypes[added] = oldType
	if r.ExprTypes[added] != oldType {
		t.Fatal("restored map lost identity")
	}
}

func TestSharedCodegenAnalyzerQuerySnapshotsAreConcurrent(t *testing.T) {
	call := &ast.CallExpr{}
	r := &Result{analyzer: &Analyzer{
		compilerBuiltinHelperCalls: map[*ast.CallExpr]string{call: "clone"},
		programRegions:             map[string]bool{"program": true},
	}}
	r.BeginSharedCodegen()
	defer r.EndSharedCodegen()

	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 1000; j++ {
				if !r.IsProgramRegion("program") || !r.IsCompilerBuiltinHelperCall(call, "clone") {
					t.Error("shared analyzer query snapshot lost an immutable row")
					return
				}
			}
		}()
	}
	workers.Wait()
}

func BenchmarkSharedCodegenMapCopies(b *testing.B) {
	original := make(map[ast.Expr]Type, 10000)
	typ := &BuiltinType{Name: "i64"}
	for i := 0; i < 10000; i++ {
		original[&ast.Ident{Name: "value"}] = typ
	}
	a := &Analyzer{exprTypes: original}
	r := &Result{ExprTypes: original, analyzer: a}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.BeginSharedCodegen()
		r.EndSharedCodegen()
	}
}
