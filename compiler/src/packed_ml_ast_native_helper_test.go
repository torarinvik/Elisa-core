package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

type nativeArtifactSpec struct {
	name            string
	objectOpt       string
	fixturePath     string
	exeName         string
	harnessPath     string
	harnessSource   string
	shimPaths       []string
	generateHeader  bool
	headerName      string
	objectName      string
	clangArgs       []string
	hashFiles       []string
	hashExpandedSrc bool
}

type nativeArtifacts struct {
	dir        string
	header     string
	object     string
	executable string
	cacheKey   string
	cacheHit   bool
}

type nativeArtifactBuildEntry struct {
	once         sync.Once
	artifacts    nativeArtifacts
	clangMissing bool
	errMessage   string
}

var (
	nativeArtifactBuildMu      sync.Mutex
	nativeArtifactBuildEntries = map[string]*nativeArtifactBuildEntry{}
)

func buildPackedMLASTNativeExecutable(tb testing.TB, repoRoot string, objectOpt string) string {
	tb.Helper()
	spec := packedMLASTMegaNativeSpec(repoRoot, objectOpt)
	return buildCachedNativeArtifacts(tb, repoRoot, spec).executable
}

func buildPackedMLASTMediumNativeExecutable(tb testing.TB, repoRoot string, objectOpt string) string {
	tb.Helper()
	spec := packedMLASTMediumNativeSpec(repoRoot, objectOpt)
	return buildCachedNativeArtifacts(tb, repoRoot, spec).executable
}

func buildPackedMLExprReproExecutable(tb testing.TB, repoRoot string, objectOpt string) string {
	tb.Helper()
	spec := packedMLExprReproNativeSpec(repoRoot, objectOpt)
	return buildCachedNativeArtifacts(tb, repoRoot, spec).executable
}

func buildCachedNativeArtifacts(tb testing.TB, repoRoot string, spec nativeArtifactSpec) nativeArtifacts {
	tb.Helper()

	key, cacheRoot, err := nativeArtifactCacheKey(repoRoot, spec)
	if err != nil {
		tb.Fatalf("failed to compute native artifact cache key for %s: %v", spec.name, err)
	}
	entryKey := spec.name + ":" + key
	nativeArtifactBuildMu.Lock()
	entry, ok := nativeArtifactBuildEntries[entryKey]
	if !ok {
		entry = &nativeArtifactBuildEntry{}
		nativeArtifactBuildEntries[entryKey] = entry
	}
	nativeArtifactBuildMu.Unlock()

	entry.once.Do(func() {
		clangPath, err := exec.LookPath("clang")
		if err != nil {
			entry.clangMissing = true
			return
		}

		finalDir := filepath.Join(cacheRoot, spec.name, key)
		artifacts := spec.artifactPaths(finalDir, key)
		if artifacts.exist() {
			artifacts.cacheHit = true
			entry.artifacts = artifacts
			debugNativeArtifactCache("hit", spec, artifacts)
			return
		}
		if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
			entry.errMessage = "failed to create cache dir: " + err.Error()
			return
		}

		buildDir, err := os.MkdirTemp(filepath.Dir(finalDir), spec.name+"_build_*")
		if err != nil {
			entry.errMessage = "failed to create temp dir: " + err.Error()
			return
		}

		buildArtifacts := spec.artifactPaths(buildDir, key)
		if spec.harnessSource != "" {
			if err := os.WriteFile(buildArtifacts.harnessPath(spec), []byte(spec.harnessSource), 0o644); err != nil {
				entry.errMessage = "failed to write generated harness: " + err.Error()
				return
			}
		}

		runCLICommands := make([][]string, 0, 2)
		if spec.generateHeader {
			runCLICommands = append(runCLICommands, []string{"-emit", "header", "-o", buildArtifacts.header, spec.fixturePath})
		}
		runCLICommands = append(runCLICommands, []string{"-emit", "obj", spec.objectOpt, "-o", buildArtifacts.object, spec.fixturePath})
		for _, args := range runCLICommands {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI(args, &stdout, &stderr)
			if exitCode != 0 {
				entry.errMessage = "runCLI failed for native artifact build: " + stderr.String()
				return
			}
			if stdout.Len() != 0 {
				entry.errMessage = "expected no stdout while building native artifact, got:\n" + stdout.String()
				return
			}
			if stderr.Len() != 0 {
				entry.errMessage = "expected no stderr while building native artifact, got:\n" + stderr.String()
				return
			}
		}

		compileArgs := append([]string{}, spec.clangArgs...)
		if runtime.GOOS == "darwin" {
			compileArgs = append(compileArgs, "-Wl,-undefined,dynamic_lookup")
		}
		if spec.generateHeader {
			compileArgs = append(compileArgs, "-I", buildDir)
		}
		harnessPath := spec.harnessPath
		if spec.harnessSource != "" {
			harnessPath = buildArtifacts.harnessPath(spec)
		}
		compileArgs = append(compileArgs, harnessPath)
		compileArgs = append(compileArgs, spec.shimPaths...)
		compileArgs = append(compileArgs, buildArtifacts.object, "-o", buildArtifacts.executable)

		compileCmd := exec.Command(clangPath, compileArgs...)
		compileOutput, err := compileCmd.CombinedOutput()
		if err != nil {
			entry.errMessage = "clang failed for native artifact build: " + err.Error() + "\n" + string(compileOutput)
			return
		}

		if err := os.RemoveAll(finalDir); err != nil && !os.IsNotExist(err) {
			entry.errMessage = "failed to clear stale cache dir: " + err.Error()
			return
		}
		if err := os.Rename(buildDir, finalDir); err != nil {
			if artifacts.exist() {
				artifacts.cacheHit = true
				entry.artifacts = artifacts
				debugNativeArtifactCache("hit-race", spec, artifacts)
				return
			}
			entry.errMessage = "failed to publish cached native artifact build: " + err.Error()
			return
		}

		artifacts.cacheHit = false
		entry.artifacts = artifacts
		debugNativeArtifactCache("miss", spec, artifacts)
	})

	if entry.clangMissing {
		tb.Skip("clang not available")
	}
	if entry.errMessage != "" {
		tb.Fatalf("%s", entry.errMessage)
	}
	return entry.artifacts
}

