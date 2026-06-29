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
)

// Content-addressed build cache for `-emit obj`. An object file produced from the
// same include-expanded source, codegen-affecting flags, toolchain, and compiler
// build is byte-identical, so we key on those and serve a cached copy — skipping
// parse, semantic analysis, and codegen entirely on a hit. This is the GOCACHE
// model and the foundation for incremental builds (docs/117); it generalizes the
// runtime-object cache (runtime_object_cache.go) from the runtime object to the
// user's program object, and reuses that file's hashing/publish machinery.
//
// Correctness rests on the key capturing everything that can change the output:
//   - the full expanded source (readSourceWithIncludes — root + transitive includes)
//   - every CLI option except the few that provably cannot affect object CONTENT
//     (output path, server addr, test filter); over-keying only costs cache reuse,
//     under-keying would serve a stale object, so we err toward over-keying
//   - the compiler's own identity (compilerSourceStamp — a digest of all compiler
//     .go sources, so any codegen change invalidates every cached object)
//   - host GOOS/GOARCH/Go version and the resolved clang path

func buildCacheEnabled() bool {
	return strings.TrimSpace(os.Getenv("ELISACORE_BUILD_CACHE")) != "0"
}

type buildCacheArtifact struct {
	key    string
	dir    string
	object string
}

func buildCacheRoot() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ELISAC_BUILD_CACHE_DIR")); override != "" {
		return override, nil
	}
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		return filepath.Join(base, "elisacore", "build_objects"), nil
	}
	return filepath.Join(os.TempDir(), "elisacore-build-object-cache"), nil
}

// isPureObjectEmit reports whether the build is a plain `-emit obj` (no native
// link/run step). Only this path is cached for now; the link/exe path layers on
// top later (docs/117).
func isPureObjectEmit(options cliOptions) bool {
	return options.emit == emitObject && !options.linkNative && !options.runNative
}

// buildCacheKeyOptions zeroes the cliOptions fields that cannot influence the
// emitted object's content, so builds that differ only in (say) the output path
// still share a cache entry. Everything else is folded into the key verbatim —
// any codegen-affecting flag, present or future, is captured by default.
func buildCacheKeyOptions(options cliOptions) cliOptions {
	options.output = ""
	options.addr = ""
	options.filter = ""
	return options
}

// buildCacheObjectArtifactFor derives the cache artifact for an `-emit obj` build.
// ok=false (no error surfaced) means "not cacheable" — e.g. the source could not
// be expanded — and callers transparently fall back to a normal build.
func buildCacheObjectArtifactFor(options cliOptions) (buildCacheArtifact, bool) {
	hash := sha256.New()
	testRunnerCacheWriteString(hash, "build-object-v1")
	testRunnerCacheWriteString(hash, "goos="+runtime.GOOS)
	testRunnerCacheWriteString(hash, "goarch="+runtime.GOARCH)
	testRunnerCacheWriteString(hash, "goversion="+runtime.Version())
	testRunnerCacheWriteString(hash, fmt.Sprintf("options=%+v", buildCacheKeyOptions(options)))

	source, err := readSourceWithIncludes(options.filename, map[string]bool{})
	if err != nil {
		return buildCacheArtifact{}, false
	}
	testRunnerCacheWriteBytes(hash, "program-source", source)

	stamp, err := compilerSourceStamp()
	if err != nil {
		return buildCacheArtifact{}, false
	}
	testRunnerCacheWriteString(hash, "compiler-stamp="+stamp)

	if clangPath, err := exec.LookPath("clang"); err == nil {
		testRunnerCacheWriteString(hash, "clang="+clangPath)
	} else {
		testRunnerCacheWriteString(hash, "clang=none")
	}

	root, err := buildCacheRoot()
	if err != nil {
		return buildCacheArtifact{}, false
	}
	key := hex.EncodeToString(hash.Sum(nil))
	dir := filepath.Join(root, key)
	return buildCacheArtifact{key: key, dir: dir, object: filepath.Join(dir, "out.o")}, true
}

// publishCachedBuildObject atomically installs a freshly built object into the
// cache (stage into a temp dir, then rename into place). A concurrent publisher
// that already won the race leaves the object present, which we treat as success.
func publishCachedBuildObject(artifact buildCacheArtifact, builtObject string) error {
	if artifact.dir == "" || artifact.object == "" || strings.TrimSpace(builtObject) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(artifact.dir), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(artifact.object); err == nil {
		return nil
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(artifact.dir), ".elisa-build-object-stage-*")
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

func debugBuildCache(stderr io.Writer, status string, artifact buildCacheArtifact) {
	if stderr == nil || !testRunnerCacheDebugEnabled() {
		return
	}
	prefix := artifact.key
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	fmt.Fprintf(stderr, "[ cache    ] build-object %s key=%s obj=%s\n", status, prefix, artifact.object)
}

// tryBuildObjectCache implements the cache around a pure `-emit obj` build. It
// returns (exitCode, handled): when handled is true the caller must return
// exitCode immediately; when false the caller proceeds with a normal build.
// On a miss it performs the build itself so it can publish the result.
func tryBuildObjectCache(options cliOptions, stdout io.Writer, stderr io.Writer) (int, bool) {
	if !buildCacheEnabled() || !isPureObjectEmit(options) {
		return 0, false
	}
	artifact, ok := buildCacheObjectArtifactFor(options)
	if !ok {
		return 0, false
	}
	outputPath := outputPathForEmit(options.filename, options.output, ".o")

	if _, err := os.Stat(artifact.object); err == nil {
		if err := ensureOutputParentExists(outputPath); err == nil {
			if err := copyExecutableFile(artifact.object, outputPath); err == nil {
				debugBuildCache(stderr, "hit", artifact)
				return 0, true
			}
		}
		// Any copy/parent failure: fall back to a normal build below.
	}

	program, okLoad := loadProgramInput(options.filename, stderr)
	if !okLoad {
		return 1, true
	}
	rc := runLoadedProgramWithOptions(options, program, stdout, stderr)
	if rc == 0 {
		if err := publishCachedBuildObject(artifact, outputPath); err != nil {
			debugBuildCache(stderr, "store-failed", artifact)
		} else {
			debugBuildCache(stderr, "miss-store", artifact)
		}
	}
	return rc, true
}
