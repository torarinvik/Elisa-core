package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func repoRootFromMainBench(b *testing.B) string {
	b.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("failed to determine benchmark file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func benchmarkCLICompileToLLVM(b *testing.B, sourcePath string) {
	b.Helper()

	expandedSource, err := readSourceWithIncludes(sourcePath, map[string]bool{})
	if err != nil {
		b.Fatalf("failed to read benchmark source %s: %v", sourcePath, err)
	}
	if len(expandedSource) == 0 {
		b.Fatalf("expected benchmark source %s to contain input", sourcePath)
	}

	args := []string{"-emit", "llvm", sourcePath}
	var stderr bytes.Buffer

	b.SetBytes(int64(len(expandedSource)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stderr.Reset()
		exitCode := runCLI(args, io.Discard, &stderr)
		if exitCode != 0 {
			b.Fatalf("runCLI(%v) returned %d\nstderr:\n%s", args, exitCode, stderr.String())
		}
		if stderr.Len() != 0 {
			b.Fatalf("expected no stderr for %v, got:\n%s", args, stderr.String())
		}
	}
}

func BenchmarkRunCLICompileSelfHostedFrontendToLLVM(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "frontend_llcontext", "contextlang_frontend.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToLLVM(b, sourcePath)
}

func BenchmarkRunCLICompileJSONParserFixtureToLLVM(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToLLVM(b, sourcePath)
}
