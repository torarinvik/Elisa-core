package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/backend"
)

const nativeTreeRuntimeBenchRepeats = 2048

func nativeTreeRuntimeBenchSource(layout string, repeats int) string {
	annotation := ""
	if strings.TrimSpace(layout) != "" {
		annotation = "@layout(" + layout + ")\n"
	}
	if layout == "per_variant_rows" {
		return fmt.Sprintf(`%stree Lua:
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

@test
def tree_runtime_bench_test() -> void:
	can Abort.Panic, Memory.Allocate:
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
			for _ in 0..<%d:
				total <- total + eval(root)
			assert_eq(total, 128i64 * %di64)
`, annotation, repeats, repeats)
	}
	return fmt.Sprintf(`%stree Lua:
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

@test
def tree_runtime_bench_test() -> void:
	can Abort.Panic, Memory.Allocate:
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
			for _ in 0..<%d:
				total <- total + eval(store, root)
			assert_eq(total, 128i64 * %di64)
`, annotation, repeats, repeats)
}

func buildNativeTreeRuntimeBenchExecutable(b *testing.B, layout string) string {
	b.Helper()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		b.Skip("clang not available")
	}

	repoRoot := repoRootFromMainBench(b)
	fixturePath := filepath.Join(repoRoot, "Code", "benchmarks", "tree_runtime_"+benchmarkTreeLayoutFilenameSuffix(layout)+".elisa")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testPrelude, err := readSourceWithIncludes(testPath, map[string]bool{})
	if err != nil {
		b.Fatalf("failed to read native test prelude: %v", err)
	}
	source := append(append([]byte{}, testPrelude...), '\n')
	source = append(source, []byte(nativeTreeRuntimeBenchSource(layout, nativeTreeRuntimeBenchRepeats))...)
	var stderr bytes.Buffer
	_, result, ok := analyzeProgram(fixturePath, source, &stderr)
	if !ok {
		b.Fatalf("failed to analyze native tree runtime benchmark source:\n%s", stderr.String())
	}
	cases := runnableTestCases(selectTestCases(result, "tree_runtime_bench_test"))
	if len(cases) != 1 {
		b.Fatalf("expected one runnable tree runtime benchmark test, got %d", len(cases))
	}
	runnerSource := buildDispatchTestRunnerSource(source, cases)
	exePath, cleanup, _, _, _, err := compileTestRunnerExecutableWithShim(clangPath, runnerSource, testRunnerDispatchShimSource(cases), nil, nil, backend.OptimizationLevel3, backend.DefaultPackedLoweringProfile(), &stderr)
	if err != nil {
		b.Fatalf("failed to build native tree runtime benchmark executable:\n%s%s", err.Error(), stderr.String())
	}
	b.Cleanup(cleanup)
	return exePath
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
	args := []string{"tree_runtime_bench_test"}
	b.SetBytes(int64(nativeTreeRuntimeBenchRepeats))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command(exePath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			b.Fatalf("native tree runtime benchmark failed for layout %q: %v\n%s", layout, err, string(output))
		}
	}
}

func BenchmarkNativeTreeRuntimePerVariantRows(b *testing.B) {
	benchmarkNativeTreeRuntimeLayout(b, "per_variant_rows")
}

func BenchmarkNativeTreeRuntimeDefaultCategoryUnion(b *testing.B) {
	benchmarkNativeTreeRuntimeLayout(b, "")
}
