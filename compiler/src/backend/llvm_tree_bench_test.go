//go:build cgo

package backend

import "testing"

const treeAoSBenchmarkSource = `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(left: Expr, right: Expr)
		Call(callee: Expr, args: darray[Expr])
	block Block:
		items: darray[Expr]

def make_binary(owner: mutable Arena&, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
	in owner:
		return Lua.Expr.Binary(span: 1i64, left: left, right: right)

def rewrite_binary(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	in perm:
		return node{left, right}

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Nil(expr):
				default
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Binary(expr, left, right):
				default{span = expr.span, left, right}
			Lua.Expr.Call(expr, callee, args):
				default

def score(node: Lua.Expr) -> i64:
	return visit node as Lua.Expr:
		Lua.Expr.Nil(expr):
			0i64
		Lua.Expr.Int(expr):
			expr.value
		Lua.Expr.Binary(expr):
			expr.left.span + expr.right.span
		Lua.Expr.Call(expr):
			expr.callee.span + expr.args.len.cast[i64]
`

func BenchmarkGenerateLLVMIRTreeAoSRows(b *testing.B) {
	result := parseAndAnalyzeBackendBenchmarkSource(b, "tree_aos_rows_bench.elisa", treeAoSBenchmarkSource)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel0, DefaultPackedLoweringProfile()); err != nil {
			b.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
		}
	}
}
