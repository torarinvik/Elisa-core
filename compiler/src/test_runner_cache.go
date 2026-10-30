package main

import (
	"context"
	"crypto/sha256"
	"elisacore/src/easm"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unsafe"

	"elisacore/src/backend"
	"elisacore/src/smt"
)

type testRunnerCacheArtifact struct {
	key        string
	dir        string
	executable string
}

func testRunnerCacheEnabled() bool {
	return strings.TrimSpace(os.Getenv("ELISACORE_TEST_CACHE")) != "0" && !testRunnerCacheObserverEnabled()
}

// Test-runner cache hits skip some or all work from the current request. Keep
// them out of requests that asked to observe or validate that work.
func testRunnerCacheObserverEnabled() bool {
	if buildCacheRequestObserverEnabled() || testTimingEnabled() || testPhaseDebugEnabled() {
		return true
	}
	return os.Getenv("ELISACORE_KEEP_TEST_TEMP") != ""
}

func writeTestRunnerSMTCacheInputs(hash hash.Hash, enableSMT bool) error {
	if !enableSMT {
		return nil
	}
	solver := smt.ResolveBinary("")
	testRunnerCacheWriteString(hash, "smt-selector="+solver)
	if selected, err := exec.LookPath(solver); err == nil {
		stamp, stampErr := toolchainNativeContentStamp(selected)
		if stampErr != nil {
			return stampErr
		}
		testRunnerCacheWriteString(hash, "smt-executable="+selected)
		testRunnerCacheWriteString(hash, "smt-content="+stamp)
	} else {
		testRunnerCacheWriteString(hash, "smt-executable=unavailable")
	}
	return nil
}

func writeTestRunnerLinkAuditInputs(hash hash.Hash, targetTriple string) error {
	if !isMachOTriple(targetTriple) {
		return nil
	}
	nmPath, err := exec.LookPath("nm")
	if err != nil {
		testRunnerCacheWriteString(hash, "macho-nm=unavailable")
		return nil
	}
	stamp, err := toolchainNativeContentStamp(nmPath)
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "macho-nm="+nmPath)
	testRunnerCacheWriteString(hash, "macho-nm-content="+stamp)
	return nil
}

const testRunnerLinkerProbeTimeout = 5 * time.Second

// testRunnerLinkerQueryArgs asks the same Clang driver to resolve ld using the
// target and linker-selection flags that will be present on the actual link.
// The driver's selected linker is an input to both persistent test-runner caches.
func testRunnerLinkerQueryArgs(targetTriple string, linkFlags []string) []string {
	args := append([]string{}, targetClangArgs(targetTriple)...)
	args = append(args, targetLinkerArgs(targetTriple)...)
	args = append(args, linkFlags...)
	return append(args, "-print-prog-name=ld")
}

func resolveTestRunnerLinkerPath(driverOutput string, targetTriple string) (string, error) {
	selected := strings.TrimSpace(driverOutput)
	if selected == "" || strings.ContainsAny(selected, "\r\n") {
		return "", fmt.Errorf("clang returned an invalid linker selector %q", driverOutput)
	}
	if !filepath.IsAbs(selected) {
		// Apple Clang may use xcrun/toolchain selection for a linker whose driver
		// spelling is only "ld". PATH lookup alone cannot prove that selection.
		if isMachOTriple(targetTriple) {
			return "", fmt.Errorf("clang returned an ambiguous linker selector %q for a Mach-O target", selected)
		}
		resolved, err := exec.LookPath(selected)
		if err != nil {
			return "", fmt.Errorf("resolve clang linker %q: %w", selected, err)
		}
		selected = resolved
	}
	if !filepath.IsAbs(selected) {
		resolved, err := filepath.Abs(selected)
		if err != nil {
			return "", err
		}
		selected = resolved
	}
	info, err := os.Stat(selected)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("clang linker %q is not an executable regular file", selected)
	}
	return selected, nil
}