func packedMLASTMegaNativeSpec(repoRoot string, objectOpt string) nativeArtifactSpec {
	return nativeArtifactSpec{
		name:            "packed-ml-ast-mega",
		objectOpt:       objectOpt,
		fixturePath:     filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_mega_core.llcontext"),
		exeName:         "packed_lowering_ml_ast_bench",
		harnessPath:     filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_bench.c"),
		shimPaths:       []string{filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c"), filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c")},
		generateHeader:  true,
		headerName:      "packed_lowering_ml_ast_mega_core.h",
		objectName:      "packed_lowering_ml_ast_mega_core.o",
		clangArgs:       []string{objectOpt, "-pthread"},
		hashExpandedSrc: true,
		hashFiles: []string{
			filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_bench.c"),
			filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c"),
			filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c"),
		},
	}
}

func packedMLASTMediumNativeSpec(repoRoot string, objectOpt string) nativeArtifactSpec {
	return nativeArtifactSpec{
		name:            "packed-ml-ast-medium",
		objectOpt:       objectOpt,
		fixturePath:     filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_medium_core.llcontext"),
		exeName:         "packed_lowering_ml_ast_medium_bench",
		harnessPath:     filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_bench.c"),
		shimPaths:       []string{filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c"), filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c")},
		generateHeader:  true,
		headerName:      "packed_lowering_ml_ast_mega_core.h",
		objectName:      "packed_lowering_ml_ast_medium_core.o",
		clangArgs:       []string{objectOpt, "-pthread"},
		hashExpandedSrc: true,
		hashFiles: []string{
			filepath.Join(repoRoot, "Code", "benchmarks", "packed_lowering_ml_ast_bench.c"),
			filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c"),
			filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c"),
		},
	}
}

