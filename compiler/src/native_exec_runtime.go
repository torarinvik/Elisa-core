package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/semantic"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func withDefaultNativeRuntimeForeignFiles(foreignFiles []string) ([]string, error) {
	return dedupeStrings(append([]string(nil), foreignFiles...)), nil
}

func compilerRepoRootForNativeExec() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("failed to locate compiler source root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..")), nil
}

func hasConcurrencyRuntimeForeignFile(foreignFiles []string) bool {
	for _, foreignFile := range foreignFiles {
		base := filepath.Base(strings.TrimSpace(foreignFile))
		if base == "concurrency.c" || strings.HasSuffix(base, "_concurrency_runtime.c") {
			return true
		}
	}
	return false
}

func nativeExecutableNeedsPThread(foreignFiles []string) bool {
	_ = foreignFiles
	return true
}

func defaultElisaCoreRuntimeSupportPath() (string, error) {
	repoRoot, err := compilerRepoRootForNativeExec()
	if err != nil {
		return "", err
	}
	path := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "native_runtime_support.elisa")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("failed to locate default elisacore runtime support %s: %w", path, err)
	}
	return path, nil
}

func profilerFallbackSourcePath() (string, error) {
	repoRoot, err := compilerRepoRootForNativeExec()
	if err != nil {
		return "", err
	}
	path := filepath.Join(repoRoot, "compiler", "runtime", "profile_hooks.c")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("failed to locate profiler fallback source %s: %w", path, err)
	}
	return path, nil
}

func writeProfilerFallbackObject(outputPath string, targetTriple string, stderr io.Writer) error {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return fmt.Errorf("clang is required for the profiler fallback: %w", err)
	}
	sourcePath, err := profilerFallbackSourcePath()
	if err != nil {
		return err
	}
	args := []string{"-c", "-O2", "-o", outputPath, sourcePath}
	if strings.TrimSpace(targetTriple) != "" {
		args = append([]string{"-target", targetTriple}, args...)
	}
	cmd := exec.Command(clangPath, args...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to compile profiler fallback: %w", err)
	}
	return nil
}

func mergeProfilerFallbackObject(outputPath string, targetTriple string, stderr io.Writer) error {
	dir := filepath.Dir(outputPath)
	fallbackFile, err := os.CreateTemp(dir, ".elisacore-profile-hooks-*.o")
	if err != nil {
		return err
	}
	fallbackPath := fallbackFile.Name()
	if err := fallbackFile.Close(); err != nil {
		_ = os.Remove(fallbackPath)
		return err
	}
	defer os.Remove(fallbackPath)
	if err := writeProfilerFallbackObject(fallbackPath, targetTriple, stderr); err != nil {
		return err
	}

	mergedFile, err := os.CreateTemp(dir, ".elisacore-runtime-merged-*.o")
	if err != nil {
		return err
	}
	mergedPath := mergedFile.Name()
	if err := mergedFile.Close(); err != nil {
		_ = os.Remove(mergedPath)
		return err
	}
	defer os.Remove(mergedPath)
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return fmt.Errorf("clang is required to merge the profiler fallback: %w", err)
	}
	args := []string{"-r", "-o", mergedPath, outputPath, fallbackPath}
	if strings.TrimSpace(targetTriple) != "" {
		args = append([]string{"-target", targetTriple}, args...)
	}
	cmd := exec.Command(clangPath, args...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to merge profiler fallback: %w", err)
	}
	if err := os.Rename(mergedPath, outputPath); err != nil {
		return fmt.Errorf("failed to publish profiler fallback: %w", err)
	}
	return nil
}

