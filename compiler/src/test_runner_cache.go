package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"elisacore/src/backend"
)

type testRunnerCacheArtifact struct {
	key        string
	dir        string
	executable string
}

func testRunnerCacheEnabled() bool {
	return strings.TrimSpace(os.Getenv("ELISACORE_TEST_CACHE")) != "0"
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

func locateCachedTestRunner(runnerSource string, shimSource string, foreignFiles []string, linkFlags []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string) (testRunnerCacheArtifact, bool, error) {
	artifact, err := testRunnerCacheArtifactFor(runnerSource, shimSource, foreignFiles, linkFlags, optLevel, packedProfile, targetTriple)
	if err != nil {
		return testRunnerCacheArtifact{}, false, err
	}
	if artifact.executable == "" {
		return artifact, false, nil
	}
	if _, err := os.Stat(artifact.executable); err == nil {
		return artifact, true, nil
	} else if !os.IsNotExist(err) {
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
	if _, err := os.Stat(artifact.executable); err == nil {
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
		if _, statErr := os.Stat(artifact.executable); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func testRunnerCacheArtifactFor(runnerSource string, shimSource string, foreignFiles []string, linkFlags []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string) (testRunnerCacheArtifact, error) {
	hash := sha256.New()
	testRunnerCacheWriteString(hash, "goos="+runtime.GOOS)
	testRunnerCacheWriteString(hash, "goarch="+runtime.GOARCH)
	testRunnerCacheWriteString(hash, "goversion="+runtime.Version())
	testRunnerCacheWriteString(hash, fmt.Sprintf("opt=%d", optLevel))
	testRunnerCacheWriteString(hash, "packedProfile="+packedProfile.SelectionKey())
	testRunnerCacheWriteString(hash, "targetTriple="+strings.TrimSpace(targetTriple))
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return testRunnerCacheArtifact{}, err
	}
	testRunnerCacheWriteString(hash, "clang="+clangPath)
	testRunnerCacheWriteBytes(hash, "runner", []byte(runnerSource))
	testRunnerCacheWriteBytes(hash, "shim", []byte(shimSource))
	resolvedForeignFiles, err := withDefaultNativeRuntimeForeignFiles(foreignFiles)
	if err != nil {
		return testRunnerCacheArtifact{}, err
	}
	for _, foreignFile := range resolvedForeignFiles {
		trimmed := strings.TrimSpace(foreignFile)
		if trimmed == "" {
			continue
		}
		// Foreign bridge files often include the real implementation with quoted
		// includes. Hash the expanded include graph so runtime changes do not hide
		// behind stale cached test runners.
		foreignSource, readErr := readSourceWithIncludes(trimmed, map[string]bool{})
		if readErr != nil {
			return testRunnerCacheArtifact{}, readErr
		}
		testRunnerCacheWriteBytes(hash, "foreign-with-includes:"+trimmed, foreignSource)
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
			return testRunnerCacheArtifact{}, readErr
		}
		testRunnerCacheWriteBytes(hash, "elisacore-runtime-support", runtimeSource)
	} else {
		return testRunnerCacheArtifact{}, err
	}
	compilerRoot, err := compilerSourceRootForCache()
	if err != nil {
		return testRunnerCacheArtifact{}, err
	}
	if err := testRunnerCacheHashGoFilesUnder(hash, compilerRoot); err != nil {
		return testRunnerCacheArtifact{}, err
	}
	cacheRoot, err := testRunnerCacheRoot()
	if err != nil {
		return testRunnerCacheArtifact{}, err
	}
	key := hex.EncodeToString(hash.Sum(nil))
	artifactDir := filepath.Join(cacheRoot, key)
	return testRunnerCacheArtifact{key: key, dir: artifactDir, executable: filepath.Join(artifactDir, "runner")}, nil
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

func testRunnerCacheWriteBytes(hash testRunnerCacheHashWriter, label string, content []byte) {
	testRunnerCacheWriteString(hash, label)
	_, _ = hash.Write(content)
	_, _ = hash.Write([]byte{0})
}

func testRunnerCacheWriteString(hash testRunnerCacheHashWriter, value string) {
	_, _ = hash.Write([]byte(value))
	_, _ = hash.Write([]byte{0})
}
