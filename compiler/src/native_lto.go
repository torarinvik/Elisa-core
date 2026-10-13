package main

// Link-time optimization for native executables (`-flto`, `-flto=thin`, project `"lto"`).
//
// Without LTO an Elisa executable is: the program module (one LLVM module, optimized
// in-process), the default runtime object (native_runtime_support.elisa, compiled
// separately at -O3) unless the program includes the runtime itself, the weak profiler-hook
// fallbacks (C), and any foreign C. Calls across those objects -- arena_alloc/arena_realloc/
// arena_free from user code into the runtime object, and the per-allocation-event profiler
// negotiation (`elisa_profile_allocation_negotiate`, a weak C stub returning 0) -- can never
// be inlined. Under LTO every one of those pieces is LLVM bitcode, and the linker's LLVM LTO
// plugin (ld64 + libLTO on macOS, lld elsewhere) optimizes and code-generates them as ONE
// unit, like Rust's `lto = "fat"` + `codegen-units = 1` or clang's `-flto`.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"elisacore/src/backend"
	"elisacore/src/semantic"
)

// nativeLinkOptions carries link-pipeline choices that are not part of code generation.
type nativeLinkOptions struct {
	LTO backend.LTOMode
}

func buildNativeExecutableWithLinkOptions(result *semantic.Result, foreignFiles []string, linkFlags []string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, link nativeLinkOptions, stderr io.Writer) (string, func(), error) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return "", func() {}, fmt.Errorf("clang is required to build native executables: %w", err)
	}
	exePath, cleanup, _, err := buildNativeExecutableWithClangOptions(clangPath, result, foreignFiles, linkFlags, outputPath, optLevel, packedProfile, targetTriple, debugInfo, traceInfo, link, stderr)
	return exePath, cleanup, err
}

// ltoClangFlag is the clang driver spelling of an LTO mode.
func ltoClangFlag(mode backend.LTOMode) string {
	if mode == backend.LTOThin {
		return "-flto=thin"
	}
	return "-flto=full"
}

// ltoOptLevelArg maps the Elisa optimization level onto clang's. LTO at -O0 still links
// bitcode (and still crosses object boundaries), it just does not optimize.
func ltoOptLevelArg(optLevel backend.OptimizationLevel) string {
	return fmt.Sprintf("-O%d", int(optLevel))
}

// writeLTOObjectFile writes the program module as an LTO object: the backend's bitcode,
// re-wrapped by `clang -c -O0 -flto[=thin] -x ir` (no further optimization; attributes kept).
// The re-wrap is load-bearing twice over: ThinLTO needs a module summary, which only clang's
// LTO bitcode writer produces; and on macOS the backend's module triple is LLVM's default
// (`arm64-apple-darwin27.0.0`), which ld64 rejects for bitcode ("unknown OS in target
// triple"), whereas clang normalizes it to the `-apple-macosxN` form ld64 expects.
func writeLTOObjectFile(result *semantic.Result, objectPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, mode backend.LTOMode, stderr io.Writer) error {
	bitcodePath := objectPath + ".module.bc"
	defer os.Remove(bitcodePath)
	perfWarnings, err := backend.WriteLLVMLTOBitcodeFile(result, bitcodePath, backend.LLVMLTOBitcodeEmitOptions{
		OptLevel:      optLevel,
		PackedProfile: packedProfile,
		TargetTriple:  targetTriple,
		DebugInfo:     debugInfo,
		Trace:         traceInfo,
	})
	if err != nil {
		return err
	}
	for _, w := range perfWarnings {
		fmt.Fprintln(stderr, w)
	}
	// Same -Wperf gate as the textual-IR object path (writeNativeObjectViaClangIR).
	if result.EnforcePerfLints && len(perfWarnings) > 0 {
		return fmt.Errorf("-Wperf: %d loop(s) expected to vectorize stayed scalar; wrap intentional scalar loops in a `can Scalar:` block", len(perfWarnings))
	}
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return fmt.Errorf("clang is required for -flto: %w", err)
	}
	args := append([]string{}, targetClangArgs(targetTriple)...)
	args = append(args, "-c", "-O0", "-Wno-override-module", ltoClangFlag(mode), "-x", "ir", bitcodePath, "-o", objectPath)
	cmd := exec.Command(clangPath, args...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write LTO object: %w", err)
	}
	return nil
}

// compileCForLTO compiles a C source (a file, or inline text when source != "") to an LTO
// object with the given clang flags.
func compileCForLTO(clangPath string, sourcePath string, outputPath string, optLevel backend.OptimizationLevel, mode backend.LTOMode, targetTriple string, stderr io.Writer) error {
	args := append([]string{}, targetClangArgs(targetTriple)...)
	args = append(args, ltoClangFlag(mode), ltoOptLevelArg(optLevel))
	if cpu := ltoClangCPUArg(targetTriple); cpu != "" {
		args = append(args, cpu)
	}
	args = append(args, "-c", sourcePath, "-o", outputPath)
	cmd := exec.Command(clangPath, args...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to compile %s for LTO: %w", filepath.Base(sourcePath), err)
	}
	return nil
}

// ltoClangCPUArg passes the Elisa target-CPU choice to clang-compiled C so the C bitcode
// carries the same "target-cpu" the Elisa bitcode is stamped with (mixed target-cpu
// attributes block inlining across them).
func ltoClangCPUArg(targetTriple string) string {
	cpu := resolvedNativeTargetCPU(targetTriple)
	if cpu == "" {
		return ""
	}
	return "-mcpu=" + cpu
}

