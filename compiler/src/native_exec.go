package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"elisacore/src/ast"
	"elisacore/src/backend"
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

type nativeBuildTiming struct {
	ObjectWrite  time.Duration
	HeaderGen    time.Duration
	HeaderWrite  time.Duration
	Link         time.Duration
	CacheLookup  time.Duration
	CachePublish time.Duration
	CacheHit     bool
}

func buildNativeExecutable(result *semantic.Result, foreignFiles []string, linkFlags []string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, stderr io.Writer) (string, func(), error) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return "", func() {}, fmt.Errorf("clang is required to build native executables: %w", err)
	}
	exePath, cleanup, _, err := buildNativeExecutableWithClang(clangPath, result, foreignFiles, linkFlags, outputPath, optLevel, packedProfile, stderr)
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

func buildNativeExecutableWithClang(clangPath string, result *semantic.Result, foreignFiles []string, linkFlags []string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, stderr io.Writer) (string, func(), nativeBuildTiming, error) {
	if result == nil {
		return "", func() {}, nativeBuildTiming{}, fmt.Errorf("semantic result is nil")
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
	if err := backend.WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result, objectPath, optLevel, packedProfile); err != nil {
		cleanup()
		return "", func() {}, timing, err
	}
	timing.ObjectWrite = time.Since(objectStart)
	runtimeObjectPath := ""
	if !resultDefinesDefaultElisaCoreRuntime(result) {
		runtimeObjectPath = filepath.Join(tempDir, "elisacore_runtime.o")
		if err := writeDefaultElisaCoreRuntimeObject(runtimeObjectPath, packedProfile, "", stderr); err != nil {
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

	linkArgs := make([]string, 0, 5+len(foreignFiles))
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
	return exePath, cleanup, timing, nil
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
	resolved := dedupeStrings(append([]string(nil), foreignFiles...))
	repoRoot, err := compilerRepoRootForNativeExec()
	if err != nil {
		return nil, err
	}
	aesRuntimePath := filepath.Join(repoRoot, "compiler", "runtime", "aes.c")
	if _, err := os.Stat(aesRuntimePath); err != nil {
		return nil, fmt.Errorf("failed to locate default AES runtime support %s: %w", aesRuntimePath, err)
	}
	resolved = append(resolved, aesRuntimePath)
	return dedupeStrings(resolved), nil
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

func writeDefaultElisaCoreRuntimeObject(outputPath string, packedProfile backend.PackedLoweringProfile, targetTriple string, stderr io.Writer) error {
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

func runNativeExecutable(exePath string, stdout io.Writer, stderr io.Writer) error {
	var runStdout bytes.Buffer
	var runStderr bytes.Buffer
	cmd := exec.Command(exePath)
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
