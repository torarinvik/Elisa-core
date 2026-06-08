package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"elisacore/src/backend"
)

func buildNativeDictRuntimeBenchExecutable(b *testing.B) string {
	b.Helper()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		b.Skip("clang not available")
	}

	repoRoot := repoRootFromMainBench(b)
	fixturePath := filepath.Join(repoRoot, "Code", "benchmarks", "dict_runtime_bench.elisa")
	source, err := readSourceWithIncludes(fixturePath, map[string]bool{})
	if err != nil {
		b.Fatalf("failed to read dict runtime benchmark fixture with includes: %v", err)
	}

	var stderr bytes.Buffer
	_, result, ok := analyzeProgram(fixturePath, source, &stderr)
	if !ok {
		b.Fatalf("failed to analyze dict runtime benchmark fixture:\n%s", stderr.String())
	}

	tempDir, err := os.MkdirTemp("", "elisacore-dict-runtime-bench-*")
	if err != nil {
		b.Fatalf("failed to create dict runtime benchmark temp dir: %v", err)
	}
	shimPath := filepath.Join(tempDir, "dict_runtime_bench_main.c")
	if err := os.WriteFile(shimPath, []byte(nativeDictRuntimeBenchShimSource()), 0o644); err != nil {
		_ = os.RemoveAll(tempDir)
		b.Fatalf("failed to write dict runtime benchmark shim: %v", err)
	}

	exePath, nativeCleanup, _, err := buildNativeExecutableWithClang(clangPath, result, []string{shimPath}, nil, filepath.Join(tempDir, "dict_runtime_bench"), backend.OptimizationLevel3, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		b.Fatalf("failed to build native dict runtime benchmark executable:\n%s%s", err.Error(), stderr.String())
	}
	b.Cleanup(func() {
		nativeCleanup()
		_ = os.RemoveAll(tempDir)
	})
	return exePath
}

func nativeDictRuntimeBenchShimSource() string {
	return `#include <stdlib.h>
#include <string.h>

extern long long dict_i64_growth_churn(long long repeats);
extern long long dict_cstr_lookup(long long repeats);
extern long long dict_enum_bool(long long repeats);

__attribute__((noinline)) long long dict_bench_escape(long long value) {
#if defined(__GNUC__) || defined(__clang__)
	__asm__ __volatile__("" : "+r"(value));
#endif
	return value;
}

int main(int argc, char **argv) {
	if (argc < 3) {
		return 2;
	}
	long long repeats = atoll(argv[2]);
	long long got = 0;
	if (strcmp(argv[1], "i64") == 0) {
		got = dict_i64_growth_churn(repeats);
	} else if (strcmp(argv[1], "cstr") == 0) {
		got = dict_cstr_lookup(repeats);
	} else if (strcmp(argv[1], "enum_bool") == 0) {
		got = dict_enum_bool(repeats);
	} else {
		return 4;
	}
	return got > 0 ? 0 : 3;
}
`
}

func benchmarkNativeDictRuntimeWorkload(b *testing.B, workload string, opsPerRepeat int64) {
	b.Helper()
	exePath := buildNativeDictRuntimeBenchExecutable(b)
	b.SetBytes(int64(b.N) * opsPerRepeat)
	b.ReportAllocs()
	b.ResetTimer()
	cmd := exec.Command(exePath, workload, strconv.Itoa(b.N))
	output, err := cmd.CombinedOutput()
	if err != nil {
		b.Fatalf("native dict runtime benchmark failed for workload %q: %v\n%s", workload, err, string(output))
	}
}

func BenchmarkNativeDictRuntimeI64GrowthChurn(b *testing.B) {
	benchmarkNativeDictRuntimeWorkload(b, "i64", 32)
}

func BenchmarkNativeDictRuntimeCStrLookup(b *testing.B) {
	benchmarkNativeDictRuntimeWorkload(b, "cstr", 4)
}

func BenchmarkNativeDictRuntimeEnumBool(b *testing.B) {
	benchmarkNativeDictRuntimeWorkload(b, "enum_bool", 5)
}