func writeTestRunnerLinkerCacheIdentity(hash hash.Hash, linkerPath string) error {
	info, err := os.Stat(linkerPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("clang linker %q is not an executable regular file", linkerPath)
	}
	stamp, err := toolchainNativeContentStamp(linkerPath)
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "linker="+linkerPath)
	testRunnerCacheWriteString(hash, "linker-content="+stamp)
	return nil
}

func writeTestRunnerLinkerCacheInputs(hash hash.Hash, clangPath string, targetTriple string, linkFlags []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), testRunnerLinkerProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, clangPath, testRunnerLinkerQueryArgs(targetTriple, linkFlags)...)
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("query clang linker selection: %w", ctx.Err())
		}
		return fmt.Errorf("query clang linker selection: %w", err)
	}
	linkerPath, err := resolveTestRunnerLinkerPath(string(output), targetTriple)
	if err != nil {
		return err
	}
	if err := writeTestRunnerLinkerCacheIdentity(hash, linkerPath); err != nil {
		return err
	}
	// /usr/bin/ld on macOS delegates to the active developer toolchain. Hash
	// xcrun's selected linker too, so an Xcode/toolchain switch at the same
	// system selector cannot leave an old test executable eligible for reuse.
	if runtime.GOOS == "darwin" && filepath.Clean(linkerPath) == "/usr/bin/ld" {
		// Use the system dispatcher that /usr/bin/ld is paired with; PATH may
		// contain an unrelated xcrun earlier in the search order.
		const xcrunPath = "/usr/bin/xcrun"
		if err := writeTestRunnerLinkerCacheIdentity(hash, xcrunPath); err != nil {
			return err
		}
		xcrunCtx, xcrunCancel := context.WithTimeout(context.Background(), testRunnerLinkerProbeTimeout)
		defer xcrunCancel()
		xcrun := exec.CommandContext(xcrunCtx, xcrunPath, "--find", "ld")
		resolvedOutput, err := xcrun.Output()
		if err != nil {
			if xcrunCtx.Err() != nil {
				return fmt.Errorf("resolve active macOS linker with xcrun: %w", xcrunCtx.Err())
			}
			return fmt.Errorf("resolve active macOS linker with xcrun: %w", err)
		}
		resolvedPath, err := resolveTestRunnerLinkerPath(string(resolvedOutput), targetTriple)
		if err != nil {
			return fmt.Errorf("resolve active macOS linker with xcrun: %w", err)
		}
		if filepath.Clean(resolvedPath) == "/usr/bin/ld" {
			return fmt.Errorf("xcrun did not resolve /usr/bin/ld to a developer-toolchain linker")
		}
		if err := writeTestRunnerLinkerCacheIdentity(hash, resolvedPath); err != nil {
			return err
		}
	}
	return nil
}

func testRunnerLinkInputContentStamp(path string) (string, error) {
	firstStamp, err := toolchainContentStamp(path)
	if err != nil {
		return "", err
	}
	before, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("link input %q is not a regular file", path)
	}
	file, err := openToolchainStampFile(path)
	if err != nil {
		return "", err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return "", err
	}
	if !sameToolchainFileVersion(before, opened) {
		file.Close()
		return "", fmt.Errorf("link input %q changed while inspecting it", path)
	}
	var header [8]byte
	count, readErr := io.ReadFull(file, header[:])
	after, statErr := file.Stat()
	selected, selectedErr := os.Stat(path)
	closeErr := file.Close()
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", readErr
	}
	if statErr != nil {
		return "", statErr
	}
	if selectedErr != nil {
		return "", selectedErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if !sameToolchainFileVersion(opened, after) || !sameToolchainFileVersion(after, selected) {
		return "", fmt.Errorf("link input %q changed while inspecting it", path)
	}
	if count == len(header) && string(header[:]) == "!<thin>\n" {
		return "", fmt.Errorf("thin archive %q has external members", path)
	}
	var magic [4]byte
	copy(magic[:], header[:4])
	objectImage := count >= 4 && (nativeToolMagic(magic) || string(header[:4]) == "BC\xc0\xde" || string(header[:4]) == "\xde\xc0\x17\x0b")
	archive := count == len(header) && string(header[:]) == "!<arch>\n"
	if !archive && !objectImage {
		return "", fmt.Errorf("link input %q is not a self-contained object or archive", path)
	}
	secondStamp, err := toolchainContentStamp(path)
	if err != nil {
		return "", err
	}
	if firstStamp != secondStamp {
		return "", fmt.Errorf("link input %q changed while hashing it", path)
	}
	return secondStamp, nil
}

