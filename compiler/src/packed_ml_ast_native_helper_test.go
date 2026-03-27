package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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

	key, cacheRoot, err := packedMLASTNativeBuildCacheKey(repoRoot, objectOpt)
	if err != nil {
		tb.Fatalf("failed to compute native packed ML AST cache key: %v", err)
	}
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
		finalDir := filepath.Join(cacheRoot, key)
		exePath := filepath.Join(finalDir, "packed_lowering_ml_ast_bench")
		if _, err := os.Stat(exePath); err == nil {
			entry.executable = exePath
			return
		}
		if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
			entry.errMessage = "failed to create cache dir: " + err.Error()
			return
		}

		outputDir, err := os.MkdirTemp(cacheRoot, "packed_ml_ast_native_build_*")
		if err != nil {
			entry.errMessage = "failed to create temp dir: " + err.Error()
			return
		}

		headerPath := filepath.Join(outputDir, "packed_lowering_ml_ast_mega_core.h")
		objectPath := filepath.Join(outputDir, "packed_lowering_ml_ast_mega_core.o")
		tempExePath := filepath.Join(outputDir, "packed_lowering_ml_ast_bench")

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
		compileArgs = append(compileArgs, "-I", outputDir, harnessPath, shimPath, runtimePath, objectPath, "-o", tempExePath)

		compileCmd := exec.Command(clangPath, compileArgs...)
		compileOutput, err := compileCmd.CombinedOutput()
		if err != nil {
			entry.errMessage = "clang failed for native packed ML AST build: " + err.Error() + "\n" + string(compileOutput)
			return
		}

		if err := os.RemoveAll(finalDir); err != nil && !os.IsNotExist(err) {
			entry.errMessage = "failed to clear stale cache dir: " + err.Error()
			return
		}
		if err := os.Rename(outputDir, finalDir); err != nil {
			if _, statErr := os.Stat(exePath); statErr == nil {
				entry.executable = exePath
				return
			}
			entry.errMessage = "failed to publish cached native packed ML AST build: " + err.Error()
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

func packedMLASTNativeBuildCacheKey(repoRoot string, objectOpt string) (string, string, error) {
	hash := sha256.New()
	writeHashString(hash, "objectOpt="+objectOpt)
	writeHashString(hash, "goos="+runtime.GOOS)
	writeHashString(hash, "goarch="+runtime.GOARCH)
	writeHashString(hash, "goversion="+runtime.Version())

	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return "", "", err
	}
	writeHashString(hash, "clang="+clangPath)

	expandedFixture, err := readSourceWithIncludes(filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_mega_core.llcontext"), map[string]bool{})
	if err != nil {
		return "", "", err
	}
	writeHashBytes(hash, "expanded_fixture", expandedFixture)

	for _, path := range []string{
		filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_bench.c"),
		filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c"),
		filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c"),
	} {
		if err := hashFileInto(hash, path); err != nil {
			return "", "", err
		}
	}
	if err := hashGoFilesUnder(hash, filepath.Join(repoRoot, "compiler", "src")); err != nil {
		return "", "", err
	}

	cacheRoot, err := packedMLASTNativeCacheRoot()
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), cacheRoot, nil
}

func packedMLASTNativeCacheRoot() (string, error) {
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		return filepath.Join(base, "llcontext", "packed_ml_ast_native"), nil
	}
	return filepath.Join(os.TempDir(), "llcontext-packed-ml-ast-cache"), nil
}

func hashGoFilesUnder(hash hashWriter, root string) error {
	paths := make([]string, 0, 64)
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := hashFileInto(hash, path); err != nil {
			return err
		}
	}
	return nil
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func hashFileInto(hash hashWriter, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	writeHashBytes(hash, path, content)
	return nil
}

func writeHashBytes(hash hashWriter, label string, content []byte) {
	writeHashString(hash, label)
	_, _ = hash.Write(content)
	_, _ = hash.Write([]byte{0})
}

func writeHashString(hash hashWriter, value string) {
	_, _ = hash.Write([]byte(value))
	_, _ = hash.Write([]byte{0})
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
