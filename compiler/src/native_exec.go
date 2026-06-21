package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/easm"
	"elisacore/src/semantic"
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
	ObjectWrite  time.Duration
	HeaderGen    time.Duration
	HeaderWrite  time.Duration
	Link         time.Duration
	CacheLookup  time.Duration
	CachePublish time.Duration
	CacheHit     bool
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

	unsafePath := base + ".unsafe.txt"
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
	if err := writeNativeObjectViaClangIR(clangPath, result, objectPath, optLevel, packedProfile, targetTriple, debugInfo, traceInfo, stderr); err != nil {
		result.EASMModules = easmModules
		cleanup()
		return "", func() {}, timing, err
	}
	result.EASMModules = easmModules
	timing.ObjectWrite = time.Since(objectStart)
	runtimeObjectPath := ""
	debugRefereeObjectPath := ""
	if !resultDefinesDefaultElisaCoreRuntime(result) {
		runtimeObjectPath = filepath.Join(tempDir, "elisacore_runtime.o")
		if err := writeDefaultElisaCoreRuntimeObject(runtimeObjectPath, packedProfile, targetTriple, stderr); err != nil {
			cleanup()
			return "", func() {}, timing, err
		}
	} else if !resultDefinesDebugReferee(result) {
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
	linkArgs = append(linkArgs, "-I", tempDir)
	if nativeExecutableNeedsPThread(foreignFiles) && runtime.GOOS != "windows" {
		linkArgs = append(linkArgs, "-pthread")
	}
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-undefined,dynamic_lookup")
	}
	linkArgs = append(linkArgs, objectPath)
	if runtimeObjectPath != "" {
		linkArgs = append(linkArgs, runtimeObjectPath)
	}
	if debugRefereeObjectPath != "" {
		linkArgs = append(linkArgs, debugRefereeObjectPath)
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

	// Safety diagnostic: catch the null-bind symbol-split class (the CUSA07399 boot
	// SIGSEGV root cause) — an undefined `_sym.N` produced when two declarations of the
	// same link symbol get LLVM-renamed; under -undefined,dynamic_lookup the duplicate
	// flat-namespace stub binds to NULL and crashes on first call. Best-effort, darwin
	// only, warning-only; skip with ELISACORE_NO_LINK_BINDING_CHECK=1.
	if isMachOTriple(targetTriple) && os.Getenv("ELISACORE_NO_LINK_BINDING_CHECK") == "" {
		warnOnSplitNullBoundSymbols(exePath, stderr)
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

// warnOnSplitNullBoundSymbols inspects a freshly-linked Mach-O for the symbol-split
// null-bind hazard: an undefined external `_sym.N` (LLVM's dedup rename) that exists
// alongside the base `_sym`. Under `-Wl,-undefined,dynamic_lookup` the `.N` flat-
// namespace stub resolves to NULL, so the first call through it crashes (this was the
// CUSA07399 boot SIGSEGV: `_mprotect.1`). Best-effort and warning-only.
func warnOnSplitNullBoundSymbols(exePath string, stderr io.Writer) {
	nmPath, err := exec.LookPath("nm")
	if err != nil {
		return
	}
	out, err := exec.Command(nmPath, "-m", exePath).Output()
	if err != nil {
		return
	}
	split := findSplitNullBoundSymbols(string(out))
	if len(split) != 0 {
		fmt.Fprintf(stderr, "warning: linked binary has %d undefined split-symbol(s) that may bind to NULL under dynamic_lookup (symbol-split null-bind hazard; e.g. duplicate externs sharing a link_name): %s\n", len(split), strings.Join(split, ", "))
		fmt.Fprintf(stderr, "         this is the CUSA07399-class fault; consolidate the duplicate declaration(s). Suppress with ELISACORE_NO_LINK_BINDING_CHECK=1.\n")
	}
	helpers := findNullBoundRuntimeHelperSymbols(string(out))
	if len(helpers) != 0 {
		fmt.Fprintf(stderr, "warning: linked binary has %d undefined elisa runtime helper symbol(s) that will bind to NULL under dynamic_lookup and segfault on first call: %s\n", len(helpers), strings.Join(helpers, ", "))
		fmt.Fprintf(stderr, "         the default runtime object likely kept them private (isDefaultNativeRuntimeSupportExport whitelist) -- add them there, or include the std runtime. Suppress with ELISACORE_NO_LINK_BINDING_CHECK=1.\n")
	}
}

// findNullBoundRuntimeHelperSymbols extracts dynamically-looked-up undefined symbols that
// match the elisa runtime helper namespaces (ctx_*, arena_*, packed_store_*). Such a symbol
// has no definition anywhere -- libSystem never provides them -- so under
// -Wl,-undefined,dynamic_lookup it resolves to NULL and the first call through it crashes.
// This is exactly the stripped-runtime-helper class (e.g. ctx_aos_store_new missing from the
// default runtime export whitelist).
func findNullBoundRuntimeHelperSymbols(nmOutput string) []string {
	re := regexp.MustCompile(`\(undefined\)[^\n]*\b(_(?:ctx|arena|packed_store)_[A-Za-z0-9_]+)\b[^\n]*dynamically looked up`)
	seen := map[string]bool{}
	var helpers []string
	for _, m := range re.FindAllStringSubmatch(nmOutput, -1) {
		if sym := m[1]; !seen[sym] {
			seen[sym] = true
			helpers = append(helpers, sym)
		}
	}
	sort.Strings(helpers)
	return helpers
}

// findSplitNullBoundSymbols extracts undefined external symbols carrying a `.<digits>`
// LLVM dedup suffix (e.g. `_mprotect.1`) from `nm -m` output. Such a symbol is the
// flat-namespace duplicate that binds to NULL under -undefined,dynamic_lookup.
func findSplitNullBoundSymbols(nmOutput string) []string {
	re := regexp.MustCompile(`\(undefined\)[^\n]*\b(_[A-Za-z0-9_$]+\.\d+)\b`)
	seen := map[string]bool{}
	var split []string
	for _, m := range re.FindAllStringSubmatch(nmOutput, -1) {
		if sym := m[1]; !seen[sym] {
			seen[sym] = true
			split = append(split, sym)
		}
	}
	return split
}

// isMachOTriple reports whether the target produces Mach-O (Apple) binaries, where DWARF
// lives in a debug map / .dSYM rather than embedded in the executable. An empty triple
// means the native host.
func isMachOTriple(targetTriple string) bool {
	t := strings.ToLower(strings.TrimSpace(targetTriple))
	if t == "" {
		return runtime.GOOS == "darwin"
	}
	return strings.Contains(t, "apple") || strings.Contains(t, "darwin") || strings.Contains(t, "macos")
}

func compileCxxForeignFiles(clangPath string, foreignFiles []string, linkFlags []string, tempDir string, targetTriple string, stderr io.Writer) ([]string, []string, error) {
	cxxFlags, filteredLinkFlags := cxxCompileAndFilteredLinkFlags(linkFlags)
	if len(cxxFlags) == 0 {
		return foreignFiles, linkFlags, nil
	}
	compiledForeign := make([]string, 0, len(foreignFiles))
	for index, foreignFile := range foreignFiles {
		if !isCxxForeignFile(foreignFile) {
			compiledForeign = append(compiledForeign, foreignFile)
			continue
		}
		objectPath := filepath.Join(tempDir, fmt.Sprintf("foreign_cxx_%d.o", index))
		compileArgs := append([]string{}, targetClangArgs(targetTriple)...)
		compileArgs = append(compileArgs, "-c", foreignFile, "-o", objectPath)
		compileArgs = append(compileArgs, cxxFlags...)
		compileCmd := exec.Command(clangPath, compileArgs...)
		compileCmd.Stdout = stderr
		compileCmd.Stderr = stderr
		if err := compileCmd.Run(); err != nil {
			return nil, nil, fmt.Errorf("failed to compile C++ foreign source %s: %w", foreignFile, err)
		}
		compiledForeign = append(compiledForeign, objectPath)
	}
	return compiledForeign, filteredLinkFlags, nil
}

func isCxxForeignFile(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".cc", ".cpp", ".cxx", ".c++":
		return true
	default:
		return false
	}
}

func cxxCompileAndFilteredLinkFlags(linkFlags []string) ([]string, []string) {
	cxxFlags := []string{}
	filtered := make([]string, 0, len(linkFlags))
	for index := 0; index < len(linkFlags); index++ {
		flag := linkFlags[index]
		switch {
		case strings.HasPrefix(flag, "-std=c++"):
			cxxFlags = append(cxxFlags, flag)
		case flag == "-I" || flag == "-isystem" || flag == "-iquote":
			filtered = append(filtered, flag)
			if index+1 < len(linkFlags) {
				index++
				filtered = append(filtered, linkFlags[index])
				cxxFlags = append(cxxFlags, flag, linkFlags[index])
			}
		case strings.HasPrefix(flag, "-I") || strings.HasPrefix(flag, "-D") ||
			strings.HasPrefix(flag, "-U") || strings.HasPrefix(flag, "-isystem") ||
			strings.HasPrefix(flag, "-iquote"):
			filtered = append(filtered, flag)
			cxxFlags = append(cxxFlags, flag)
		default:
			filtered = append(filtered, flag)
		}
	}
	return cxxFlags, filtered
}

func nativeEASMAssemblySource(modules []*easm.Module, targetTriple string) (string, error) {
	var out strings.Builder
	out.WriteString(".text\n")
	prefix := nativeEASMSymbolPrefix(targetTriple)
	for _, module := range modules {
		if module == nil {
			continue
		}
		if !nativeEASMModuleMatchesTarget(module, targetTriple) {
			continue
		}
		for i := range module.Functions {
			fn := &module.Functions[i]
			name := strings.TrimSpace(fn.Name)
			if name == "" {
				continue
			}
			symbol := prefix + name
			out.WriteString("\n.globl ")
			out.WriteString(symbol)
			out.WriteString("\n")
			out.WriteString(symbol)
			out.WriteString(":\n")
			for _, inst := range fn.Instructions {
				if inst.Pseudo {
					continue
				}
				text := strings.TrimSpace(inst.Text)
				if text == "" {
					continue
				}
				if strings.EqualFold(text, "trap") {
					text = "int3"
				}
				out.WriteString("\t")
				out.WriteString(text)
				out.WriteString("\n")
			}
		}
	}
	return out.String(), nil
}

func nativeEASMModuleMatchesTarget(module *easm.Module, targetTriple string) bool {
	moduleTarget := strings.ToLower(strings.TrimSpace(module.Target))
	if moduleTarget == "" {
		return true
	}
	target := strings.ToLower(strings.TrimSpace(targetTriple))
	if target == "" {
		target = runtime.GOARCH
	}
	switch moduleTarget {
	case "x86_64", "amd64":
		return strings.Contains(target, "x86_64") || strings.Contains(target, "amd64")
	case "aarch64", "arm64":
		return strings.Contains(target, "aarch64") || strings.Contains(target, "arm64")
	default:
		return strings.Contains(target, moduleTarget)
	}
}

func nativeEASMSymbolPrefix(targetTriple string) string {
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	if strings.Contains(triple, "darwin") || strings.Contains(triple, "macos") {
		return "_"
	}
	return ""
}

func targetClangArgs(targetTriple string) []string {
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	if triple == "" {
		return nil
	}
	if strings.Contains(triple, "darwin") || strings.Contains(triple, "macos") {
		switch {
		case strings.Contains(triple, "x86_64") || strings.Contains(triple, "amd64"):
			return []string{"-arch", "x86_64"}
		case strings.Contains(triple, "aarch64") || strings.Contains(triple, "arm64"):
			return []string{"-arch", "arm64"}
		}
	}
	return []string{"-target", strings.TrimSpace(targetTriple)}
}

func targetLinkerArgs(targetTriple string) []string {
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	if !(strings.Contains(triple, "darwin") || strings.Contains(triple, "macos")) {
		return nil
	}
	if !(strings.Contains(triple, "x86_64") || strings.Contains(triple, "amd64")) {
		return nil
	}
	return []string{
		"-Wl,-ld_classic",
		"-Wl,-no_pie",
		"-Wl,-no_fixup_chains",
		"-Wl,-no_huge",
		"-Wl,-pagezero_size,0x4000",
		"-Wl,-segaddr,TCB_SPACE,0x4000",
		"-Wl,-segaddr,SYSTEM_MANAGED,0x400000",
		"-Wl,-segaddr,SYSTEM_RESERVED,0x7FFFFC000",
		"-Wl,-segaddr,USER_AREA,0x7000000000",
		"-Wl,-image_base,0x700000000000",
	}
}

func targetRunPrefix(targetTriple string) []string {
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" &&
		(strings.Contains(triple, "darwin") || strings.Contains(triple, "macos")) &&
		(strings.Contains(triple, "x86_64") || strings.Contains(triple, "amd64")) {
		return []string{"/usr/bin/arch", "-x86_64"}
	}
	return nil
}

func nativeExecCommand(exePath string, targetTriple string, args ...string) *exec.Cmd {
	prefix := targetRunPrefix(targetTriple)
	if len(prefix) == 0 {
		return exec.Command(exePath, args...)
	}
	cmdArgs := append([]string{}, prefix[1:]...)
	cmdArgs = append(cmdArgs, exePath)
	cmdArgs = append(cmdArgs, args...)
	return exec.Command(prefix[0], cmdArgs...)
}

func writeNativeObjectViaClangIR(clangPath string, result *semantic.Result, objectPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, stderr io.Writer) error {
	_ = clangPath
	// Optional textual IR dump for debugging. This regenerates the module and
	// serializes it (slow for large modules), so it is opt-in.
	if os.Getenv("ELISACORE_EMIT_LL") != "" {
		if ir, gerr := backend.GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result, optLevel, packedProfile, targetTriple); gerr == nil {
			irPath := strings.TrimSuffix(objectPath, filepath.Ext(objectPath)) + ".ll"
			_ = os.WriteFile(irPath, []byte(ir), 0o644)
		}
	}
	// Opt-in fast path: emit the object file directly from the in-memory LLVM
	// module via the LLVM C API (LLVMTargetMachineEmitToFile). This avoids
	// serializing the whole module to tens of MB of textual IR, writing it to
	// disk, and having an external `llc` subprocess re-parse all of it -- which
	// dominates build time for large modules. It is opt-in (ELISACORE_INPROCESS_EMIT)
	// because in-process LLVM codegen runs on a cgo thread with a limited stack
	// and can SIGSEGV on pathologically large functions, whereas `llc` runs in a
	// separate process with a full stack. Once modules avoid such giant functions
	// this can become the default.
	if os.Getenv("ELISACORE_INPROCESS_EMIT") != "" {
		if err := backend.WriteLLVMObjectFileWithOptions(result, objectPath, backend.LLVMObjectEmitOptions{
			OptLevel:      optLevel,
			PackedProfile: packedProfile,
			TargetTriple:  targetTriple,
			DebugInfo:     debugInfo,
			Trace:         traceInfo,
		}); err == nil {
			return nil
		}
		// Fall through to the textual IR + llc path on any in-process error.
	}
	// Default path: generate textual IR and compile it with llc.
	buildTiming := os.Getenv("ELISACORE_BUILD_TIMING") != ""
	irStart := time.Now()
	ir, perfWarnings, err := backend.GenerateLLVMIRWithWarnings(result, optLevel, packedProfile, targetTriple, debugInfo, traceInfo)
	if err != nil {
		return err
	}
	for _, w := range perfWarnings {
		fmt.Fprintln(stderr, w)
	}
	if buildTiming {
		fmt.Fprintf(stderr, "[build-timing] IR-gen: %v (%d bytes IR)\n", time.Since(irStart).Round(time.Millisecond), len(ir))
	}
	irPath := strings.TrimSuffix(objectPath, filepath.Ext(objectPath)) + ".ll"
	if err := os.WriteFile(irPath, []byte(ir), 0o644); err != nil {
		return err
	}
	llcStart := time.Now()
	llcErr := writeLLVMIRObjectViaLLC(irPath, objectPath, targetTriple, stderr)
	if buildTiming {
		fmt.Fprintf(stderr, "[build-timing] llc(+cache): %v err=%v\n", time.Since(llcStart).Round(time.Millisecond), llcErr)
	}
	if llcErr == nil {
		return nil
	} else if err = llcErr; strings.TrimSpace(targetTriple) != "" {
		return err
	}
	_ = stderr
	return backend.WriteLLVMObjectFileWithOptions(result, objectPath, backend.LLVMObjectEmitOptions{
		OptLevel:      optLevel,
		PackedProfile: packedProfile,
		TargetTriple:  targetTriple,
		DebugInfo:     debugInfo,
		Trace:         traceInfo,
	})
}