// writeTestRunnerLinkDependencyInputs keys explicit self-contained object and
// archive files. Library searches, response files, linker scripts, plugins, and
// unclassified flags can select additional files that this cache cannot resolve,
// so those requests build cold and are not published.
func writeTestRunnerLinkDependencyInputs(hash hash.Hash, linkFlags []string) error {
	for index := 0; index < len(linkFlags); index++ {
		flag := strings.TrimSpace(linkFlags[index])
		if flag == "" {
			continue
		}
		switch {
		case strings.HasPrefix(flag, "-fuse-ld=") && strings.TrimSpace(strings.TrimPrefix(flag, "-fuse-ld=")) != "":
			// The selected linker path and bytes are queried and hashed separately.
		default:
			if strings.HasPrefix(flag, "-") || strings.HasPrefix(flag, "@") {
				return fmt.Errorf("test-runner cache cannot key opaque link input %q", flag)
			}
			path, err := filepath.Abs(flag)
			if err != nil {
				return err
			}
			stamp, err := testRunnerLinkInputContentStamp(path)
			if err != nil {
				return err
			}
			testRunnerCacheWriteString(hash, "link-input="+path)
			testRunnerCacheWriteString(hash, "link-input-content="+stamp)
		}
	}
	return nil
}

func writeTestRunnerForeignLinkInputs(hash hash.Hash, foreignFiles []string) error {
	for _, foreignFile := range foreignFiles {
		trimmed := strings.TrimSpace(foreignFile)
		if trimmed == "" {
			continue
		}
		switch strings.ToLower(filepath.Ext(trimmed)) {
		case ".c", ".cc", ".cpp", ".cxx", ".c++", ".m", ".mm", ".s", ".asm":
			// The text include expander below is not a C/C++ preprocessor and
			// cannot represent angle-bracket, macro, or conditional includes.
			return fmt.Errorf("test-runner cache cannot key preprocessed foreign source %q", trimmed)
		}
		path, err := filepath.Abs(trimmed)
		if err != nil {
			return err
		}
		stamp, err := testRunnerLinkInputContentStamp(path)
		if err != nil {
			return err
		}
		testRunnerCacheWriteString(hash, "foreign-link-input="+path)
		testRunnerCacheWriteString(hash, "foreign-link-content="+stamp)
	}
	return nil
}

func testRunnerCacheIncludeEnvironmentIsSafe() error {
	for _, name := range []string{"CPATH", "C_INCLUDE_PATH", "CPLUS_INCLUDE_PATH", "OBJC_INCLUDE_PATH"} {
		if _, set := os.LookupEnv(name); set {
			return fmt.Errorf("test-runner cache cannot key external include environment %s", name)
		}
	}
	return nil
}

func testRunnerCacheDebugEnabled() bool {
	return strings.TrimSpace(os.Getenv("ELISACORE_TEST_CACHE_DEBUG")) != ""
}

func debugTestRunnerCache(stderr io.Writer, status string, artifact testRunnerCacheArtifact) {
	if stderr == nil || !testRunnerCacheDebugEnabled() {
		return
	}
	prefix := artifact.key
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	fmt.Fprintf(stderr, "[ cache    ] %s key=%s exe=%s\n", status, prefix, artifact.executable)
}

