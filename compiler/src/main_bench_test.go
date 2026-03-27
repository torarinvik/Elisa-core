package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
)

var (
	frontendLexerBenchBuildOnce       sync.Once
	frontendLexerBenchBuildClangMiss  bool
	frontendLexerBenchExecutablePath  string
	frontendLexerBenchBuildErrMessage string

	packedMLASTBenchBuildOnce       sync.Once
	packedMLASTBenchBuildClangMiss  bool
	packedMLASTBenchExecutablePath  string
	packedMLASTBenchBuildErrMessage string
)

func repoRootFromMainBench(b *testing.B) string {
	b.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("failed to determine benchmark file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func benchmarkCLICompileToLLVM(b *testing.B, sourcePath string, extraArgs ...string) {
	b.Helper()

	expandedSource, err := readSourceWithIncludes(sourcePath, map[string]bool{})
	if err != nil {
		b.Fatalf("failed to read benchmark source %s: %v", sourcePath, err)
	}
	if len(expandedSource) == 0 {
		b.Fatalf("expected benchmark source %s to contain input", sourcePath)
	}

	args := append([]string{"-emit", "llvm"}, extraArgs...)
	args = append(args, sourcePath)
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

func benchmarkCLICompileToLLVMParallel(b *testing.B, sourcePath string, workers int, extraArgs ...string) {
	b.Helper()

	expandedSource, err := readSourceWithIncludes(sourcePath, map[string]bool{})
	if err != nil {
		b.Fatalf("failed to read benchmark source %s: %v", sourcePath, err)
	}
	if len(expandedSource) == 0 {
		b.Fatalf("expected benchmark source %s to contain input", sourcePath)
	}

	args := append([]string{"-emit", "llvm"}, extraArgs...)
	args = append(args, sourcePath)
	prevMaxProcs := runtime.GOMAXPROCS(workers)
	defer runtime.GOMAXPROCS(prevMaxProcs)

	b.SetBytes(int64(len(expandedSource)))
	b.ReportAllocs()
	b.ResetTimer()

	jobs := make(chan struct{}, workers)
	errs := make(chan string, 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var stderr bytes.Buffer
			for range jobs {
				stderr.Reset()
				exitCode := runCLI(args, io.Discard, &stderr)
				if exitCode == 0 && stderr.Len() == 0 {
					continue
				}
				msg := stderr.String()
				if exitCode != 0 {
					if msg == "" {
						msg = "<no stderr>"
					}
					msg = "runCLI returned non-zero exit code: " + msg
				}
				select {
				case errs <- msg:
				default:
				}
			}
		}()
	}
	for i := 0; i < b.N; i++ {
		jobs <- struct{}{}
	}
	close(jobs)
	wg.Wait()

	select {
	case msg := <-errs:
		b.Fatalf("parallel CLI benchmark failed for %s: %s", sourcePath, msg)
	default:
	}
}

func benchmarkCLICompileToObject(b *testing.B, sourcePath string, extraArgs ...string) {
	b.Helper()

	expandedSource, err := readSourceWithIncludes(sourcePath, map[string]bool{})
	if err != nil {
		b.Fatalf("failed to read benchmark source %s: %v", sourcePath, err)
	}
	if len(expandedSource) == 0 {
		b.Fatalf("expected benchmark source %s to contain input", sourcePath)
	}

	outputPath := filepath.Join(b.TempDir(), "bench_output.o")
	argsPrefix := append([]string{"-emit", "obj"}, extraArgs...)

	b.SetBytes(int64(len(expandedSource)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		args := append(append([]string{}, argsPrefix...), "-o", outputPath, sourcePath)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runCLI(args, &stdout, &stderr)
		if exitCode != 0 {
			b.Fatalf("runCLI(%v) returned %d\nstderr:\n%s", args, exitCode, stderr.String())
		}
		if stdout.Len() != 0 {
			b.Fatalf("expected no stdout for %v, got:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			b.Fatalf("expected no stderr for %v, got:\n%s", args, stderr.String())
		}
	}
}

func buildFrontendLexerBenchExecutableForBench(b *testing.B) string {
	b.Helper()

	frontendLexerBenchBuildOnce.Do(func() {
		clangPath, err := exec.LookPath("clang")
		if err != nil {
			frontendLexerBenchBuildClangMiss = true
			return
		}

		repoRoot := repoRootFromMainBench(b)
		fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "frontend_lexer.llcontext")
		harnessPath := filepath.Join(repoRoot, "Code", "benchmarks", "frontend_lexer_bench.c")
		shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "frontend_lexer_runtime_shims.c")

		outputDir, err := os.MkdirTemp("", "frontend_lexer_native_bench_*")
		if err != nil {
			frontendLexerBenchBuildErrMessage = "failed to create temp dir: " + err.Error()
			return
		}

		headerPath := filepath.Join(outputDir, "frontend_lexer.h")
		objectPath := filepath.Join(outputDir, "frontend_lexer.o")
		exePath := filepath.Join(outputDir, "frontend_lexer_bench")

		for _, args := range [][]string{
			{"-emit", "header", "-packed-abi", "row-handle", "-o", headerPath, fixturePath},
			{"-emit", "obj", "-O3", "-packed-abi", "row-handle", "-o", objectPath, fixturePath},
		} {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI(args, &stdout, &stderr)
			if exitCode != 0 {
				frontendLexerBenchBuildErrMessage = "runCLI failed for native frontend lexer benchmark: " + stderr.String()
				return
			}
			if stdout.Len() != 0 {
				frontendLexerBenchBuildErrMessage = "expected no stdout while building native frontend lexer benchmark, got:\n" + stdout.String()
				return
			}
			if stderr.Len() != 0 {
				frontendLexerBenchBuildErrMessage = "expected no stderr while building native frontend lexer benchmark, got:\n" + stderr.String()
				return
			}
		}

		compileArgs := []string{"-O3"}
		if runtime.GOOS == "darwin" {
			compileArgs = append(compileArgs, "-Wl,-undefined,dynamic_lookup")
		}
		compileArgs = append(compileArgs, "-I", outputDir, harnessPath, shimPath, objectPath, "-o", exePath)

		compileCmd := exec.Command(clangPath, compileArgs...)
		compileOutput, err := compileCmd.CombinedOutput()
		if err != nil {
			frontendLexerBenchBuildErrMessage = "clang failed for native frontend lexer benchmark: " + err.Error() + "\n" + string(compileOutput)
			return
		}

		frontendLexerBenchExecutablePath = exePath
	})

	if frontendLexerBenchBuildClangMiss {
		b.Skip("clang not available")
	}
	if frontendLexerBenchBuildErrMessage != "" {
		b.Fatal(frontendLexerBenchBuildErrMessage)
	}
	return frontendLexerBenchExecutablePath
}

func buildPackedMLASTBenchExecutableForBench(b *testing.B) string {
	b.Helper()

	packedMLASTBenchBuildOnce.Do(func() {
		clangPath, err := exec.LookPath("clang")
		if err != nil {
			packedMLASTBenchBuildClangMiss = true
			return
		}

		repoRoot := repoRootFromMainBench(b)
		fixturePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_mega_core.llcontext")
		harnessPath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_bench.c")
		shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c")
		runtimePath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c")

		outputDir, err := os.MkdirTemp("", "packed_ml_ast_native_bench_*")
		if err != nil {
			packedMLASTBenchBuildErrMessage = "failed to create temp dir: " + err.Error()
			return
		}

		headerPath := filepath.Join(outputDir, "packed_lowering_ml_ast_mega_core.h")
		objectPath := filepath.Join(outputDir, "packed_lowering_ml_ast_mega_core.o")
		exePath := filepath.Join(outputDir, "packed_lowering_ml_ast_bench")

		for _, args := range [][]string{
			{"-emit", "header", "-packed-abi", "row-handle", "-o", headerPath, fixturePath},
			{"-emit", "obj", "-O3", "-packed-abi", "row-handle", "-o", objectPath, fixturePath},
		} {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI(args, &stdout, &stderr)
			if exitCode != 0 {
				packedMLASTBenchBuildErrMessage = "runCLI failed for native packed ML AST benchmark: " + stderr.String()
				return
			}
			if stdout.Len() != 0 {
				packedMLASTBenchBuildErrMessage = "expected no stdout while building native packed ML AST benchmark, got:\n" + stdout.String()
				return
			}
			if stderr.Len() != 0 {
				packedMLASTBenchBuildErrMessage = "expected no stderr while building native packed ML AST benchmark, got:\n" + stderr.String()
				return
			}
		}

		compileArgs := []string{"-O3", "-pthread"}
		if runtime.GOOS == "darwin" {
			compileArgs = append(compileArgs, "-Wl,-undefined,dynamic_lookup")
		}
		compileArgs = append(compileArgs, "-I", outputDir, harnessPath, shimPath, runtimePath, objectPath, "-o", exePath)

		compileCmd := exec.Command(clangPath, compileArgs...)
		compileOutput, err := compileCmd.CombinedOutput()
		if err != nil {
			packedMLASTBenchBuildErrMessage = "clang failed for native packed ML AST benchmark: " + err.Error() + "\n" + string(compileOutput)
			return
		}

		packedMLASTBenchExecutablePath = exePath
	})

	if packedMLASTBenchBuildClangMiss {
		b.Skip("clang not available")
	}
	if packedMLASTBenchBuildErrMessage != "" {
		b.Fatal(packedMLASTBenchBuildErrMessage)
	}
	return packedMLASTBenchExecutablePath
}

func benchmarkNativeFrontendLexerRuntime(b *testing.B, sourcePath string) {
	b.Helper()

	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		b.Fatalf("failed to read benchmark source %s: %v", sourcePath, err)
	}
	if len(raw) == 0 {
		b.Fatalf("expected benchmark source %s to contain input", sourcePath)
	}

	exePath := buildFrontendLexerBenchExecutableForBench(b)

	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command(exePath, sourcePath, "1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			b.Fatalf("native frontend lexer benchmark failed for %s: %v\n%s", sourcePath, err, string(output))
		}
	}
}