func writeLLVMIRObjectViaLLC(irPath string, objectPath string, targetTriple string, stderr io.Writer) error {
	llcPath, err := findLLC()
	if err != nil {
		return err
	}
	// Content-hash object cache: compiling the (large, mostly-stable) module with
	// llc -O0 dominates native build time. If an object compiled from byte-for-byte
	// identical IR (and target/llc) already exists in the cache, reuse it and skip
	// llc entirely. Disable with ELISACORE_NO_OBJCACHE.
	cacheKey, cacheOK := llcObjectCacheKey(irPath, targetTriple, llcPath)
	if cacheOK {
		if cachedPath, hit := lookupCachedObject(cacheKey); hit {
			if copyErr := copyFileContents(cachedPath, objectPath); copyErr == nil {
				return nil
			}
		}
	}
	args := []string{"-filetype=obj", "-O0"}
	if strings.TrimSpace(targetTriple) != "" {
		args = append(args, "-mtriple="+strings.TrimSpace(targetTriple))
	}
	args = append(args, irPath, "-o", objectPath)
	cmd := exec.Command(llcPath, args...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to compile LLVM IR object with llc: %w", err)
	}
	if cacheOK {
		storeCachedObject(cacheKey, objectPath)
	}
	return nil
}

func objectCacheDir() (string, bool) {
	if os.Getenv("ELISACORE_NO_OBJCACHE") != "" {
		return "", false
	}
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "elisacore", "objcache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false
	}
	return dir, true
}

