package main

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
	"sync"

	"elisacore/src/backend"
)

// The default elisacore runtime support object (native_runtime_support.elisa, include-
// expanded) is identical across every native/test build that shares the same runtime
// source, packed profile, target triple, toolchain, and compiler build. It was previously
// re-parsed, re-analyzed, and re-compiled at -O3 on *every* build (see
// writeDefaultElisaCoreRuntimeObject), which dominated per-test compile time. This is a
// content-addressed cache of that object, mirroring the test-runner cache machinery in
// test_runner_cache.go (same enable flag, same cache-root convention, same atomic
// staging-then-rename publish that is safe under the parallel `t.Parallel()` test suite).
// docs/93-runtime-object-cache.md.

// runtimeObjectCacheEnabled gates the runtime-object cache independently of the outer
// test-runner cache, so it can be A/B measured (ELISACORE_RUNTIME_OBJECT_CACHE=0 disables).
// Default on.
func runtimeObjectCacheEnabled() bool {
	return strings.TrimSpace(os.Getenv("ELISACORE_RUNTIME_OBJECT_CACHE")) != "0"
}

type runtimeObjectCacheArtifact struct {
	key    string
	dir    string
	object string
}

// compilerSourceStampOnce memoizes the digest of all compiler .go sources. The compiler
// binary cannot change mid-process, so this is computed once and reused for the cache key
// (a codegen change rebuilds the binary -> different stamp -> stale objects invalidated).
var (
	compilerSourceStampOnce  sync.Once
	compilerSourceStampValue string
	compilerSourceStampErr   error
)

func compilerSourceStamp() (string, error) {
	compilerSourceStampOnce.Do(func() {
		root, err := compilerSourceRootForCache()
		if err != nil {
			compilerSourceStampErr = err
			return
		}
		hash := sha256.New()
		if err := testRunnerCacheHashGoFilesUnder(hash, root); err != nil {
			compilerSourceStampErr = err
			return
		}
		compilerSourceStampValue = hex.EncodeToString(hash.Sum(nil))
	})
	return compilerSourceStampValue, compilerSourceStampErr
}

func runtimeObjectCacheArtifactFor(packedProfile backend.PackedLoweringProfile, targetTriple string) (runtimeObjectCacheArtifact, error) {
	hash := sha256.New()
	testRunnerCacheWriteString(hash, "runtime-object-v1")
	testRunnerCacheWriteString(hash, "goos="+runtime.GOOS)
	testRunnerCacheWriteString(hash, "goarch="+runtime.GOARCH)
	testRunnerCacheWriteString(hash, "goversion="+runtime.Version())
	// The runtime object is always emitted at -O3, no debug/trace info (see
	// writeDefaultElisaCoreRuntimeObject). Pin those into the key so it stays correct if
	// that ever changes.
	testRunnerCacheWriteString(hash, fmt.Sprintf("opt=%d", backend.OptimizationLevel3))
	testRunnerCacheWriteString(hash, "debug=false")
	testRunnerCacheWriteString(hash, "trace=false")
	testRunnerCacheWriteString(hash, "packedProfile="+packedProfile.SelectionKey())
	testRunnerCacheWriteString(hash, "targetTriple="+strings.TrimSpace(targetTriple))
	if clangPath, err := exec.LookPath("clang"); err == nil {
		testRunnerCacheWriteString(hash, "clang="+clangPath)
	} else {
		// No clang: the fallback backend emitter path is used instead. Distinguish the key
		// so a clang/no-clang environment swap cannot collide.
		testRunnerCacheWriteString(hash, "clang=none")
	}
	runtimePath, err := defaultElisaCoreRuntimeSupportPath()
	if err != nil {
		return runtimeObjectCacheArtifact{}, err
	}
	runtimeSource, err := readSourceWithIncludes(runtimePath, map[string]bool{})
	if err != nil {
		return runtimeObjectCacheArtifact{}, err
	}
	testRunnerCacheWriteBytes(hash, "elisacore-runtime-support", runtimeSource)
	stamp, err := compilerSourceStamp()
	if err != nil {
		return runtimeObjectCacheArtifact{}, err
	}
	testRunnerCacheWriteString(hash, "compiler-stamp="+stamp)

	cacheRoot, err := runtimeObjectCacheRoot()
	if err != nil {
		return runtimeObjectCacheArtifact{}, err
	}
	key := hex.EncodeToString(hash.Sum(nil))
	dir := filepath.Join(cacheRoot, key)
	return runtimeObjectCacheArtifact{key: key, dir: dir, object: filepath.Join(dir, "elisacore_runtime.o")}, nil
}

func runtimeObjectCacheRoot() (string, error) {
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		return filepath.Join(base, "elisacore", "runtime_objects"), nil
	}
	return filepath.Join(os.TempDir(), "elisacore-runtime-object-cache"), nil
}

// publishCachedRuntimeObject atomically installs a freshly built runtime object into the
// cache: copy into a staging dir, then rename the dir into place. A concurrent publisher
// that already won the race leaves the object present, which we treat as success.
func publishCachedRuntimeObject(artifact runtimeObjectCacheArtifact, builtObject string) error {
	if artifact.dir == "" || artifact.object == "" || strings.TrimSpace(builtObject) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(artifact.dir), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(artifact.object); err == nil {
		return nil
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(artifact.dir), ".elisa-runtime-object-stage-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()
	stageObject := filepath.Join(stagingDir, filepath.Base(artifact.object))
	if err := copyExecutableFile(builtObject, stageObject); err != nil {
		return err
	}
	if err := os.Rename(stagingDir, artifact.dir); err != nil {
		if _, statErr := os.Stat(artifact.object); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func debugRuntimeObjectCache(stderr io.Writer, status string, artifact runtimeObjectCacheArtifact) {
	if stderr == nil || !testRunnerCacheDebugEnabled() {
		return
	}
	prefix := artifact.key
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	fmt.Fprintf(stderr, "[ cache    ] runtime-object %s key=%s obj=%s\n", status, prefix, artifact.object)
}