// writeDefaultElisaCoreRuntimeObject emits the default runtime support object, serving it
// from the content-addressed runtime-object cache when possible. The underlying compile
// (parse + analyze + -O3 codegen of the include-expanded runtime) is identical across every
// build that shares the cache key, so this avoids redoing it per test. docs/93.
func writeDefaultElisaCoreRuntimeObject(outputPath string, packedProfile backend.PackedLoweringProfile, targetTriple string, stderr io.Writer) error {
	if !testRunnerCacheEnabled() || !runtimeObjectCacheEnabled() {
		return compileDefaultElisaCoreRuntimeObject(outputPath, packedProfile, targetTriple, stderr)
	}
	artifact, err := runtimeObjectCacheArtifactFor(packedProfile, targetTriple)
	if err != nil {
		// Key derivation failed (e.g. runtime source unreadable) -- fall back to a direct
		// compile rather than failing the build.
		return compileDefaultElisaCoreRuntimeObject(outputPath, packedProfile, targetTriple, stderr)
	}
	if usableCachedFile(artifact.object, false) {
		if copyErr := copyExecutableFile(artifact.object, outputPath); copyErr == nil {
			debugRuntimeObjectCache(stderr, "hit", artifact)
			return nil
		}
		// Copy failed (entry removed/corrupt mid-read) -- recompile below.
	}
	debugRuntimeObjectCache(stderr, "miss", artifact)
	if err := compileDefaultElisaCoreRuntimeObject(outputPath, packedProfile, targetTriple, stderr); err != nil {
		return err
	}
	if pubErr := publishCachedRuntimeObject(artifact, outputPath); pubErr != nil {
		// Non-fatal: a failed publish just means the next build recompiles.
		debugRuntimeObjectCache(stderr, "publish-error", artifact)
	} else {
		debugRuntimeObjectCache(stderr, "publish", artifact)
	}
	return nil
}

const defaultElisaCoreProfilerFallbackSource = `#include <stddef.h>
#include <stdint.h>

#if defined(__GNUC__) || defined(__clang__)
#define ELISA_WEAK __attribute__((weak))
#else
#define ELISA_WEAK
#endif

/* The profiler ABI is optional.  Keep ordinary runtime objects self-contained,
   while allowing a final executable to replace these weak hooks with a real
   collector. */
ELISA_WEAK uint32_t elisa_profile_allocation_negotiate(uint32_t version) {
    (void)version;
    return 0;
}

ELISA_WEAK uint32_t elisa_profile_region_layout_negotiate(uint32_t version) {
    (void)version;
    return 0;
}

ELISA_WEAK void elisa_profile_region_layout_v1(
    uintptr_t arena,
    size_t region,
    uintptr_t header,
    uintptr_t data,
    size_t capacity
) {
    (void)arena;
    (void)region;
    (void)header;
    (void)data;
    (void)capacity;
}

ELISA_WEAK void elisa_profile_allocation_event_v1(
    uint32_t kind,
    uintptr_t address,
    size_t size,
    uintptr_t old_address,
    size_t old_size,
    uintptr_t arena,
    size_t region
) {
    (void)kind;
    (void)address;
    (void)size;
    (void)old_address;
    (void)old_size;
    (void)arena;
    (void)region;
}
`

// writeElisaCoreProfilerFallbackObject makes the optional profiler ABI available to a native
// link. The weak definitions keep ordinary executables self-contained while allowing a final
// link to replace them with a real collector.
func writeElisaCoreProfilerFallbackObject(clangPath string, outputPath string, targetTriple string, stderr io.Writer) error {
	sourcePath := outputPath + ".profiler.c"
	defer os.Remove(sourcePath)
	if err := os.WriteFile(sourcePath, []byte(defaultElisaCoreProfilerFallbackSource), 0o644); err != nil {
		return fmt.Errorf("failed to write default profiler fallback: %w", err)
	}
	compileArgs := append([]string{}, targetClangArgs(targetTriple)...)
	compileArgs = append(compileArgs, "-c", sourcePath, "-o", outputPath)
	compile := exec.Command(clangPath, compileArgs...)
	compile.Stdout = stderr
	compile.Stderr = stderr
	if err := compile.Run(); err != nil {
		return fmt.Errorf("failed to compile default profiler fallback: %w", err)
	}
	return nil
}

// addDefaultElisaCoreProfilerFallback makes the auto-linked runtime object safe for
// ordinary programs.  The runtime deliberately exposes the profiler ABI as extern hooks so
// a final link can provide a collector; without these weak definitions, a dynamic-lookup link
// succeeds on macOS and then calls address zero on the first arena allocation.
func addDefaultElisaCoreProfilerFallback(clangPath string, runtimeObjectPath string, outputPath string, targetTriple string, stderr io.Writer) error {
	hooksObjectPath := outputPath + ".profiler.o"
	defer os.Remove(hooksObjectPath)
	if err := writeElisaCoreProfilerFallbackObject(clangPath, hooksObjectPath, targetTriple, stderr); err != nil {
		return err
	}
	mergeArgs := append([]string{}, targetClangArgs(targetTriple)...)
	mergeArgs = append(mergeArgs, "-r", "-Wl,-w", "-o", outputPath, runtimeObjectPath, hooksObjectPath)
	merge := exec.Command(clangPath, mergeArgs...)
	merge.Stdout = stderr
	merge.Stderr = stderr
	if err := merge.Run(); err != nil {
		return fmt.Errorf("failed to link default profiler fallback: %w", err)
	}
	return nil
}

