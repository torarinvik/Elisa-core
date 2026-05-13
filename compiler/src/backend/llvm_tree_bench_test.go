//go:build cgo

package backend

import "testing"

func treeBenchmarkSource(layout string) string {
	prefix := ""
	if layout != "" {
		prefix = "@layout(" + layout + ")\n"
	}
	return prefix + `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(left: Expr, right: Expr)
		Call(callee: Expr, args: darray[Expr])

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
	in perm:
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
}

func BenchmarkGenerateLLVMIRTreeAoSRows(b *testing.B) {
	benchmarkGenerateLLVMIRTreeLayout(b, "tree_aos_rows_bench.elisa", treeBenchmarkSource("per_variant_rows"))
}

func BenchmarkGenerateLLVMIRTreeCategoryUnion(b *testing.B) {
	benchmarkGenerateLLVMIRTreeLayout(b, "tree_category_union_bench.elisa", treeBenchmarkSource(""))
}

func BenchmarkGenerateLLVMIRFrozenTreeRowQueries(b *testing.B) {
	src := `@layout(soa)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Name(name_index: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def query(owner: Arena, target: i64) -> usize:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(span: target, value: 1)
		right = Lua.Expr.Name(span: target, name_index: 2)
		_ = Lua.Expr.Add(span: target, left: left, right: right)
	frozen = freeze(move store)
	ints: usize = count node in frozen.Expr.where_kind(.Int) where node.span == target
	names: usize = count node in frozen.Expr where kind == .Name and name_index == target
	return ints + names
`
	benchmarkGenerateLLVMIRTreeLayout(b, "tree_frozen_row_queries_bench.elisa", src)
}

func benchmarkGenerateLLVMIRTreeLayout(b *testing.B, filename string, src string) {
	result := parseAndAnalyzeBackendBenchmarkSource(b, filename, src)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel0, DefaultPackedLoweringProfile()); err != nil {
			b.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
		}
	}
}
