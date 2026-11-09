package main

import (
	"elisacore/src/backend"
	"elisacore/src/easm"
	"elisacore/src/semantic"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type cArchiveManifest struct {
	Source            string   `json:"source"`
	Archive           string   `json:"archive"`
	TargetTriple      string   `json:"target_triple,omitempty"`
	Objects           []string `json:"objects"`
	RuntimeIncluded   bool     `json:"runtime_included"`
	RuntimeObject     string   `json:"runtime_object,omitempty"`
	HeaderAudit       string   `json:"header_audit"`
	UnsafeReport      string   `json:"unsafe_report"`
	ExportedFunctions []string `json:"exported_functions"`
	ExportedGlobals   []string `json:"exported_globals"`
	ExportedTypes     []string `json:"exported_types"`
	GeneratedBy       string   `json:"generated_by"`
	ABIContract       string   `json:"abi_contract"`
}

// processStartTime anchors build-timing measurements (ELISACORE_BUILD_TIMING) so the
// front-end share (parse + whole-program analysis + program load) can be read off as the
// time from process start to the first native build.
var processStartTime = time.Now()

type nativeBuildTiming struct {
	UncacheableSMTOutcome bool
	ObjectWrite           time.Duration
	HeaderGen             time.Duration
	HeaderWrite           time.Duration
	Link                  time.Duration
	CacheLookup           time.Duration
	CachePublish          time.Duration
	CacheHit              bool
}

func buildNativeExecutable(result *semantic.Result, foreignFiles []string, linkFlags []string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, stderr io.Writer) (string, func(), error) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return "", func() {}, fmt.Errorf("clang is required to build native executables: %w", err)
	}
	exePath, cleanup, _, err := buildNativeExecutableWithClang(clangPath, result, foreignFiles, linkFlags, outputPath, optLevel, packedProfile, targetTriple, debugInfo, traceInfo, stderr)
	return exePath, cleanup, err
}