// analyzeDefaultElisaCoreRuntime parses and analyzes the include-expanded default runtime
// support module (shared by the native and the LTO runtime objects).
func analyzeDefaultElisaCoreRuntime(stderr io.Writer) (*semantic.Result, error) {
	runtimePath, err := defaultElisaCoreRuntimeSupportPath()
	if err != nil {
		return nil, err
	}
	src, err := readSourceWithIncludes(runtimePath, map[string]bool{})
	if err != nil {
		return nil, err
	}
	var parseStderr bytes.Buffer
	file, ok := parseProgram(runtimePath, src, &parseStderr)
	if !ok {
		if parseStderr.Len() != 0 && stderr != nil {
			_, _ = io.Copy(stderr, &parseStderr)
		}
		return nil, fmt.Errorf("failed to parse default elisacore runtime support")
	}
	runtimeResult := semantic.Analyze(file)
	if errs := runtimeResult.Errors(); len(errs) != 0 {
		if stderr != nil {
			for _, e := range errs {
				fmt.Fprintf(stderr, "%s\n", e)
			}
		}
		return nil, fmt.Errorf("failed to analyze default elisacore runtime support")
	}
	if warns := runtimeResult.Notices(); len(warns) != 0 && stderr != nil {
		for _, w := range warns {
			if shouldSuppressDeprecatedWarningsForTests(w) {
				continue
			}
			fmt.Fprintf(stderr, "%s\n", w)
		}
	}
	return runtimeResult, nil
}

func compileDefaultElisaCoreRuntimeObject(outputPath string, packedProfile backend.PackedLoweringProfile, targetTriple string, stderr io.Writer) error {
	runtimeResult, err := analyzeDefaultElisaCoreRuntime(stderr)
	if err != nil {
		return err
	}
	if clangPath, err := exec.LookPath("clang"); err == nil {
		// The fallback source is an embedded constant here, not a file located
		// relative to the repo, so an installed compiler does not depend on the
		// checkout still being present. The non-IR path below keeps
		// mergeProfilerFallbackObject, which rewrites outputPath in place.
		runtimeObjectPath := outputPath + ".runtime"
		defer os.Remove(runtimeObjectPath)
		if err := writeNativeObjectViaClangIR(clangPath, runtimeResult, runtimeObjectPath, backend.OptimizationLevel3, packedProfile, targetTriple, false, false, stderr); err == nil {
			return addDefaultElisaCoreProfilerFallback(clangPath, runtimeObjectPath, outputPath, targetTriple, stderr)
		} else if strings.TrimSpace(targetTriple) != "" {
			return err
		}
	}
	if err := backend.WriteLLVMObjectFileWithOptions(runtimeResult, outputPath, backend.LLVMObjectEmitOptions{
		OptLevel:      backend.OptimizationLevel3,
		PackedProfile: packedProfile,
		TargetTriple:  targetTriple,
	}); err != nil {
		return err
	}
	return mergeProfilerFallbackObject(outputPath, targetTriple, stderr)
}

func resultDefinesDefaultElisaCoreRuntime(result *semantic.Result) bool {
	if result == nil || result.GlobalScope == nil {
		return false
	}
	for _, name := range []string{"arena_alloc", "ctx_strlen", "ctx_string_view_slice"} {
		sym, ok := result.GlobalScope.Lookup(name)
		if !ok || sym == nil {
			return false
		}
		if _, ok := sym.Node.(*ast.FuncDecl); !ok {
			return false
		}
	}
	return true
}

// resultDefinesDebugReferee reports whether the program supplies the debug_referee
// implementation itself (an actual definition, not just the .elisai interface's externs).
// A real `def` parses to *ast.FuncDecl; an `extern` parses to *ast.ExternFuncDecl, so this
// stays false for interface-only includes -- exactly the case that needs the standalone object.
func resultDefinesDebugReferee(result *semantic.Result) bool {
	if result == nil || result.GlobalScope == nil {
		return false
	}
	sym, ok := result.GlobalScope.Lookup("debug_referee_record")
	if !ok || sym == nil {
		return false
	}
	_, ok = sym.Node.(*ast.FuncDecl)
	return ok
}