// objCacheTempRunRE matches the per-run temp directory segment that the test
// harness embeds in the IR (ModuleID, source_filename, and @panic.file string
// constants). It is normalized away before hashing so re-runs of the same test
// produce a stable cache key. (A reused object may therefore show a previous
// run's temp path in a panic message; this is cosmetic only.)
var objCacheTempRunRE = regexp.MustCompile(`elisacore-(test|native)-run-[0-9]+`)

// llcObjectCacheKey hashes the IR content (with the per-run temp path
// normalized) plus the target triple and llc binary so cached objects are
// invalidated when any of those change.
func llcObjectCacheKey(irPath string, targetTriple string, llcPath string) (string, bool) {
	irBytes, err := os.ReadFile(irPath)
	if err != nil {
		return "", false
	}
	if objCacheTempRunRE != nil {
		irBytes = objCacheTempRunRE.ReplaceAll(irBytes, []byte("elisacore-run-CACHED"))
	}
	h := sha256.New()
	h.Write(irBytes)
	h.Write([]byte("\x00llc\x00"))
	h.Write([]byte(strings.TrimSpace(targetTriple)))
	h.Write([]byte{0})
	h.Write([]byte(llcPath))
	// Mix in the llc binary's size+modtime as a cheap toolchain-version proxy.
	if info, statErr := os.Stat(llcPath); statErr == nil {
		fmt.Fprintf(h, "\x00%d\x00%d", info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

func lookupCachedObject(key string) (string, bool) {
	dir, ok := objectCacheDir()
	if !ok {
		return "", false
	}
	path := filepath.Join(dir, key+".o")
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, true
	}
	return "", false
}

func storeCachedObject(key string, objectPath string) {
	dir, ok := objectCacheDir()
	if !ok {
		return
	}
	finalPath := filepath.Join(dir, key+".o")
	// Write to a temp file then rename, so concurrent builds never observe a
	// partially-written cache entry.
	tmp, err := os.CreateTemp(dir, key+".*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	tmp.Close()
	if copyErr := copyFileContents(objectPath, tmpName); copyErr != nil {
		os.Remove(tmpName)
		return
	}
	if renameErr := os.Rename(tmpName, finalPath); renameErr != nil {
		os.Remove(tmpName)
	}
}

func copyFileContents(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func findLLC() (string, error) {
	if path, err := exec.LookPath("llc"); err == nil {
		return path, nil
	}
	candidates := []string{
		"/opt/homebrew/opt/llvm/bin/llc",
		"/usr/local/opt/llvm/bin/llc",
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("llc is required for cross-target native object emission")
}

type cOpaqueRuntimeBinding struct {
	ElisaName string
	CHeader   string
	CType     string
}

func collectCOpaqueRuntimeBindings(result *semantic.Result) []cOpaqueRuntimeBinding {
	if result == nil {
		return nil
	}
	names := make([]string, 0, len(result.NamedTypes))
	for name := range result.NamedTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	bindings := make([]cOpaqueRuntimeBinding, 0)
	seen := map[string]bool{}
	for _, name := range names {
		opaque, ok := result.NamedTypes[name].(*semantic.OpaqueType)
		if !ok || strings.TrimSpace(opaque.CHeader) == "" || strings.TrimSpace(opaque.CType) == "" {
			continue
		}
		key := name + "\x00" + opaque.CHeader + "\x00" + opaque.CType
		if seen[key] {
			continue
		}
		seen[key] = true
		bindings = append(bindings, cOpaqueRuntimeBinding{
			ElisaName: name,
			CHeader:   strings.TrimSpace(opaque.CHeader),
			CType:     strings.TrimSpace(opaque.CType),
		})
	}
	return bindings
}

func generatedCOpaqueRuntimeShimSource(result *semantic.Result) string {
	bindings := collectCOpaqueRuntimeBindings(result)
	if len(bindings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("#include <stddef.h>\n#include <stdlib.h>\n#include <string.h>\n")
	included := map[string]bool{}
	for _, binding := range bindings {
		if included[binding.CHeader] {
			continue
		}
		included[binding.CHeader] = true
		b.WriteString("#include ")
		b.WriteString(formatCInclude(binding.CHeader))
		b.WriteByte('\n')
	}
	b.WriteString("\nstatic size_t elisa_c_opaque_size_for(const char *name) {\n")
	b.WriteString("  if (name == NULL) { return 0; }\n")
	for _, binding := range bindings {
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return sizeof(%s); }\n", binding.ElisaName, binding.CType, binding.CType)
	}
	b.WriteString("  return 0;\n}\n\n")
	b.WriteString("size_t elisa_c_opaque_size(const char *name) {\n")
	b.WriteString("  return elisa_c_opaque_size_for(name);\n")
	b.WriteString("}\n\n")
	b.WriteString("size_t elisa_c_opaque_align(const char *name) {\n")
	b.WriteString("  if (name == NULL) { return 0; }\n")
	for _, binding := range bindings {
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return _Alignof(%s); }\n", binding.ElisaName, binding.CType, binding.CType)
	}
	b.WriteString("  return 0;\n}\n\n")
	b.WriteString("void *elisa_c_opaque_alloc(const char *name) {\n")
	b.WriteString("  size_t size = elisa_c_opaque_size_for(name);\n")
	b.WriteString("  if (size == 0) { return NULL; }\n")
	b.WriteString("  return calloc(1, size);\n")
	b.WriteString("}\n\n")
	b.WriteString("void elisa_c_opaque_free(void *ptr) {\n")
	b.WriteString("  free(ptr);\n")
	b.WriteString("}\n")
	return b.String()
}

type nativeCallbackRuntimeBinding struct {
	ElisaName string
	CName     string
	Return    string
	Param     string
	CallConv  string
}

func collectNativeCallbackRuntimeBindings(result *semantic.Result) []nativeCallbackRuntimeBinding {
	if result == nil || result.GlobalScope == nil {
		return nil
	}
	names := make([]string, 0, len(result.GlobalScope.Symbols))
	for name := range result.GlobalScope.Symbols {
		names = append(names, name)
	}
	sort.Strings(names)
	bindings := make([]nativeCallbackRuntimeBinding, 0)
	seen := map[string]bool{}
	for _, name := range names {
		sym := result.GlobalScope.Symbols[name]
		if sym == nil || sym.Kind != semantic.SymbolFunc {
			continue
		}
		fnType, ok := sym.Type.(*semantic.FuncType)
		if !ok || strings.TrimSpace(fnType.CallConv) == "" || len(fnType.Params) != 1 {
			continue
		}
		returnType, ok := nativeCallbackCScalarType(fnType.Return)
		if !ok {
			continue
		}
		paramType, ok := nativeCallbackCScalarType(fnType.Params[0])
		if !ok {
			continue
		}
		cName := strings.TrimSpace(sym.LinkName)
		if cName == "" {
			cName = sym.Name
		}
		if !isSimpleCIdentifier(cName) {
			continue
		}
		key := name + "\x00" + cName
		if seen[key] {
			continue
		}
		seen[key] = true
		bindings = append(bindings, nativeCallbackRuntimeBinding{
			ElisaName: name,
			CName:     cName,
			Return:    returnType,
			Param:     paramType,
			CallConv:  strings.ToLower(strings.TrimSpace(fnType.CallConv)),
		})
	}
	return bindings
}

func nativeCallbackCScalarType(t semantic.Type) (string, bool) {
	switch tt := t.(type) {
	case *semantic.BuiltinType:
		switch tt.Name {
		case "void":
			return "void", true
		case "u32":
			return "uint32_t", true
		case "i32":
			return "int32_t", true
		case "usize":
			return "size_t", true
		case "isize":
			return "ptrdiff_t", true
		}
	case *semantic.RefType:
		if builtin, ok := tt.Elem.(*semantic.BuiltinType); ok && builtin.Name == "void" {
			return "void *", true
		}
	}
	return "", false
}

func isSimpleCIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return false
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func nativeCallbackCallConvMacro(callConv string) string {
	switch strings.ToLower(strings.TrimSpace(callConv)) {
	case "stdcall", "winapi":
		return "ELISA_NATIVE_CALLBACK_STDCALL"
	default:
		return "ELISA_NATIVE_CALLBACK_CDECL"
	}
}

func nativeCallbackReturnSuffix(cType string) (string, bool) {
	switch strings.TrimSpace(cType) {
	case "uint32_t":
		return "u32", true
	case "int32_t":
		return "i32", true
	case "size_t":
		return "usize", true
	case "ptrdiff_t":
		return "isize", true
	default:
		return "", false
	}
}

func generatedNativeCallbackRuntimeShimSource(result *semantic.Result) string {
	bindings := collectNativeCallbackRuntimeBindings(result)
	if len(bindings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("#include <stddef.h>\n#include <stdint.h>\n#include <stdlib.h>\n#include <string.h>\n\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("#define WIN32_LEAN_AND_MEAN\n")
	b.WriteString("#include <process.h>\n")
	b.WriteString("#include <windows.h>\n")
	b.WriteString("#else\n")
	b.WriteString("#include <pthread.h>\n")
	b.WriteString("#endif\n\n")
	b.WriteString("#if defined(_WIN32) && !defined(_WIN64)\n")
	b.WriteString("#define ELISA_NATIVE_CALLBACK_STDCALL __stdcall\n")
	b.WriteString("#else\n")
	b.WriteString("#define ELISA_NATIVE_CALLBACK_STDCALL\n")
	b.WriteString("#endif\n")
	b.WriteString("#define ELISA_NATIVE_CALLBACK_CDECL\n\n")
	for _, binding := range bindings {
		fmt.Fprintf(&b, "extern %s %s %s(%s);\n", binding.Return, nativeCallbackCallConvMacro(binding.CallConv), binding.CName, binding.Param)
	}
	b.WriteString("\ntypedef uint32_t (*elisa_native_u32_voidp_fn)(void *);\n")
	b.WriteString("typedef struct elisa_native_u32_thread_start {\n")
	b.WriteString("  elisa_native_u32_voidp_fn fn;\n")
	b.WriteString("  void *arg;\n")
	b.WriteString("  uint32_t result;\n")
	b.WriteString("} elisa_native_u32_thread_start;\n\n")
	b.WriteString("typedef struct elisa_native_u32_callback_context {\n")
	b.WriteString("  const char *name;\n")
	b.WriteString("  void *arg;\n")
	b.WriteString("  uint32_t fallback;\n")
	b.WriteString("  uint32_t result;\n")
	b.WriteString("} elisa_native_u32_callback_context;\n\n")
	b.WriteString("uint32_t elisa_native_callback_call_u32_voidp(const char *name, void *arg, uint32_t fallback);\n\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("static unsigned __stdcall elisa_native_u32_thread_main(void *raw) {\n")
	b.WriteString("  elisa_native_u32_thread_start *start = (elisa_native_u32_thread_start *)raw;\n")
	b.WriteString("  start->result = start->fn(start->arg);\n")
	b.WriteString("  return 0u;\n")
	b.WriteString("}\n")
	b.WriteString("#else\n")
	b.WriteString("static void *elisa_native_u32_thread_main(void *raw) {\n")
	b.WriteString("  elisa_native_u32_thread_start *start = (elisa_native_u32_thread_start *)raw;\n")
	b.WriteString("  start->result = start->fn(start->arg);\n")
	b.WriteString("  return NULL;\n")
	b.WriteString("}\n")
	b.WriteString("#endif\n")
	b.WriteString("static void *elisa_native_u32_context_ctx_thread_entry(void *raw) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  if (ctx != NULL) { ctx->result = elisa_native_callback_call_u32_voidp(ctx->name, ctx->arg, ctx->fallback); }\n")
	b.WriteString("  return NULL;\n")
	b.WriteString("}\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("static unsigned __stdcall elisa_native_u32_context_thread_main(void *raw) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  ctx->result = elisa_native_callback_call_u32_voidp(ctx->name, ctx->arg, ctx->fallback);\n")
	b.WriteString("  return 0u;\n")
	b.WriteString("}\n")
	b.WriteString("#else\n")
	b.WriteString("static void *elisa_native_u32_context_thread_main(void *raw) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  ctx->result = elisa_native_callback_call_u32_voidp(ctx->name, ctx->arg, ctx->fallback);\n")
	b.WriteString("  return NULL;\n")
	b.WriteString("}\n")
	b.WriteString("#endif\n")
	b.WriteString("\nvoid *elisa_native_callback_ptr(const char *name) {\n")
	b.WriteString("  if (name == NULL) { return NULL; }\n")
	for _, binding := range bindings {
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return (void *)&%s; }\n", binding.ElisaName, binding.CName, binding.CName)
	}
	b.WriteString("  return NULL;\n")
	b.WriteString("}\n")
	b.WriteString("\nuint32_t elisa_native_callback_call_u32_voidp(const char *name, void *arg, uint32_t fallback) {\n")
	b.WriteString("  if (name == NULL) { return fallback; }\n")
	for _, binding := range bindings {
		if binding.Return != "uint32_t" || binding.Param != "void *" {
			continue
		}
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return %s(arg); }\n", binding.ElisaName, binding.CName, binding.CName)
	}
	b.WriteString("  return fallback;\n")
	b.WriteString("}\n")
	for _, ret := range []string{"int32_t", "size_t", "ptrdiff_t"} {
		suffix, _ := nativeCallbackReturnSuffix(ret)
		fmt.Fprintf(&b, "\n%s elisa_native_callback_call_%s_voidp(const char *name, void *arg, %s fallback) {\n", ret, suffix, ret)
		b.WriteString("  if (name == NULL) { return fallback; }\n")
		for _, binding := range bindings {
			if binding.Return != ret || binding.Param != "void *" {
				continue
			}
			fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return %s(arg); }\n", binding.ElisaName, binding.CName, binding.CName)
		}
		b.WriteString("  return fallback;\n")
		b.WriteString("}\n")
	}
	b.WriteString("\nuint32_t elisa_native_callback_spawn_join_u32_voidp(const char *name, void *arg, uint32_t fallback) {\n")
	b.WriteString("  elisa_native_u32_thread_start start;\n")
	b.WriteString("  if (name == NULL) { return fallback; }\n")
	b.WriteString("  start.fn = NULL;\n")
	b.WriteString("  start.arg = arg;\n")
	b.WriteString("  start.result = fallback;\n")
	for _, binding := range bindings {
		if binding.Return != "uint32_t" || binding.Param != "void *" {
			continue
		}
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { start.fn = (elisa_native_u32_voidp_fn)&%s; }\n", binding.ElisaName, binding.CName, binding.CName)
	}
	b.WriteString("  if (start.fn == NULL) { return fallback; }\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("  uintptr_t handle = _beginthreadex(NULL, 0u, elisa_native_u32_thread_main, &start, 0u, NULL);\n")
	b.WriteString("  if (handle == 0u) { return fallback; }\n")
	b.WriteString("  if (WaitForSingleObject((HANDLE)handle, INFINITE) != WAIT_OBJECT_0) { CloseHandle((HANDLE)handle); return fallback; }\n")
	b.WriteString("  CloseHandle((HANDLE)handle);\n")
	b.WriteString("#else\n")
	b.WriteString("  pthread_t thread;\n")
	b.WriteString("  if (pthread_create(&thread, NULL, elisa_native_u32_thread_main, &start) != 0) { return fallback; }\n")
	b.WriteString("  if (pthread_join(thread, NULL) != 0) { return fallback; }\n")
	b.WriteString("#endif\n")
	b.WriteString("  return start.result;\n")
	b.WriteString("}\n")
	b.WriteString("\nvoid *elisa_native_callback_context_new_u32_voidp(const char *name, void *arg, uint32_t fallback) {\n")
	b.WriteString("  if (name == NULL || elisa_native_callback_ptr(name) == NULL) { return NULL; }\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)calloc(1, sizeof(*ctx));\n")
	b.WriteString("  if (ctx == NULL) { return NULL; }\n")
	b.WriteString("  ctx->name = name;\n")
	b.WriteString("  ctx->arg = arg;\n")
	b.WriteString("  ctx->fallback = fallback;\n")
	b.WriteString("  ctx->result = fallback;\n")
	b.WriteString("  return ctx;\n")
	b.WriteString("}\n")
	b.WriteString("\nvoid *elisa_native_callback_context_entry_u32_voidp(void) {\n")
	b.WriteString("  return (void *)&elisa_native_u32_context_ctx_thread_entry;\n")
	b.WriteString("}\n")
	b.WriteString("\nint elisa_native_callback_context_start_u32_voidp(void *raw, uintptr_t *out) {\n")
	b.WriteString("  if (raw == NULL || out == NULL) { return -1; }\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("  uintptr_t handle = _beginthreadex(NULL, 0u, elisa_native_u32_context_thread_main, raw, 0u, NULL);\n")
	b.WriteString("  if (handle == 0u) { return -1; }\n")
	b.WriteString("  *out = handle;\n")
	b.WriteString("  return 0;\n")
	b.WriteString("#else\n")
	b.WriteString("  pthread_t thread;\n")
	b.WriteString("  int status = pthread_create(&thread, NULL, elisa_native_u32_context_thread_main, raw);\n")
	b.WriteString("  if (status != 0) { return status; }\n")
	b.WriteString("  *out = (uintptr_t)thread;\n")
	b.WriteString("  return 0;\n")
	b.WriteString("#endif\n")
	b.WriteString("}\n")
	b.WriteString("\nuint32_t elisa_native_callback_context_join_u32_voidp(uintptr_t handle, void *raw, uint32_t fallback) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  if (handle == 0u || ctx == NULL) { return fallback; }\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("  if (WaitForSingleObject((HANDLE)handle, INFINITE) != WAIT_OBJECT_0) { CloseHandle((HANDLE)handle); return fallback; }\n")
	b.WriteString("  CloseHandle((HANDLE)handle);\n")
	b.WriteString("#else\n")
	b.WriteString("  if (pthread_join((pthread_t)handle, NULL) != 0) { return fallback; }\n")
	b.WriteString("#endif\n")
	b.WriteString("  return ctx->result;\n")
	b.WriteString("}\n")
	b.WriteString("\nuint32_t elisa_native_callback_context_spawn_join_u32_voidp(void *raw, uint32_t fallback) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  if (ctx == NULL) { return fallback; }\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("  uintptr_t handle = _beginthreadex(NULL, 0u, elisa_native_u32_context_thread_main, ctx, 0u, NULL);\n")
	b.WriteString("  if (handle == 0u) { return fallback; }\n")
	b.WriteString("  if (WaitForSingleObject((HANDLE)handle, INFINITE) != WAIT_OBJECT_0) { CloseHandle((HANDLE)handle); return fallback; }\n")
	b.WriteString("  CloseHandle((HANDLE)handle);\n")
	b.WriteString("#else\n")
	b.WriteString("  pthread_t thread;\n")
	b.WriteString("  if (pthread_create(&thread, NULL, elisa_native_u32_context_thread_main, ctx) != 0) { return fallback; }\n")
	b.WriteString("  if (pthread_join(thread, NULL) != 0) { return fallback; }\n")
	b.WriteString("#endif\n")
	b.WriteString("  return ctx->result;\n")
	b.WriteString("}\n")
	b.WriteString("\nuint32_t elisa_native_callback_context_result_u32(void *raw, uint32_t fallback) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  return ctx == NULL ? fallback : ctx->result;\n")
	b.WriteString("}\n")
	b.WriteString("\nvoid elisa_native_callback_context_free(void *raw) {\n")
	b.WriteString("  free(raw);\n")
	b.WriteString("}\n")
	return b.String()
}

func resultNeedsLLVMCAPILinkage(result *semantic.Result) bool {
	if result == nil || result.GlobalScope == nil {
		return false
	}
	for _, sym := range result.GlobalScope.Symbols {
		if sym == nil || (sym.Kind != semantic.SymbolExternFunc && sym.Kind != semantic.SymbolExternVar) {
			continue
		}
		linkName := strings.TrimSpace(sym.LinkName)
		if strings.HasPrefix(linkName, "LLVM") || strings.HasPrefix(sym.Name, "llvm_") {
			return true
		}
	}
	return false
}

func llvmNativeLinkFlags(existing []string) []string {
	flags := make([]string, 0, 8)
	switch runtime.GOOS {
	case "darwin":
		switch runtime.GOARCH {
		case "arm64":
			flags = appendExistingLibraryDirFlag(flags, "/opt/homebrew/opt/llvm/lib", existing)
		case "amd64":
			flags = appendExistingLibraryDirFlag(flags, "/usr/local/opt/llvm/lib", existing)
		}
	case "linux":
		for _, dir := range []string{
			"/usr/lib/llvm-21/lib",
			"/usr/lib/llvm-20/lib",
			"/usr/lib/llvm-19/lib",
			"/usr/lib/llvm-18/lib",
			"/usr/lib/llvm-17/lib",
			"/usr/lib/llvm-16/lib",
			"/usr/lib/llvm-15/lib",
		} {
			flags = appendExistingLibraryDirFlag(flags, dir, existing)
		}
	}
	flags = append(flags, "-lLLVM-C", "-lLLVM")
	return flags
}

func appendExistingLibraryDirFlag(flags []string, dir string, existing []string) []string {
	if dir == "" || linkFlagsContainLibraryDir(existing, dir) {
		return flags
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return append(flags, "-L"+dir)
	}
	return flags
}

func appendMissingLinkFlags(base []string, candidates ...string) []string {
	result := append([]string(nil), base...)
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || linkFlagsContain(result, candidate) {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func linkFlagsContain(flags []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for index, flag := range flags {
		trimmed := strings.TrimSpace(flag)
		if trimmed == target {
			return true
		}
		if strings.HasPrefix(target, "-l") && trimmed == "-l" && index+1 < len(flags) && strings.TrimSpace(flags[index+1]) == strings.TrimPrefix(target, "-l") {
			return true
		}
	}
	return false
}

func linkFlagsContainLibraryDir(flags []string, dir string) bool {
	dir = filepath.Clean(strings.TrimSpace(dir))
	for index, flag := range flags {
		trimmed := strings.TrimSpace(flag)
		if strings.HasPrefix(trimmed, "-L") && filepath.Clean(strings.TrimSpace(strings.TrimPrefix(trimmed, "-L"))) == dir {
			return true
		}
		if trimmed == "-L" && index+1 < len(flags) && filepath.Clean(strings.TrimSpace(flags[index+1])) == dir {
			return true
		}
	}
	return false
}

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
	if _, statErr := os.Stat(artifact.object); statErr == nil {
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

func compileDefaultElisaCoreRuntimeObject(outputPath string, packedProfile backend.PackedLoweringProfile, targetTriple string, stderr io.Writer) error {
	runtimePath, err := defaultElisaCoreRuntimeSupportPath()
	if err != nil {
		return err
	}
	src, err := readSourceWithIncludes(runtimePath, map[string]bool{})
	if err != nil {
		return err
	}
	var parseStderr bytes.Buffer
	file, ok := parseProgram(runtimePath, src, &parseStderr)
	if !ok {
		if parseStderr.Len() != 0 && stderr != nil {
			_, _ = io.Copy(stderr, &parseStderr)
		}
		return fmt.Errorf("failed to parse default elisacore runtime support")
	}
	runtimeResult := semantic.Analyze(file)
	if errs := runtimeResult.Errors(); len(errs) != 0 {
		if stderr != nil {
			for _, e := range errs {
				fmt.Fprintf(stderr, "%s\n", e)
			}
		}
		return fmt.Errorf("failed to analyze default elisacore runtime support")
	}
	if warns := runtimeResult.Notices(); len(warns) != 0 && stderr != nil {
		for _, w := range warns {
			if shouldSuppressDeprecatedWarningsForTests(w) {
				continue
			}
			fmt.Fprintf(stderr, "%s\n", w)
		}
	}
	if clangPath, err := exec.LookPath("clang"); err == nil {
		if err := writeNativeObjectViaClangIR(clangPath, runtimeResult, outputPath, backend.OptimizationLevel3, packedProfile, targetTriple, false, false, stderr); err == nil {
			return nil
		} else if strings.TrimSpace(targetTriple) != "" {
			return err
		}
	}
	return backend.WriteLLVMObjectFileWithOptions(runtimeResult, outputPath, backend.LLVMObjectEmitOptions{
		OptLevel:      backend.OptimizationLevel3,
		PackedProfile: packedProfile,
		TargetTriple:  targetTriple,
	})
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