// usableCachedFile is the minimum integrity check before a cache entry can affect a
// compiler result. Cache publication is atomic, but an entry can still be truncated or
// replaced outside the process (for example by cleanup tooling or a damaged filesystem).
// Treating any existing path as a hit would then report success while copying a directory
// or an empty artifact to the caller. Executable entries additionally need an execute bit
// because the early runner cache uses the cached path directly.
func usableCachedFile(path string, executable bool) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return false
	}
	return !executable || info.Mode().Perm()&0o111 != 0
}

func locateCachedTestRunner(runnerSource string, shimSource string, easmModules []*easm.Module, foreignFiles []string, linkFlags []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, enableSMT bool) (testRunnerCacheArtifact, bool, error) {
	artifact, err := testRunnerCacheArtifactFor(runnerSource, shimSource, easmModules, foreignFiles, linkFlags, optLevel, packedProfile, targetTriple, enableSMT)
	if err != nil {
		return testRunnerCacheArtifact{}, false, err
	}
	if artifact.executable == "" {
		return artifact, false, nil
	}
	if usableCachedFile(artifact.executable, true) {
		return artifact, true, nil
	} else if _, err := os.Stat(artifact.executable); err != nil && !os.IsNotExist(err) {
		return artifact, false, err
	}
	return artifact, false, nil
}

func publishCachedTestRunner(artifact testRunnerCacheArtifact, builtExecutable string) error {
	if artifact.dir == "" || artifact.executable == "" || strings.TrimSpace(builtExecutable) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(artifact.dir), 0o755); err != nil {
		return err
	}
	if usableCachedFile(artifact.executable, true) {
		return nil
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(artifact.dir), ".elisa-test-runner-stage-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(stagingDir)
	}()
	stageExecutable := filepath.Join(stagingDir, filepath.Base(artifact.executable))
	if err := copyExecutableFile(builtExecutable, stageExecutable); err != nil {
		return err
	}
	if err := os.Chmod(stageExecutable, 0o755); err != nil {
		return err
	}
	if err := os.Rename(stagingDir, artifact.dir); err != nil {
		if usableCachedFile(artifact.executable, true) {
			return nil
		}
		return err
	}
	return nil
}