func benchmarkNativeFrontendLexerRuntimeParallel(b *testing.B, sourcePath string, workers int) {
	b.Helper()

	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		b.Fatalf("failed to read benchmark source %s: %v", sourcePath, err)
	}
	if len(raw) == 0 {
		b.Fatalf("expected benchmark source %s to contain input", sourcePath)
	}

	exePath := buildFrontendLexerBenchExecutableForBench(b)

	prevMaxProcs := runtime.GOMAXPROCS(workers)
	defer runtime.GOMAXPROCS(prevMaxProcs)

	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()

	jobs := make(chan struct{}, workers)
	errs := make(chan string, 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				cmd := exec.Command(exePath, sourcePath, "1")
				output, err := cmd.CombinedOutput()
				if err == nil {
					continue
				}
				msg := err.Error()
				if len(output) != 0 {
					msg += "\n" + string(output)
				}
				select {
				case errs <- msg:
				default:
				}
			}
		}()
	}
	for i := 0; i < b.N; i++ {
		jobs <- struct{}{}
	}
	close(jobs)
	wg.Wait()

	select {
	case msg := <-errs:
		b.Fatalf("parallel native frontend lexer benchmark failed for %s: %s", sourcePath, msg)
	default:
	}
}

func benchmarkNativePackedMLASTRuntime(b *testing.B, sourcePath string, mode string, workers int) {
	b.Helper()

	expandedSource, err := readSourceWithIncludes(sourcePath, map[string]bool{})
	if err != nil {
		b.Fatalf("failed to read benchmark source %s: %v", sourcePath, err)
	}
	if len(expandedSource) == 0 {
		b.Fatalf("expected benchmark source %s to contain input", sourcePath)
	}

	exePath := buildPackedMLASTBenchExecutableForBench(b)

	args := []string{mode, "1"}
	if mode == "parallel" {
		args = append(args, strconv.Itoa(workers))
	}

	b.SetBytes(int64(len(expandedSource)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command(exePath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			b.Fatalf("native packed ML AST benchmark failed for %s (%s): %v\n%s", sourcePath, mode, err, string(output))
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

func BenchmarkRunCLICompileFrontendLexerFixtureToLLVM(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "test_programs", "frontend_lexer.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToLLVM(b, sourcePath)
}

func BenchmarkRunCLICompileFrontendLexerFixtureToLLVMO3(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "test_programs", "frontend_lexer.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToLLVM(b, sourcePath, "-O3")
}

func BenchmarkRunCLICompileFrontendLexerFixtureToObjectO3(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "test_programs", "frontend_lexer.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToObject(b, sourcePath, "-O3")
}

func BenchmarkRunCLICompilePackedMegaASTToLLVM(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_parser_ast_mega_bench.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToLLVM(b, sourcePath)
}

func BenchmarkRunCLICompilePackedMegaASTParallel10ToLLVM(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_parser_ast_mega_parallel_bench.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToLLVMParallel(b, sourcePath, 10)
}

func BenchmarkRunCLICompilePackedMLASTRowHandleToLLVM(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_mega_bench.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToLLVM(b, sourcePath, "-packed-abi", "row-handle")
}

func BenchmarkRunCLICompilePackedMLASTRowHandleParallel10ToLLVM(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_mega_parallel_bench.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToLLVMParallel(b, sourcePath, 10, "-packed-abi", "row-handle")
}

func BenchmarkRunCLICompilePackedMLASTRowHandleToObjectO3(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_mega_bench.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkCLICompileToObject(b, sourcePath, "-O3", "-packed-abi", "row-handle")
}

func BenchmarkRunNativeFrontendLexerPackedMegaAST(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_parser_ast_mega_core.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkNativeFrontendLexerRuntime(b, sourcePath)
}

func BenchmarkRunNativeFrontendLexerPackedMegaASTParallel10(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_parser_ast_mega_core.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkNativeFrontendLexerRuntimeParallel(b, sourcePath, 10)
}

func BenchmarkRunNativePackedMLASTRowHandleRuntime(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_mega_core.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkNativePackedMLASTRuntime(b, sourcePath, "scalar", 1)
}

func BenchmarkRunNativePackedMLASTRowHandleRuntimeParallel10(b *testing.B) {
	repoRoot := repoRootFromMainBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_mega_core.llcontext")
	if _, err := os.Stat(sourcePath); err != nil {
		b.Fatalf("failed to stat %s: %v", sourcePath, err)
	}
	benchmarkNativePackedMLASTRuntime(b, sourcePath, "parallel", 10)
}
