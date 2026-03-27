package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

type packedMLASTNativeBuildEntry struct {
	once         sync.Once
	executable   string
	clangMissing bool
	errMessage   string
}

var (
	packedMLASTNativeBuildMu      sync.Mutex
	packedMLASTNativeBuildEntries = map[string]*packedMLASTNativeBuildEntry{}
)

func buildPackedMLASTNativeExecutable(tb testing.TB, repoRoot string, objectOpt string) string {
	tb.Helper()

	key := objectOpt
	packedMLASTNativeBuildMu.Lock()
	entry, ok := packedMLASTNativeBuildEntries[key]
	if !ok {
		entry = &packedMLASTNativeBuildEntry{}
		packedMLASTNativeBuildEntries[key] = entry
	}
	packedMLASTNativeBuildMu.Unlock()

	entry.once.Do(func() {
		clangPath, err := exec.LookPath("clang")
		if err != nil {
			entry.clangMissing = true
			return
		}

		fixturePath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_mega_core.llcontext")
		harnessPath := filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_bench.c")
		shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c")
		runtimePath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c")

		outputDir, err := os.MkdirTemp("", "packed_ml_ast_native_*")
		if err != nil {
			entry.errMessage = "failed to create temp dir: " + err.Error()
			return
		}

		headerPath := filepath.Join(outputDir, "packed_lowering_ml_ast_mega_core.h")
		objectPath := filepath.Join(outputDir, "packed_lowering_ml_ast_mega_core.o")
		exePath := filepath.Join(outputDir, "packed_lowering_ml_ast_bench")

		for _, args := range [][]string{
			{"-emit", "header", "-o", headerPath, fixturePath},
			{"-emit", "obj", objectOpt, "-o", objectPath, fixturePath},
		} {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI(args, &stdout, &stderr)
			if exitCode != 0 {
				entry.errMessage = "runCLI failed for native packed ML AST build: " + stderr.String()
				return
			}
			if stdout.Len() != 0 {
				entry.errMessage = "expected no stdout while building native packed ML AST executable, got:\n" + stdout.String()
				return
			}
			if stderr.Len() != 0 {
				entry.errMessage = "expected no stderr while building native packed ML AST executable, got:\n" + stderr.String()
				return
			}
		}

		compileArgs := []string{objectOpt, "-pthread"}
		if runtime.GOOS == "darwin" {
			compileArgs = append(compileArgs, "-Wl,-undefined,dynamic_lookup")
		}
		compileArgs = append(compileArgs, "-I", outputDir, harnessPath, shimPath, runtimePath, objectPath, "-o", exePath)

		compileCmd := exec.Command(clangPath, compileArgs...)
		compileOutput, err := compileCmd.CombinedOutput()
		if err != nil {
			entry.errMessage = "clang failed for native packed ML AST build: " + err.Error() + "\n" + string(compileOutput)
			return
		}

		entry.executable = exePath
	})

	if entry.clangMissing {
		tb.Skip("clang not available")
	}
	if entry.errMessage != "" {
		tb.Fatalf("%s", entry.errMessage)
	}
	return entry.executable
}

func requireSlowNativeMLAST(tb testing.TB) {
	tb.Helper()
	if testing.Short() {
		tb.Skip("skipping slow native ML AST smoke in short mode")
	}
	if os.Getenv("LLCONTEXT_SLOW_NATIVE") == "" {
		tb.Skip("skipping slow native ML AST smoke; set LLCONTEXT_SLOW_NATIVE=1 to run it")
	}
}