// writeCommonTestRunnerCacheInputs hashes every build input that determines a test
// executable EXCEPT the generated runner/shim source (inner cache) or the program
// source/filter (early cache): build env, toolchain, easm, self-contained foreign
// object/archive inputs, link flags and selected Clang linker, the elisacore runtime
// support, and the compiler's own Go sources.
// It treats the installed Clang resource directory and default OS SDK headers and
// libraries as stable toolchain inputs; their full contents are not traversed here.
// Both cache keys layer their distinguishing inputs on top of this.
func writeCommonTestRunnerCacheInputs(hash hash.Hash, easmModules []*easm.Module, foreignFiles []string, linkFlags []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, enableSMT bool) error {
	if err := writeTestRunnerLinkDependencyInputs(hash, linkFlags); err != nil {
		return err
	}
	if err := testRunnerCacheIncludeEnvironmentIsSafe(); err != nil {
		return err
	}
	resolvedForeignFiles, err := withDefaultNativeRuntimeForeignFiles(foreignFiles)
	if err != nil {
		return err
	}
	if err := writeTestRunnerForeignLinkInputs(hash, resolvedForeignFiles); err != nil {
		return err
	}
	if err := writeNativeCacheTarget(hash, targetTriple); err != nil {
		return err
	}
	if err := writeTestRunnerSMTCacheInputs(hash, enableSMT); err != nil {
		return err
	}
	if err := writeTestRunnerLinkAuditInputs(hash, targetTriple); err != nil {
		return err
	}
	for _, entry := range buildCacheEnvKey() {
		testRunnerCacheWriteString(hash, "env="+entry)
	}

	testRunnerCacheWriteString(hash, "goos="+runtime.GOOS)
	testRunnerCacheWriteString(hash, "goarch="+runtime.GOARCH)
	testRunnerCacheWriteString(hash, "goversion="+runtime.Version())
	testRunnerCacheWriteString(hash, fmt.Sprintf("opt=%d", optLevel))
	testRunnerCacheWriteString(hash, "packedProfile="+packedProfile.SelectionKey())
	testRunnerCacheWriteString(hash, "targetTriple="+strings.TrimSpace(targetTriple))
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "clang="+clangPath)
	clangStamp, err := toolchainNativeContentStamp(clangPath)
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "clang-content="+clangStamp)
	if err := writeTestRunnerLinkerCacheInputs(hash, clangPath, targetTriple, linkFlags); err != nil {
		return err
	}
	for _, module := range easmModules {
		if module == nil {
			continue
		}
		testRunnerCacheWriteString(hash, "easm="+module.Path)
		if strings.TrimSpace(module.Path) == "" {
			continue
		}
		source, err := os.ReadFile(module.Path)
		if err != nil {
			return err
		}
		testRunnerCacheWriteBytes(hash, "easm-body", source)
	}
	for _, linkFlag := range linkFlags {
		trimmed := strings.TrimSpace(linkFlag)
		if trimmed == "" {
			continue
		}
		testRunnerCacheWriteString(hash, "linkflag="+trimmed)
	}
	if runtimePath, err := defaultElisaCoreRuntimeSupportPath(); err == nil {
		runtimeSource, readErr := readSourceWithIncludes(runtimePath, map[string]bool{})
		if readErr != nil {
			return readErr
		}
		testRunnerCacheWriteBytes(hash, "elisacore-runtime-support", runtimeSource)
	} else {
		return err
	}
	// The compiler's identity is the RUNNING BINARY (see compilerSourceStamp), not the
	// .go files on disk: keyed on the tree, a runner built by one binary was served to
	// another built from different sources.
	stamp, err := compilerSourceStamp()
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "compiler="+stamp)
	return nil
}

func testRunnerCacheArtifactFor(runnerSource string, shimSource string, easmModules []*easm.Module, foreignFiles []string, linkFlags []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, enableSMT bool) (testRunnerCacheArtifact, error) {
	hash := sha256.New()
	testRunnerCacheWriteString(hash, fmt.Sprintf("runner-smt=%t", enableSMT))
	if err := writeCommonTestRunnerCacheInputs(hash, easmModules, foreignFiles, linkFlags, optLevel, packedProfile, targetTriple, enableSMT); err != nil {
		return testRunnerCacheArtifact{}, err
	}
	testRunnerCacheWriteBytes(hash, "runner", []byte(runnerSource))
	testRunnerCacheWriteBytes(hash, "shim", []byte(shimSource))
	cacheRoot, err := testRunnerCacheRoot()
	if err != nil {
		return testRunnerCacheArtifact{}, err
	}
	key := hex.EncodeToString(hash.Sum(nil))
	artifactDir := filepath.Join(cacheRoot, key)
	return testRunnerCacheArtifact{key: key, dir: artifactDir, executable: filepath.Join(artifactDir, "runner")}, nil
}

func nativeIncludeDirsFromLinkFlags(linkFlags []string) []string {
	dirs := make([]string, 0, len(linkFlags))
	for index := 0; index < len(linkFlags); index++ {
		flag := strings.TrimSpace(linkFlags[index])
		if flag == "" {
			continue
		}
		switch flag {
		case "-I", "-iquote", "-isystem":
			if index+1 >= len(linkFlags) {
				continue
			}
			index++
			if dir := cleanNativeIncludeDir(linkFlags[index]); dir != "" {
				dirs = append(dirs, dir)
			}
		default:
			for _, prefix := range []string{"-I", "-iquote", "-isystem"} {
				if strings.HasPrefix(flag, prefix) && len(flag) > len(prefix) {
					if dir := cleanNativeIncludeDir(flag[len(prefix):]); dir != "" {
						dirs = append(dirs, dir)
					}
					break
				}
			}
		}
	}
	return dedupeStrings(dirs)
}