func buildCArchive(result *semantic.Result, sourcePath string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, stderr io.Writer) error {
	if result == nil {
		return fmt.Errorf("semantic result is nil")
	}
	archivePath := outputPathForEmit(sourcePath, outputPath, ".a")
	if err := ensureOutputParentExists(archivePath); err != nil {
		return err
	}
	// The unsafe-permission audit GATES the emission, so it runs before anything is written.
	// It used to run after `ar` had already produced the archive: a REJECTED program still
	// left a complete, linkable .a (and .h) on disk, so the exit code said "failed" while the
	// artifact said "success". Anything that checks for the file rather than the exit status --
	// an incremental build, a test harness -- would link and RUN a program the compiler had
	// refused (verified: an unguarded `xs[0]` audit-failed, yet its archive linked and returned
	// 42). Fail first, write nothing.
	unsafeResult := semantic.AnalyzeWithOptions(result.File, semantic.AnalyzeOptions{
		EnforceUnsafePermissions: true,
		TargetTriple:             targetTriple,
		TargetDebug:              optLevel == backend.OptimizationLevel0,
	})
	if errs := unsafeResult.Errors(); len(errs) != 0 {
		for _, e := range errs {
			if stderr != nil {
				fmt.Fprintf(stderr, "%s\n", e)
			}
		}
		return fmt.Errorf("unsafe permission audit failed")
	}
	tempDir, err := os.MkdirTemp("", "elisacore-c-archive-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	moduleObjectPath := filepath.Join(tempDir, "elisacore_module.o")
	objectOptions := backend.LLVMObjectEmitOptions{
		OptLevel:      optLevel,
		PackedProfile: packedProfile,
		TargetTriple:  targetTriple,
	}
	if err := backend.WriteLLVMObjectFileWithOptions(result, moduleObjectPath, objectOptions); err != nil {
		return err
	}

	objectPaths := []string{moduleObjectPath}
	manifestObjects := []string{"elisacore_module.o"}
	runtimeIncluded := false
	runtimeObjectPath := ""
	if !resultDefinesDefaultElisaCoreRuntime(result) {
		runtimeObjectPath = filepath.Join(tempDir, "elisacore_runtime.o")
		if err := writeDefaultElisaCoreRuntimeObject(runtimeObjectPath, packedProfile, targetTriple, stderr); err != nil {
			return err
		}
		objectPaths = append(objectPaths, runtimeObjectPath)
		manifestObjects = append(manifestObjects, "elisacore_runtime.o")
		runtimeIncluded = true
	} else {
		// A source-provided runtime still contains the optional profiler wrappers,
		// so archives that own their runtime must carry the weak callbacks too.
		profileFallbackPath := filepath.Join(tempDir, "elisacore_profile_hooks.o")
		if err := writeProfilerFallbackObject(profileFallbackPath, targetTriple, stderr); err != nil {
			return err
		}
		objectPaths = append(objectPaths, profileFallbackPath)
		manifestObjects = append(manifestObjects, "elisacore_profile_hooks.o")
	}

	arPath, err := exec.LookPath("ar")
	if err != nil {
		return fmt.Errorf("ar is required to build C ABI archives: %w", err)
	}
	args := append([]string{"rcs", archivePath}, objectPaths...)
	cmd := exec.Command(arPath, args...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build C ABI archive: %w", err)
	}

	base := cArchiveSidecarBase(archivePath)
	headerPath := base + ".h"
	headerSource, err := backend.GenerateCHeader(result)
	if err != nil {
		return err
	}
	if err := writeOutputFile(headerPath, []byte(headerSource)); err != nil {
		return err
	}

	// The audit itself ran up front (it gates emission); only its REPORT is an artifact, so
	// that is all that happens here.
	unsafePath := base + ".unsafe.txt"
	unsafeReport := generateUnsafeReport(unsafeResult)
	if err := writeOutputFile(unsafePath, []byte(unsafeReport)); err != nil {
		return err
	}

	manifestPath := base + ".elisa-abi.json"
	manifest := cArchiveManifest{
		Source:            sourcePath,
		Archive:           archivePath,
		TargetTriple:      strings.TrimSpace(targetTriple),
		Objects:           manifestObjects,
		RuntimeIncluded:   runtimeIncluded,
		RuntimeObject:     "elisacore_runtime.o",
		HeaderAudit:       headerPath,
		UnsafeReport:      unsafePath,
		ExportedFunctions: exportedFunctionNames(result),
		ExportedGlobals:   exportedGlobalNames(result),
		ExportedTypes:     exportedTypeNames(result),
		GeneratedBy:       "elisacore -emit c-archive",
		ABIContract:       "checked-in C/C++ headers are the build contract; generated headers are audit artifacts",
	}
	if !runtimeIncluded {
		manifest.RuntimeObject = ""
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeOutputFile(manifestPath, append(payload, '\n'))
}

func cArchiveSidecarBase(archivePath string) string {
	if strings.EqualFold(filepath.Ext(archivePath), ".a") {
		return strings.TrimSuffix(archivePath, filepath.Ext(archivePath))
	}
	return archivePath
}

func exportedFunctionNames(result *semantic.Result) []string {
	if result == nil {
		return nil
	}
	names := make([]string, 0, len(result.ExportedFuncs))
	for _, exported := range result.ExportedFuncs {
		if exported != nil {
			names = append(names, exported.PublicName)
		}
	}
	return names
}

func exportedGlobalNames(result *semantic.Result) []string {
	if result == nil {
		return nil
	}
	names := make([]string, 0, len(result.ExportedGlobals))
	for _, exported := range result.ExportedGlobals {
		if exported != nil {
			names = append(names, exported.PublicName)
		}
	}
	return names
}

func exportedTypeNames(result *semantic.Result) []string {
	if result == nil {
		return nil
	}
	names := make([]string, 0, len(result.ExportedTypes))
	for _, exported := range result.ExportedTypes {
		if exported != nil {
			names = append(names, exported.PublicName)
		}
	}
	return names
}

func buildNativeExecutableWithClang(clangPath string, result *semantic.Result, foreignFiles []string, linkFlags []string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, stderr io.Writer) (string, func(), nativeBuildTiming, error) {
	return buildNativeExecutableWithClangOptions(clangPath, result, foreignFiles, linkFlags, outputPath, optLevel, packedProfile, targetTriple, debugInfo, traceInfo, nativeLinkOptions{}, stderr)
}

func buildNativeExecutableWithClangOptions(clangPath string, result *semantic.Result, foreignFiles []string, linkFlags []string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, link nativeLinkOptions, stderr io.Writer) (string, func(), nativeBuildTiming, error) {
	lto := link.LTO != backend.LTOOff
	if result == nil {
		return "", func() {}, nativeBuildTiming{}, fmt.Errorf("semantic result is nil")
	}
	if os.Getenv("ELISACORE_BUILD_TIMING") != "" {
		fmt.Fprintf(stderr, "[build-timing] front-end (process start -> native build): %v\n", time.Since(processStartTime).Round(time.Millisecond))
	}
	resolvedForeignFiles, err := withDefaultNativeRuntimeForeignFiles(foreignFiles)
	if err != nil {
		return "", func() {}, nativeBuildTiming{}, err
	}
	foreignFiles = resolvedForeignFiles
	tempDir, err := os.MkdirTemp("", "elisacore-native-run-*")
	if err != nil {
		return "", func() {}, nativeBuildTiming{}, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}
	timing := nativeBuildTiming{}

	exePath := strings.TrimSpace(outputPath)
	if exePath == "" {
		exePath = filepath.Join(tempDir, "elisacore_program")
	} else if err := ensureOutputParentExists(exePath); err != nil {
		cleanup()
		return "", func() {}, timing, err
	}

	objectPath := filepath.Join(tempDir, "elisacore_module.o")
	objectStart := time.Now()
	easmModules := append([]*easm.Module(nil), result.EASMModules...)
	result.EASMModules = nil
	var objectErr error
	if lto {
		objectErr = writeLTOObjectFile(result, objectPath, optLevel, packedProfile, targetTriple, debugInfo, traceInfo, link.LTO, stderr)
	} else {
		objectErr = writeNativeObjectViaClangIR(clangPath, result, objectPath, optLevel, packedProfile, targetTriple, debugInfo, traceInfo, stderr)
	}
	if err := objectErr; err != nil {
		result.EASMModules = easmModules
		cleanup()
		return "", func() {}, timing, err
	}
	result.EASMModules = easmModules
	timing.ObjectWrite = time.Since(objectStart)
	runtimeObjectPath := ""
	profilerFallbackObjectPath := ""
	debugRefereeObjectPath := ""
	selfProvidesRuntime := resultDefinesDefaultElisaCoreRuntime(result)
	if !selfProvidesRuntime {
		runtimeObjectPath = filepath.Join(tempDir, "elisacore_runtime.o")
		if lto {
			// Bitcode runtime + bitcode weak profiler hooks (linked separately below).
			if err := writeDefaultElisaCoreRuntimeLTOObject(runtimeObjectPath, packedProfile, targetTriple, link.LTO, stderr); err != nil {
				cleanup()
				return "", func() {}, timing, err
			}
			profilerFallbackObjectPath = filepath.Join(tempDir, "elisacore_profiler_fallback.o")
			if err := writeProfilerFallbackLTOObject(clangPath, profilerFallbackObjectPath, optLevel, link.LTO, targetTriple, stderr); err != nil {
				cleanup()
				return "", func() {}, timing, err
			}
		} else if err := writeDefaultElisaCoreRuntimeObject(runtimeObjectPath, packedProfile, targetTriple, stderr); err != nil {
			cleanup()
			return "", func() {}, timing, err
		}
	} else {
		// A source-provided runtime still includes the optional profiler wrappers.
		// Supply the weak callbacks at the native executable link boundary as well;
		// otherwise an uninstrumented program would call an unresolved optional hook
		// through a null address. Strong host collectors can override these symbols.
		profilerFallbackObjectPath = filepath.Join(tempDir, "elisacore_profile_hooks.o")
		if err := writeProfilerFallbackObject(profilerFallbackObjectPath, targetTriple, stderr); err != nil {
			cleanup()
			return "", func() {}, timing, err
		}
		if !resultDefinesDebugReferee(result) {
			// The program self-provides the core runtime, so the default runtime object -- which
			// also carries debug_referee.elisa -- is skipped above. When the program still
			// references the debug referee (e.g. via debug_referee.elisai's externs) without
			// defining it, link a standalone debug_referee object so its trace globals/functions
			// resolve instead of failing at load ("symbol not found in flat namespace"). The
			// guard skips programs that already define the referee, avoiding duplicate symbols.
			debugRefereeObjectPath = filepath.Join(tempDir, "elisacore_debug_referee.o")
			if err := writeDebugRefereeObject(debugRefereeObjectPath, packedProfile, targetTriple, stderr); err != nil {
				cleanup()
				return "", func() {}, timing, err
			}
		}
	}
	if selfProvidesRuntime {
		// A source that includes the runtime supplies arena_profile_* itself and therefore
		// intentionally skips the auto-linked runtime object above. It still needs the
		// optional hook fallback; otherwise macOS's dynamic lookup leaves the negotiation
		// function at NULL and the first arena allocation jumps through address zero.
		profilerFallbackObjectPath = filepath.Join(tempDir, "elisacore_profiler_fallback.o")
		writeFallback := func() error {
			return writeElisaCoreProfilerFallbackObject(clangPath, profilerFallbackObjectPath, targetTriple, stderr)
		}
		if lto {
			writeFallback = func() error {
				return writeProfilerFallbackLTOObject(clangPath, profilerFallbackObjectPath, optLevel, link.LTO, targetTriple, stderr)
			}
		}
		if err := writeFallback(); err != nil {
			cleanup()
			return "", func() {}, timing, err
		}
	}
	headerGenStart := time.Now()
	headerSource, err := backend.GenerateCHeader(result)
	if err != nil {
		cleanup()
		return "", func() {}, timing, err
	}
	timing.HeaderGen = time.Since(headerGenStart)
	headerWriteStart := time.Now()
	for _, name := range nativeHeaderNames(result, exePath) {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte(headerSource), 0o644); err != nil {
			cleanup()
			return "", func() {}, timing, err
		}
	}
	timing.HeaderWrite = time.Since(headerWriteStart)
	if shimSource := generatedCOpaqueRuntimeShimSource(result); strings.TrimSpace(shimSource) != "" {
		shimPath := filepath.Join(tempDir, "elisacore_c_opaque_runtime.c")
		if err := os.WriteFile(shimPath, []byte(shimSource), 0o644); err != nil {
			cleanup()
			return "", func() {}, timing, err
		}
		foreignFiles = append([]string{shimPath}, foreignFiles...)
	}
	if shimSource := generatedNativeCallbackRuntimeShimSource(result); strings.TrimSpace(shimSource) != "" {
		shimPath := filepath.Join(tempDir, "elisacore_native_callbacks.c")
		if err := os.WriteFile(shimPath, []byte(shimSource), 0o644); err != nil {
			cleanup()
			return "", func() {}, timing, err
		}
		foreignFiles = append([]string{shimPath}, foreignFiles...)
	}
	if len(easmModules) != 0 {
		easmPath := filepath.Join(tempDir, "elisacore_easm.s")
		easmSource, err := nativeEASMAssemblySource(easmModules, targetTriple)
		if err != nil {
			cleanup()
			return "", func() {}, timing, err
		}
		if strings.TrimSpace(easmSource) != "" {
			if err := os.WriteFile(easmPath, []byte(easmSource), 0o644); err != nil {
				cleanup()
				return "", func() {}, timing, err
			}
			foreignFiles = append([]string{easmPath}, foreignFiles...)
		}
	}
	foreignStart := time.Now()
	foreignFiles, linkFlags, err = compileCxxForeignFiles(clangPath, foreignFiles, linkFlags, tempDir, targetTriple, stderr)
	if os.Getenv("ELISACORE_BUILD_TIMING") != "" {
		fmt.Fprintf(stderr, "[build-timing] foreign C/C++ compile: %v\n", time.Since(foreignStart).Round(time.Millisecond))
	}
	if err != nil {
		cleanup()
		return "", func() {}, timing, err
	}

	linkArgs := make([]string, 0, 7+len(foreignFiles))
	linkArgs = append(linkArgs, targetClangArgs(targetTriple)...)
	linkArgs = append(linkArgs, targetLinkerArgs(targetTriple)...)
	if lto {
		// Foreign .c files on this command line are compiled by this same clang invocation,
		// so they become bitcode too and take part in the LTO unit.
		linkArgs = append(linkArgs, ltoLinkArgs(link.LTO, optLevel, targetTriple, linkFlags)...)
	}
	linkArgs = append(linkArgs, "-I", tempDir)
	if nativeExecutableNeedsPThread(foreignFiles) && runtime.GOOS != "windows" {
		linkArgs = append(linkArgs, "-pthread")
	}
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-undefined,dynamic_lookup")
	}
	if runtime.GOOS == "linux" {
		// Elisa codegen emits objects with absolute (non-PIC) relocations; Ubuntu
		// clang/ld default to -pie and reject them, so link as a non-PIE.
		linkArgs = append(linkArgs, "-no-pie")
	}
	linkArgs = append(linkArgs, objectPath)
	if runtimeObjectPath != "" {
		linkArgs = append(linkArgs, runtimeObjectPath)
	}
	if debugRefereeObjectPath != "" {
		linkArgs = append(linkArgs, debugRefereeObjectPath)
	}
	if profilerFallbackObjectPath != "" {
		linkArgs = append(linkArgs, profilerFallbackObjectPath)
	}
	linkArgs = append(linkArgs, foreignFiles...)
	linkArgs = append(linkArgs, linkFlags...)
	if resultNeedsLLVMCAPILinkage(result) {
		linkArgs = appendMissingLinkFlags(linkArgs, llvmNativeLinkFlags(linkFlags)...)
	}
	linkArgs = append(linkArgs, "-o", exePath)

	linkCmd := exec.Command(clangPath, linkArgs...)
	linkCmd.Stdout = stderr
	linkCmd.Stderr = stderr
	linkStart := time.Now()
	if err := linkCmd.Run(); err != nil {
		cleanup()
		return "", func() {}, timing, fmt.Errorf("failed to link native executable: %w", err)
	}
	timing.Link = time.Since(linkStart)
	if os.Getenv("ELISACORE_BUILD_TIMING") != "" {
		fmt.Fprintf(stderr, "[build-timing] link: %v | object-write(IR-gen+llc): %v\n", timing.Link.Round(time.Millisecond), timing.ObjectWrite.Round(time.Millisecond))
	}

	// Safety gate: catch the null-bind symbol-split class (the CUSA07399 boot
	// SIGSEGV root cause) — an undefined `_sym.N` produced when two declarations of the
	// same link symbol get LLVM-renamed; under -undefined,dynamic_lookup the duplicate
	// flat-namespace stub binds to NULL and crashes on first call. Best-effort, darwin
	// only. Missing Elisa runtime helpers are equally certain to bind to NULL. Skip
	// explicitly with ELISACORE_NO_LINK_BINDING_CHECK=1 when overriding this safety gate.
	if isMachOTriple(targetTriple) && os.Getenv("ELISACORE_NO_LINK_BINDING_CHECK") == "" {
		if err := rejectNullBoundSymbols(exePath); err != nil {
			cleanup()
			return "", func() {}, timing, err
		}
	}

	// On Mach-O targets DWARF stays in the .o files (a "debug map" in the executable),
	// so a debugger needs either those .o files or a .dSYM bundle. The temp .o files are
	// about to be cleaned up, so when -g is requested, package the DWARF into <exe>.dSYM
	// now (best-effort) so `lldb <exe>` resolves source lines, locals and our pretty-printers.
	if debugInfo && isMachOTriple(targetTriple) {
		if dsymPath, lookErr := exec.LookPath("dsymutil"); lookErr == nil {
			dsymCmd := exec.Command(dsymPath, exePath)
			dsymCmd.Stdout = stderr
			dsymCmd.Stderr = stderr
			_ = dsymCmd.Run()
		}
	}
	return exePath, cleanup, timing, nil
}
