package main

import (
	"crypto/sha256"
	"elisacore/src/backend"
	"elisacore/src/easm"
	"elisacore/src/semantic"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// rejectNullBoundSymbols inspects a freshly-linked Mach-O for the symbol-split
// null-bind hazard: an undefined external `_sym.N` (LLVM's dedup rename) that exists
// alongside the base `_sym`. Under `-Wl,-undefined,dynamic_lookup` the `.N` flat-
// namespace stub resolves to NULL, so the first call through it crashes (this was the
// CUSA07399 boot SIGSEGV: `_mprotect.1`). It also rejects undefined Elisa runtime helpers,
// which have no system-library provider. The check is deliberately limited to compiler-owned
// hazards; arbitrary user `extern` declarations retain the existing dynamic-lookup FFI contract.
func rejectNullBoundSymbols(exePath string) error {
	nmPath, err := exec.LookPath("nm")
	if err != nil {
		return nil
	}
	out, err := exec.Command(nmPath, "-m", exePath).Output()
	if err != nil {
		return nil
	}
	split := findSplitNullBoundSymbols(string(out))
	helpers := findNullBoundRuntimeHelperSymbols(string(out))
	if len(split) == 0 && len(helpers) == 0 {
		return nil
	}
	return nullBoundSymbolsError(split, helpers)
}

func nullBoundSymbolsError(split []string, helpers []string) error {
	var problems []string
	if len(split) != 0 {
		problems = append(problems, fmt.Sprintf("undefined split symbol(s) %s", strings.Join(split, ", ")))
	}
	if len(helpers) != 0 {
		problems = append(problems, fmt.Sprintf("undefined Elisa runtime helper(s) %s", strings.Join(helpers, ", ")))
	}
	return fmt.Errorf("native link rejected: %s; these symbols bind to NULL under dynamic_lookup and can segfault on first call (fix the duplicate/runtime declaration or set ELISACORE_NO_LINK_BINDING_CHECK=1 to override)", strings.Join(problems, "; "))
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
// flat-namespace duplicate that binds to NULL under -undefined,dynamic_lookup, but only
// when the unsuffixed symbol is also present. Without that base symbol, a user may have
// deliberately chosen a link name ending in `.N`; rejecting that ordinary FFI name would
// contradict the dynamic-lookup contract preserved by this gate.
func findSplitNullBoundSymbols(nmOutput string) []string {
	lineSymbol := regexp.MustCompile(`\b(_[A-Za-z0-9_$]+(?:\.\d+)?)\b`)
	undefinedSplit := regexp.MustCompile(`\(undefined\)[^\n]*\b(_[A-Za-z0-9_$]+\.\d+)\b`)
	symbols := map[string]bool{}
	for _, line := range strings.Split(nmOutput, "\n") {
		if sym := lineSymbol.FindStringSubmatch(line); len(sym) == 2 {
			symbols[sym[1]] = true
		}
	}
	seen := map[string]bool{}
	var split []string
	for _, m := range undefinedSplit.FindAllStringSubmatch(nmOutput, -1) {
		sym := m[1]
		base := sym[:strings.LastIndexByte(sym, '.')]
		if symbols[base] && !seen[sym] {
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
	// Under -Wperf, unacknowledged scalar loops (no `can Scalar` grant) are hard errors.
	if result.EnforcePerfLints && len(perfWarnings) > 0 {
		return fmt.Errorf("-Wperf: %d loop(s) expected to vectorize stayed scalar; wrap intentional scalar loops in a `can Scalar:` block", len(perfWarnings))
	}
	if buildTiming {
		fmt.Fprintf(stderr, "[build-timing] IR-gen: %v (%d bytes IR)\n", time.Since(irStart).Round(time.Millisecond), len(ir))
	}
	irPath := strings.TrimSuffix(objectPath, filepath.Ext(objectPath)) + ".ll"
	if err := os.WriteFile(irPath, []byte(ir), 0o644); err != nil {
		return err
	}
	llcStart := time.Now()
	llcErr := writeLLVMIRObjectViaLLC(irPath, objectPath, optLevel, targetTriple, stderr)
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

// llcCodegenOptArg is llc's CODEGEN level for IR the in-process pipeline already optimized at
// optLevel. It matches `-emit obj`, whose target machine uses LLVMCodeGenLevelDefault (= llc
// -O2): instruction selection, register allocation and scheduling were otherwise run at
// -O0 (FastISel + the fast register allocator) for every optimized project build/run and for
// the -O3 default runtime object. -O0 builds keep llc -O0 for compile speed.
func llcCodegenOptArg(optLevel backend.OptimizationLevel) string {
	if optLevel == backend.OptimizationLevel0 {
		return "-O0"
	}
	return "-O2"
}

func writeLLVMIRObjectViaLLC(irPath string, objectPath string, optLevel backend.OptimizationLevel, targetTriple string, stderr io.Writer) error {
	llcPath, err := findLLC()
	if err != nil {
		return err
	}
	// Content-hash object cache: compiling the (large, mostly-stable) module with
	// llc -O0 dominates native build time. If an object compiled from byte-for-byte
	// identical IR (and target/llc) already exists in the cache, reuse it and skip
	// llc entirely. Disable with ELISACORE_NO_OBJCACHE.
	cacheKey, cacheOK := llcObjectCacheKey(irPath, llcCodegenOptArg(optLevel), targetTriple, llcPath)
	if cacheOK {
		if cachedPath, hit := lookupCachedObject(cacheKey); hit {
			if copyErr := copyFileContents(cachedPath, objectPath); copyErr == nil {
				return nil
			}
		}
	}
	args := []string{"-filetype=obj", llcCodegenOptArg(optLevel)}
	if strings.TrimSpace(targetTriple) != "" {
		args = append(args, "-mtriple="+strings.TrimSpace(targetTriple))
	}
	// The IR was optimized by a target machine tuned for this CPU (host CPU for a native
	// build, or `-mcpu`); select the same CPU for instruction selection. The textual IR
	// carries no per-function "target-cpu", so without this llc fell back to the generic CPU.
	if cpu := resolvedNativeTargetCPU(targetTriple); cpu != "" {
		args = append(args, "-mcpu="+cpu)
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

// llcObjectCacheKey hashes exact IR bytes. Text resembling a temporary path
// can be user data; normalizing arbitrary IR would merge different programs.
func llcObjectCacheKey(irPath string, codegenOpt string, targetTriple string, llcPath string) (string, bool) {
	irBytes, err := os.ReadFile(irPath)
	if err != nil {
		return "", false
	}
	target, err := backend.ResolveTargetIdentity(targetTriple)
	if err != nil {
		return "", false
	}
	toolStamp, err := toolchainNativeContentStamp(llcPath)
	if err != nil {
		return "", false
	}
	return llcObjectCacheKeyForInputs(irBytes, codegenOpt, target, llcPath, toolStamp), true
}

func llcObjectCacheKeyForInputs(irBytes []byte, codegenOpt string, target backend.TargetIdentity, llcPath string, toolStamp string) string {
	h := sha256.New()
	testRunnerCacheWriteString(h, "llc-object-exact-ir-v3")
	h.Write(irBytes)
	h.Write([]byte("\x00llc\x00"))
	writeResolvedNativeCacheTarget(h, target)
	testRunnerCacheWriteString(h, "codegen="+codegenOpt)
	testRunnerCacheWriteString(h, "llc-path="+llcPath)
	testRunnerCacheWriteString(h, "llc-content="+toolStamp)
	return hex.EncodeToString(h.Sum(nil))
}

func lookupCachedObject(key string) (string, bool) {
	dir, ok := objectCacheDir()
	if !ok {
		return "", false
	}
	path := filepath.Join(dir, key+".o")
	if usableCachedFile(path, false) {
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
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("llc is required for cross-target native object emission")
}