func cleanNativeIncludeDir(raw string) string {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		return ""
	}
	cleaned, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	return cleaned
}

var testRunnerCacheQuotedIncludePattern = regexp.MustCompile(`(?m)^\s*#\s*include\s+"([^"]+)"`)

func testRunnerCacheHashForeignFile(hash io.Writer, path string, seen map[string]bool) error {
	cleaned, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return err
	}
	if seen[cleaned] {
		return nil
	}
	seen[cleaned] = true
	data, err := os.ReadFile(cleaned)
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "foreign="+cleaned)
	testRunnerCacheWriteBytes(hash, "foreign-body", data)
	for _, match := range testRunnerCacheQuotedIncludePattern.FindAllSubmatch(data, -1) {
		if len(match) < 2 {
			continue
		}
		includePath := filepath.Clean(filepath.Join(filepath.Dir(cleaned), string(match[1])))
		if _, statErr := os.Stat(includePath); statErr != nil {
			continue
		}
		if err := testRunnerCacheHashForeignFile(hash, includePath, seen); err != nil {
			return err
		}
	}
	return nil
}

func compilerSourceRootForCache() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("failed to locate compiler source root")
	}
	return filepath.Dir(thisFile), nil
}

func testRunnerCacheRoot() (string, error) {
	if root := os.Getenv("ELISA_TEST_RUNNER_CACHE_DIR"); root != "" {
		return root, nil
	}
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		return filepath.Join(base, "elisacore", "test_runners"), nil
	}
	return filepath.Join(os.TempDir(), "elisacore-test-runner-cache"), nil
}

func copyExecutableFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

type testRunnerCacheHashWriter interface {
	Write([]byte) (int, error)
}

func testRunnerCacheHashGoFilesUnder(hash testRunnerCacheHashWriter, root string) error {
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
		if err := testRunnerCacheHashFile(hash, path); err != nil {
			return err
		}
	}
	return nil
}

func testRunnerCacheHashFile(hash testRunnerCacheHashWriter, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	testRunnerCacheWriteBytes(hash, path, content)
	return nil
}

// Typed, length-prefixed records keep arbitrary source bytes from moving field
// boundaries. Changing this format intentionally invalidates old cache keys.
func testRunnerCacheWriteBytes(hash testRunnerCacheHashWriter, label string, content []byte) {
	_, _ = hash.Write(testRunnerCacheFramingBytes[2:3])
	testRunnerCacheWriteLength(hash, uint64(len(label)))
	testRunnerCacheWriteRawString(hash, label)
	testRunnerCacheWriteLength(hash, uint64(len(content)))
	_, _ = hash.Write(content)
}

// Immutable byte views avoid a temporary escaping buffer for each small length.
// io.Writer implementations may neither modify nor retain the provided bytes.
var testRunnerCacheFramingBytes = func() (values [256]byte) {
	for i := range values {
		values[i] = byte(i)
	}
	return
}()

func testRunnerCacheWriteLength(hash testRunnerCacheHashWriter, length uint64) {
	for length >= 128 {
		index := int(length&127) | 128
		_, _ = hash.Write(testRunnerCacheFramingBytes[index : index+1])
		length >>= 7
	}
	index := int(length)
	_, _ = hash.Write(testRunnerCacheFramingBytes[index : index+1])
}

func testRunnerCacheWriteRawString(hash testRunnerCacheHashWriter, value string) {
	_, _ = hash.Write(unsafe.Slice(unsafe.StringData(value), len(value)))
}

func testRunnerCacheWriteString(hash testRunnerCacheHashWriter, value string) {
	_, _ = hash.Write(testRunnerCacheFramingBytes[1:2])
	testRunnerCacheWriteLength(hash, uint64(len(value)))
	// Borrow immutable string bytes only for this synchronous writer call.
	testRunnerCacheWriteRawString(hash, value)
}