// resolvedNativeTargetCPU is the CPU code generation targets: an explicit `-mcpu`, else
// `native` for a host build (what the in-process target machine does), else "" (generic,
// for an explicit cross triple).
func resolvedNativeTargetCPU(targetTriple string) string {
	if cpu := backend.RequestedTargetCPU(); cpu != "" {
		return cpu
	}
	if strings.TrimSpace(targetTriple) == "" {
		return "native"
	}
	return ""
}

// writeProfilerFallbackLTOObject compiles the weak profiler hooks to bitcode. Under LTO the
// linker resolves the weak definition as prevailing, so the per-allocation negotiation call
// in the runtime folds to its constant 0 and the event branch disappears.
func writeProfilerFallbackLTOObject(clangPath string, outputPath string, optLevel backend.OptimizationLevel, mode backend.LTOMode, targetTriple string, stderr io.Writer) error {
	sourcePath := outputPath + ".profiler.c"
	defer os.Remove(sourcePath)
	if err := os.WriteFile(sourcePath, []byte(defaultElisaCoreProfilerFallbackSource), 0o644); err != nil {
		return fmt.Errorf("failed to write default profiler fallback: %w", err)
	}
	if optLevel == backend.OptimizationLevel0 {
		optLevel = backend.OptimizationLevel2
	}
	return compileCForLTO(clangPath, sourcePath, outputPath, optLevel, mode, targetTriple, stderr)
}

// writeDefaultElisaCoreRuntimeLTOObject emits the default runtime support module as LTO
// bitcode (always -O3, like the native runtime object), served from the runtime-object cache
// under a key that also covers the LTO mode and target CPU. The profiler fallback is NOT
// merged in (bitcode cannot be `ld -r`-merged with a native object); the caller links it as
// its own LTO object.
func writeDefaultElisaCoreRuntimeLTOObject(outputPath string, packedProfile backend.PackedLoweringProfile, targetTriple string, mode backend.LTOMode, stderr io.Writer) error {
	compile := func(path string) error {
		return compileDefaultElisaCoreRuntimeLTOObject(path, packedProfile, targetTriple, mode, stderr)
	}
	if !testRunnerCacheEnabled() || !runtimeObjectCacheEnabled() {
		return compile(outputPath)
	}
	artifact, err := runtimeObjectCacheArtifactFor(packedProfile, targetTriple)
	if err != nil {
		return compile(outputPath)
	}
	hash := sha256.New()
	testRunnerCacheWriteString(hash, "runtime-lto-object-v1")
	testRunnerCacheWriteString(hash, "base="+artifact.key)
	testRunnerCacheWriteString(hash, "lto="+string(mode))
	testRunnerCacheWriteString(hash, "cpu="+resolvedNativeTargetCPU(targetTriple))
	artifact.key = hex.EncodeToString(hash.Sum(nil))
	artifact.dir = filepath.Join(filepath.Dir(artifact.dir), artifact.key)
	artifact.object = filepath.Join(artifact.dir, "elisacore_runtime.lto.o")
	if usableCachedFile(artifact.object, false) {
		if copyErr := copyExecutableFile(artifact.object, outputPath); copyErr == nil {
			debugRuntimeObjectCache(stderr, "hit", artifact)
			return nil
		}
	}
	debugRuntimeObjectCache(stderr, "miss", artifact)
	if err := compile(outputPath); err != nil {
		return err
	}
	if pubErr := publishCachedRuntimeObject(artifact, outputPath); pubErr != nil {
		debugRuntimeObjectCache(stderr, "publish-error", artifact)
	} else {
		debugRuntimeObjectCache(stderr, "publish", artifact)
	}
	return nil
}

func compileDefaultElisaCoreRuntimeLTOObject(outputPath string, packedProfile backend.PackedLoweringProfile, targetTriple string, mode backend.LTOMode, stderr io.Writer) error {
	runtimeResult, err := analyzeDefaultElisaCoreRuntime(stderr)
	if err != nil {
		return err
	}
	return writeLTOObjectFile(runtimeResult, outputPath, backend.OptimizationLevel3, packedProfile, targetTriple, false, false, mode, stderr)
}

// ltoLinkArgs are the clang-driver arguments that turn a link into an LTO link.
//
//   - `-flto[=thin]` + `-O<n>` select the LTO pipeline and its level.
//   - macOS: Homebrew clang hands ld64 its own libLTO (`-lto_library`), which must match the
//     LLVM that wrote the bitcode; libLTO defaults to O2 unless told otherwise, so the level
//     is passed through `-mllvm`.
//   - Linux/other ELF: GNU ld needs the LLVMgold plugin, which is often missing; prefer lld
//     when it is installed and the project did not choose a linker. clang forwards -O and
//     -mcpu to lld as --plugin-opt itself.
func ltoLinkArgs(mode backend.LTOMode, optLevel backend.OptimizationLevel, targetTriple string, linkFlags []string) []string {
	args := []string{ltoClangFlag(mode), ltoOptLevelArg(optLevel)}
	if cpu := ltoClangCPUArg(targetTriple); cpu != "" {
		args = append(args, cpu)
	}
	if isMachOTriple(targetTriple) {
		args = append(args, "-Wl,-mllvm,"+ltoOptLevelArg(optLevel))
		return args
	}
	if runtime.GOOS != "windows" && !linkFlagsChooseLinker(linkFlags) {
		if _, err := exec.LookPath("ld.lld"); err == nil {
			args = append(args, "-fuse-ld=lld")
		}
	}
	return args
}

func linkFlagsChooseLinker(linkFlags []string) bool {
	for _, flag := range linkFlags {
		if strings.HasPrefix(flag, "-fuse-ld") {
			return true
		}
	}
	return false
}