func packedMLExprReproNativeSpec(repoRoot string, objectOpt string) nativeArtifactSpec {
	return nativeArtifactSpec{
		name:            "packed-ml-expr-repro",
		objectOpt:       objectOpt,
		fixturePath:     filepath.Join(repoRoot, "Code", "benchmarks", "packed_runtime_ml_expr_repro.llcontext"),
		exeName:         "packed_runtime_ml_expr_repro",
		harnessSource:   "#include <stdio.h>\nlong long packed_ml_expr_repro(void);\nint main(void) { printf(\"%lld\\n\", packed_ml_expr_repro()); return 0; }\n",
		shimPaths:       []string{filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c"), filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c")},
		generateHeader:  false,
		objectName:      "packed_runtime_ml_expr_repro.o",
		clangArgs:       []string{objectOpt},
		hashExpandedSrc: true,
		hashFiles: []string{
			filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c"),
			filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_concurrency_runtime.c"),
		},
	}
}

func nativeArtifactCacheKey(repoRoot string, spec nativeArtifactSpec) (string, string, error) {
	hash := sha256.New()
	writeHashString(hash, "name="+spec.name)
	writeHashString(hash, "objectOpt="+spec.objectOpt)
	writeHashString(hash, "goos="+runtime.GOOS)
	writeHashString(hash, "goarch="+runtime.GOARCH)
	writeHashString(hash, "goversion="+runtime.Version())
	for _, arg := range spec.clangArgs {
		writeHashString(hash, "clangArg="+arg)
	}
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return "", "", err
	}
	writeHashString(hash, "clang="+clangPath)
	writeHashString(hash, "generateHeader="+fmt.Sprintf("%t", spec.generateHeader))
	writeHashString(hash, "fixturePath="+spec.fixturePath)
	if spec.hashExpandedSrc {
		expandedFixture, err := readSourceWithIncludes(spec.fixturePath, map[string]bool{})
		if err != nil {
			return "", "", err
		}
		writeHashBytes(hash, "expanded_fixture", expandedFixture)
	} else if err := hashFileInto(hash, spec.fixturePath); err != nil {
		return "", "", err
	}
	if spec.harnessSource != "" {
		writeHashBytes(hash, "inline_harness", []byte(spec.harnessSource))
	}
	for _, path := range append([]string{spec.harnessPath}, spec.hashFiles...) {
		if path == "" {
			continue
		}
		if err := hashFileInto(hash, path); err != nil {
			return "", "", err
		}
	}
	if err := hashGoFilesUnder(hash, filepath.Join(repoRoot, "compiler", "src")); err != nil {
		return "", "", err
	}

	cacheRoot, err := nativeArtifactCacheRoot()
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), cacheRoot, nil
}

func nativeArtifactCacheRoot() (string, error) {
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		return filepath.Join(base, "llcontext", "native_artifacts"), nil
	}
	return filepath.Join(os.TempDir(), "llcontext-native-artifact-cache"), nil
}

func (spec nativeArtifactSpec) artifactPaths(dir string, key string) nativeArtifacts {
	artifacts := nativeArtifacts{
		dir:        dir,
		cacheKey:   key,
		executable: filepath.Join(dir, spec.exeName),
	}
	if spec.headerName != "" {
		artifacts.header = filepath.Join(dir, spec.headerName)
	}
	if spec.objectName != "" {
		artifacts.object = filepath.Join(dir, spec.objectName)
	}
	return artifacts
}

func (artifacts nativeArtifacts) exist() bool {
	if artifacts.executable == "" {
		return false
	}
	if _, err := os.Stat(artifacts.executable); err != nil {
		return false
	}
	if artifacts.object != "" {
		if _, err := os.Stat(artifacts.object); err != nil {
			return false
		}
	}
	if artifacts.header != "" {
		if _, err := os.Stat(artifacts.header); err != nil {
			return false
		}
	}
	return true
}

func (artifacts nativeArtifacts) harnessPath(spec nativeArtifactSpec) string {
	return filepath.Join(artifacts.dir, spec.name+"_main.c")
}

func debugNativeArtifactCache(status string, spec nativeArtifactSpec, artifacts nativeArtifacts) {
	if os.Getenv("LLCONTEXT_CACHE_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[llcontext-cache] %s name=%s key=%s exe=%s\n", status, spec.name, artifacts.cacheKey[:12], artifacts.executable)
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
