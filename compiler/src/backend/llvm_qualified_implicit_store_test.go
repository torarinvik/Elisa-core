//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestQualifiedImplicitPackedStoreUsesExpectedType(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "qualified_implicit_store.elisa", `module Shapes:
    public:
        enum Node layout(handle: u32):
            pass
        enum Expr is Node:
            Lit(value: i64)
            Pair(left: Expr, right: Expr)

def read(node: Shapes::Expr) -> i64:
    return match node:
        Shapes::Expr.Lit(value): value
        _: 0

def caller(node: Shapes::Expr) -> i64:
    return read(node)
`)
	// Backend-recovered implicit arguments have no entry in the semantic type
	// table. Recreate that condition while retaining the typed callee signature.
	synthetic := 0
	for node := range result.ExprTypes {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			continue
		}
		for _, arg := range call.ResolvedImplicitArgs {
			if ident, ok := arg.(*ast.Ident); ok && strings.HasPrefix(ident.Name, semantic.PackedStoreImplicitUsePrefix) {
				delete(result.ExprTypes, ident)
				synthetic++
			}
		}
	}
	if synthetic == 0 {
		t.Fatal("fixture did not thread a consumer store")
	}
	if _, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0); err != nil {
		t.Fatalf("qualified store failed without expression metadata: %v", err)
	}
}
