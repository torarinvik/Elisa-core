package semantic

import (
	"elisacore/src/ast"
	"testing"
)

// Positional call analysis aliases Args and ResolvedArgs. Rewriting a deeply
// nested analyzed call must keep that sharing and replace the semantic operands.
func TestSubstituteIdentsPreservesAnalyzedCallDAG(t *testing.T) {
	const depth = 96
	var source ast.Expr = &ast.Ident{Name: "result"}
	for i := 0; i < depth; i++ {
		args := []ast.Expr{source}
		source = &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: args, ResolvedArgs: args, ResolvedArgsValid: true}
	}
	replacement := &ast.IntLit{Value: "7"}
	rewritten := substituteIdents(source, map[string]ast.Expr{"result": replacement})
	for i := 0; i < depth; i++ {
		original := source.(*ast.CallExpr)
		call, ok := rewritten.(*ast.CallExpr)
		if !ok || !call.ResolvedArgsValid || len(call.Args) != 1 || len(call.ResolvedArgs) != 1 {
			t.Fatalf("lost analyzed call at depth %d", i)
		}
		if call.Args[0] != call.ResolvedArgs[0] {
			t.Fatalf("duplicated shared argument at depth %d", i)
		}
		if call.Func != original.Func {
			t.Fatalf("lost unchanged callee identity at depth %d", i)
		}
		source, rewritten = original.Args[0], call.Args[0]
	}
	if rewritten != replacement {
		t.Fatal("resolved/source argument did not receive result substitution")
	}
}
