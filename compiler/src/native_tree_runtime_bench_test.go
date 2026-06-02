package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"elisacore/src/backend"
)

func nativeTreeRuntimeBenchSource(layout string) string {
	annotation := ""
	if strings.TrimSpace(layout) != "" {
		annotation = "@layout(" + layout + ")\n"
	}
	if layout == "per_variant_rows" {
		return fmt.Sprintf(`extern tree_runtime_escape(value: i64) -> i64

%stree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)
		Call(callee: Expr, args: darray[Expr])

def eval(node: Lua.Expr) -> i64:
	return visit node as Lua.Expr:
		Lua.Expr.Int(expr):
			expr.value + expr.span
		Lua.Expr.Binary(expr):
			eval(expr.left) + eval(expr.right) + expr.span
		Lua.Expr.Call(expr):
			eval(expr.callee) + expr.args.len.cast[i64] + expr.span

def tree_runtime_bench_impl(repeats: i64) -> i64:
	can Memory.Allocate:
		region scratch(32768)
		owner: mutable Arena& = scratch.ref[mutable Arena&]
		in owner:
			a: Lua.Expr = Lua.Expr.Int(span: 1, value: 10)
			b: Lua.Expr = Lua.Expr.Int(span: 2, value: 20)
			c: Lua.Expr = Lua.Expr.Int(span: 3, value: 30)
			d: Lua.Expr = Lua.Expr.Int(span: 4, value: 40)
			left: Lua.Expr = Lua.Expr.Binary(span: 5, left: a, right: b)
			right: Lua.Expr = Lua.Expr.Binary(span: 6, left: c, right: d)
			root: Lua.Expr = Lua.Expr.Binary(span: 7, left: left, right: right)
			total: mutable i64 = 0
			for _ in 0..<repeats:
				total <- total + tree_runtime_escape(eval(root))
			out: i64 = total
			destroy scratch
			return out

export func tree_runtime_bench(repeats: i64) -> i64 = tree_runtime_bench_impl
`, annotation)
	}
	return fmt.Sprintf(`extern tree_runtime_escape(value: i64) -> i64

%stree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)
		Call(callee: Expr, args: darray[Expr])

def eval(store: Lua.Store[Local], node: Lua.Expr) -> i64:
	in store:
		if node is Lua.Expr.Int:
			return node.value + node.span
		if node is Lua.Expr.Binary:
			return eval(store, node.left) + eval(store, node.right) + node.span
		if node is Lua.Expr.Call:
			return eval(store, node.callee) + node.args.len.cast[i64] + node.span
		return 0

def tree_runtime_bench_impl(repeats: i64) -> i64:
	can Memory.Allocate:
		region scratch(32768)
		store = Lua.Store(scratch)
		in store:
			a: Lua.Expr = Lua.Expr.Int(span: 1, value: 10)
			b: Lua.Expr = Lua.Expr.Int(span: 2, value: 20)
			c: Lua.Expr = Lua.Expr.Int(span: 3, value: 30)
			d: Lua.Expr = Lua.Expr.Int(span: 4, value: 40)
			left: Lua.Expr = Lua.Expr.Binary(span: 5, left: a, right: b)
			right: Lua.Expr = Lua.Expr.Binary(span: 6, left: c, right: d)
			root: Lua.Expr = Lua.Expr.Binary(span: 7, left: left, right: right)
			total: mutable i64 = 0
			for _ in 0..<repeats:
				total <- total + tree_runtime_escape(eval(store, root))
			out: i64 = total
			destroy scratch
			return out

export func tree_runtime_bench(repeats: i64) -> i64 = tree_runtime_bench_impl
`, annotation)
}

func buildNativeTreeRuntimeBenchExecutable(b *testing.B, layout string) string {
	b.Helper()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		b.Skip("clang not available")
	}

	repoRoot := repoRootFromMainBench(b)
	fixturePath := filepath.Join(repoRoot, "Code", "benchmarks", "tree_runtime_"+benchmarkTreeLayoutFilenameSuffix(layout)+".elisa")
	source := []byte(nativeTreeRuntimeBenchSource(layout))
	var stderr bytes.Buffer
	_, result, ok := analyzeProgram(fixturePath, source, &stderr)
	if !ok {
		b.Fatalf("failed to analyze native tree runtime benchmark source:\n%s", stderr.String())
	}
	tempDir, err := os.MkdirTemp("", "elisacore-tree-runtime-bench-*")
	if err != nil {
		b.Fatalf("failed to create native tree runtime benchmark temp dir: %v", err)
	}
	shimPath := filepath.Join(tempDir, "tree_runtime_bench_main.c")
	if err := os.WriteFile(shimPath, []byte(nativeTreeRuntimeBenchShimSource()), 0o644); err != nil {
		_ = os.RemoveAll(tempDir)
		b.Fatalf("failed to write native tree runtime benchmark shim: %v", err)
	}
	exePath, nativeCleanup, _, err := buildNativeExecutableWithClang(clangPath, result, []string{shimPath}, nil, filepath.Join(tempDir, "tree_runtime_bench"), backend.OptimizationLevel3, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		b.Fatalf("failed to build native tree runtime benchmark executable:\n%s%s", err.Error(), stderr.String())
	}
	b.Cleanup(func() {
		nativeCleanup()
		_ = os.RemoveAll(tempDir)
	})
	return exePath
}

func nativeTreeRuntimeBenchShimSource() string {
	return `#include <stdlib.h>

extern long long tree_runtime_bench(long long repeats);

__attribute__((noinline)) long long tree_runtime_escape(long long value) {
#if defined(__GNUC__) || defined(__clang__)
	__asm__ __volatile__("" : "+r"(value));
#endif
	return value;
}

int main(int argc, char **argv) {
	if (argc < 2) {
		return 2;
	}
	long long repeats = atoll(argv[1]);
	long long got = tree_runtime_bench(repeats);
	long long want = 128LL * repeats;
	return got == want ? 0 : 3;
}
`
}

func benchmarkTreeLayoutFilenameSuffix(layout string) string {
	trimmed := strings.TrimSpace(layout)
	if trimmed == "" {
		return "default_category_union"
	}
	return strings.NewReplacer("-", "_").Replace(trimmed)
}

func benchmarkNativeTreeRuntimeLayout(b *testing.B, layout string) {
	b.Helper()
	exePath := buildNativeTreeRuntimeBenchExecutable(b, layout)
	args := []string{strconv.Itoa(b.N)}
	b.SetBytes(1)
	b.ReportAllocs()
	b.ResetTimer()
	cmd := exec.Command(exePath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		b.Fatalf("native tree runtime benchmark failed for layout %q: %v\n%s", layout, err, string(output))
	}
}

func BenchmarkNativeTreeRuntimePerVariantRows(b *testing.B) {
	benchmarkNativeTreeRuntimeLayout(b, "per_variant_rows")
}

func BenchmarkNativeTreeRuntimeDefaultCategoryUnion(b *testing.B) {
	benchmarkNativeTreeRuntimeLayout(b, "")
}