func debugRefereeSupportPath() (string, error) {
	repoRoot, err := compilerRepoRootForNativeExec()
	if err != nil {
		return "", err
	}
	path := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "debug_referee.elisa")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("failed to locate debug referee runtime support %s: %w", path, err)
	}
	return path, nil
}

// writeDebugRefereeObject compiles debug_referee.elisa on its own into a native object. It
// depends only on language builtins (cstr, panic), so it links cleanly alongside a program's
// self-provided runtime without redefining arena_alloc and friends.
func writeDebugRefereeObject(outputPath string, packedProfile backend.PackedLoweringProfile, targetTriple string, stderr io.Writer) error {
	srcPath, err := debugRefereeSupportPath()
	if err != nil {
		return err
	}
	src, err := readSourceWithIncludes(srcPath, map[string]bool{})
	if err != nil {
		return err
	}
	var parseStderr bytes.Buffer
	file, ok := parseProgram(srcPath, src, &parseStderr)
	if !ok {
		if parseStderr.Len() != 0 && stderr != nil {
			_, _ = io.Copy(stderr, &parseStderr)
		}
		return fmt.Errorf("failed to parse debug referee runtime support")
	}
	refereeResult := semantic.Analyze(file)
	if errs := refereeResult.Errors(); len(errs) != 0 {
		if stderr != nil {
			for _, e := range errs {
				fmt.Fprintf(stderr, "%s\n", e)
			}
		}
		return fmt.Errorf("failed to analyze debug referee runtime support")
	}
	if clangPath, err := exec.LookPath("clang"); err == nil {
		if err := writeNativeObjectViaClangIR(clangPath, refereeResult, outputPath, backend.OptimizationLevel3, packedProfile, targetTriple, false, false, stderr); err == nil {
			return nil
		} else if strings.TrimSpace(targetTriple) != "" {
			return err
		}
	}
	return backend.WriteLLVMObjectFileWithOptions(refereeResult, outputPath, backend.LLVMObjectEmitOptions{
		OptLevel:      backend.OptimizationLevel3,
		PackedProfile: packedProfile,
		TargetTriple:  targetTriple,
	})
}

func runNativeExecutable(exePath string, targetTriple string, stdout io.Writer, stderr io.Writer) error {
	var runStdout bytes.Buffer
	var runStderr bytes.Buffer
	cmd := nativeExecCommand(exePath, targetTriple)
	cmd.Stdout = &runStdout
	cmd.Stderr = &runStderr
	err := cmd.Run()
	if runStdout.Len() != 0 && stdout != nil {
		_, _ = io.Copy(stdout, &runStdout)
	}
	if runStderr.Len() != 0 && stderr != nil {
		_, _ = io.Copy(stderr, &runStderr)
	}
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
		if status.Signaled() {
			return fmt.Errorf("native executable terminated with signal %s", status.Signal())
		}
		if status.Exited() {
			if stdout != nil {
				if runStdout.Len() != 0 {
					data := runStdout.Bytes()
					if data[len(data)-1] != '\n' {
						_, _ = fmt.Fprintln(stdout)
					}
				}
				_, _ = fmt.Fprintf(stdout, "[ result   ] %d\n", status.ExitStatus())
			}
			return nil
		}
	}
	if code := exitErr.ExitCode(); code >= 0 {
		if stdout != nil {
			if runStdout.Len() != 0 {
				data := runStdout.Bytes()
				if data[len(data)-1] != '\n' {
					_, _ = fmt.Fprintln(stdout)
				}
			}
			_, _ = fmt.Fprintf(stdout, "[ result   ] %d\n", code)
		}
		return nil
	}
	return err
}

func nativeHeaderNames(result *semantic.Result, outputPath string) []string {
	names := []string{"elisacore.h"}
	for _, candidate := range []string{nativeHeaderBaseName(outputPath), nativeHeaderBaseName(nativeResultFilename(result))} {
		if candidate == "" {
			continue
		}
		duplicate := false
		for _, existing := range names {
			if existing == candidate {
				duplicate = true
				break
			}
		}
		if !duplicate {
			names = append(names, candidate)
		}
	}
	return names
}

func nativeResultFilename(result *semantic.Result) string {
	if result == nil || result.File == nil {
		return ""
	}
	return result.File.Filename
}

func nativeHeaderBaseName(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	base := filepath.Base(trimmed)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		return ""
	}
	return stem + ".h"
}
