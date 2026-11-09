package main

import (
	"crypto/sha256"
	"elisacore/src/backend"
	"elisacore/src/smt"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
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
//   - the compiler's own identity (compilerSourceStamp — a digest of the running compiler
//     artifact, so any codegen change invalidates every cached object)
//   - host GOOS/GOARCH/Go version and the resolved clang path

func buildCacheEnabled() bool {
	return strings.TrimSpace(os.Getenv("ELISACORE_BUILD_CACHE")) != "0"
}

type buildCacheArtifact struct {
	key         string
	dir         string
	object      string
	optionsKey  string
	environment []string
}

type buildCacheArtifactHandoff struct {
	artifact buildCacheArtifact
}

// defaultSMTProofCacheDir places the persistent proof cache beside the build cache unless
// ELISACORE_SMT_CACHE_DIR chooses one. Its records are keyed by the exact query and the
// compiler, solver and budget identity, so its location never influences a verdict and
// stays out of every cache key.
func defaultSMTProofCacheDir() {
	if _, set := os.LookupEnv("ELISACORE_SMT_CACHE_DIR"); set || !buildCacheEnabled() {
		return
	}
	if root, err := buildCacheRoot(); err == nil {
		_ = os.Setenv("ELISACORE_SMT_CACHE_DIR", filepath.Join(root, "smt-proofs-v1"))
	}
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

func buildCacheOptionsIdentity(options cliOptions) string {
	return fmt.Sprintf("%+v", buildCacheKeyOptions(options))
}

// buildCacheObjectArtifactFor derives the cache artifact for an `-emit obj` build.
// ok=false (no error surfaced) means "not cacheable" — e.g. the source could not
// be expanded — and callers transparently fall back to a normal build.
func buildCacheObjectArtifactFor(options cliOptions) (buildCacheArtifact, bool) {
	program, ok := loadProgramInput(options.filename, io.Discard)
	if !ok {
		return buildCacheArtifact{}, false
	}
	return buildCacheObjectArtifactForProgram(options, program)
}

// Key and compiler consume the same loaded snapshot. Frontend-IR inputs require
// a separate full-bundle identity; resolved source alone cannot identify an AST.
func buildCacheObjectArtifactForProgram(options cliOptions, program *loadedProgram) (buildCacheArtifact, bool) {
	if program == nil || program.file != nil || len(program.easm) != 0 {
		return buildCacheArtifact{}, false
	}
	mergerMayRun := buildCacheMayUseObjectMerger(options)
	var mergerCommand []string
	var mergerExecutable string
	// An explicit ELISA_LD command may wrap or delegate to another executable
	// (for example, `env /custom/ld`). Stamping argv[0] alone cannot detect an
	// in-place change to the delegated linker, so unsafe selectors bypass the
	// complete-object cache.
	if mergerMayRun {
		var trusted bool
		mergerCommand, mergerExecutable, trusted = buildCacheDefaultObjectMergerInputs()
		if !trusted {
			return buildCacheArtifact{}, false
		}
	}
	optionsKey := buildCacheOptionsIdentity(options)
	environment := compilerCacheEnvKey()
	hash := sha256.New()
	if err := writeNativeCacheTarget(hash, options.targetTriple); err != nil {
		return buildCacheArtifact{}, false
	}
	testRunnerCacheWriteString(hash, "build-object-v4-transcript")
	testRunnerCacheWriteString(hash, "goos="+runtime.GOOS)
	testRunnerCacheWriteString(hash, "goarch="+runtime.GOARCH)
	testRunnerCacheWriteString(hash, "goversion="+runtime.Version())
	testRunnerCacheWriteString(hash, "options="+optionsKey)
	for _, entry := range environment {
		testRunnerCacheWriteString(hash, "env="+entry)
	}

	pathInputs, err := json.Marshal(diagnosticPathInputs(program.filename, program.source))
	if err != nil {
		return buildCacheArtifact{}, false
	}
	testRunnerCacheWriteBytes(hash, "diagnostic-path-inputs", pathInputs)
	testRunnerCacheWriteBytes(hash, "program-source", program.source)
	testRunnerCacheWriteString(hash, "program-filename="+program.filename)
	origins := append([]string(nil), program.sourceFiles...)
	sort.Strings(origins)
	for _, origin := range origins {
		testRunnerCacheWriteString(hash, "source-authority="+origin)
	}

	// The running compiler and the selected solver are independent native inputs.
	// Hash them concurrently, while keeping every stamp and its original order in
	// the key. Always join the worker before returning, including on error.
	type solverStampResult struct {
		selector string
		selected string
		stamp    string
		err      error
	}
	var solverDone chan solverStampResult
	if options.enableSMT {
		solverDone = make(chan solverStampResult, 1)
		go func() {
			result := solverStampResult{selector: smt.ResolveBinary("")}
			if selected, err := exec.LookPath(result.selector); err == nil {
				result.selected = selected
				result.stamp, result.err = toolchainNativeContentStamp(selected)
			}
			solverDone <- result
		}()
		defer func() {
			if solverDone != nil {
				<-solverDone
			}
		}()
	}

	stamp, err := compilerSourceStamp()
	if err != nil {
		return buildCacheArtifact{}, false
	}
	testRunnerCacheWriteString(hash, "compiler-stamp="+stamp)

	if clangPath, err := exec.LookPath("clang"); err == nil {
		// A launcher script can delegate to a compiler whose replacement is not
		// represented by this file's digest. Outer-cache certificates apply the
		// same native-image requirement to selected tools.
		testRunnerCacheWriteString(hash, "clang="+clangPath)
		stamp, stampErr := toolchainNativeContentStamp(clangPath)
		if stampErr != nil {
			return buildCacheArtifact{}, false
		}
		testRunnerCacheWriteString(hash, "clang-content="+stamp)
	} else {
		testRunnerCacheWriteString(hash, "clang=none")
	}

	// SMT can change proof diagnostics and strict-proof acceptance even when
	// source and command spelling are unchanged. Never execute a cached query
	// closure under an unstamped replacement solver.
	if solverDone != nil {
		result := <-solverDone
		solverDone = nil
		if result.err != nil {
			return buildCacheArtifact{}, false
		}
		testRunnerCacheWriteString(hash, "smt-selector="+result.selector)
		if result.selected != "" {
			testRunnerCacheWriteString(hash, "smt-executable="+result.selected)
			testRunnerCacheWriteString(hash, "smt-content="+result.stamp)
		} else {
			testRunnerCacheWriteString(hash, "smt-executable=unavailable")
		}
	}

	// Partitioned IR, native fragments, and the ordinary parallel object emitter
	// merge objects with the request's selected linker. The environment key
	// records ELISA_LD's spelling, but cannot detect an executable replaced in
	// place or a different `ld` selected through PATH. The persistent object
	// cache can return before backend partition keys are consulted, so include
	// the resolved command and strong executable digest whenever a merge path may
	// run. O0 partitioned IR is enabled unless explicitly disabled; object shape
	// determines whether it crosses the partition threshold, so conservatively
	// include the merger for those requests.
	if mergerMayRun {
		stamp, err := toolchainNativeContentStamp(mergerExecutable)
		if err != nil {
			return buildCacheArtifact{}, false
		}
		identity, err := json.Marshal(struct {
			Command    []string
			Executable string
			Digest     string
		}{Command: mergerCommand, Executable: mergerExecutable, Digest: stamp})
		if err != nil {
			return buildCacheArtifact{}, false
		}
		testRunnerCacheWriteBytes(hash, "native-fragment-merger", identity)
	}

	root, err := buildCacheRoot()
	if err != nil {
		return buildCacheArtifact{}, false
	}
	key := hex.EncodeToString(hash.Sum(nil))
	dir := filepath.Join(root, key)
	return buildCacheArtifact{
		key:         key,
		dir:         dir,
		object:      filepath.Join(dir, "out.o"),
		optionsKey:  optionsKey,
		environment: environment,
	}, true
}

func buildCacheObjectArtifactForProgramOrHandoff(options cliOptions, program *loadedProgram) (buildCacheArtifact, bool) {
	if program != nil && program.cacheHandoff != nil && program.file == nil && len(program.easm) == 0 {
		artifact := program.cacheHandoff.artifact
		root, err := buildCacheRoot()
		if err == nil && artifact.dir == filepath.Join(root, artifact.key) &&
			artifact.optionsKey == buildCacheOptionsIdentity(options) &&
			buildCacheEnvironmentEqual(artifact.environment, compilerCacheEnvKey()) {
			return artifact, true
		}
	}
	return buildCacheObjectArtifactForProgram(options, program)
}

func buildCacheEnvironmentEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func buildCacheMayUseObjectMerger(options cliOptions) bool {
	if !buildCacheEnabled() {
		return false
	}
	// writeObjectFile also attempts the ordinary partition-and-merge emitter at
	// O0 and after whole-module optimization whenever multiple codegen jobs are
	// available. The cache key precedes module-weight checks, so conservatively
	// include the selected merger for either path.
	if buildCacheCodegenJobs() > 1 {
		return true
	}
	if effectiveOptimizationLevel(options) == backend.OptimizationLevel0 {
		return os.Getenv("ELISACORE_PARALLEL_IR") != "0"
	}
	return (os.Getenv("ELISACORE_PARTITION_CACHE") != "0" &&
		os.Getenv("ELISACORE_NATIVE_FRAGMENTS") == "1") ||
		strings.TrimSpace(os.Getenv("ELISACORE_PARALLEL_OPT")) == "1"
}

// Keep this in step with backend.codegenJobs: default min(NumCPU, 16), explicit
// values clamped to 1..64, and invalid values falling back to the default.
func buildCacheCodegenJobs() int {
	if text := strings.TrimSpace(os.Getenv("ELISACORE_CODEGEN_JOBS")); text != "" {
		if jobs, err := strconv.Atoi(text); err == nil {
			if jobs < 1 {
				return 1
			}
			if jobs > 64 {
				return 64
			}
			return jobs
		}
	}
	jobs := runtime.NumCPU()
	if jobs > 16 {
		jobs = 16
	}
	if jobs < 1 {
		jobs = 1
	}
	return jobs
}

// buildCacheDefaultObjectMergerInputs admits only the platform linker selected
// through the system path. A user PATH launcher can delegate to another ld that
// is not represented by ObjectMergerCacheInputs' argv[0] digest. Explicit
// ELISA_LD commands are also rejected because their arguments may select such a
// delegate or supply linker scripts.
func buildCacheDefaultObjectMergerInputs() (command []string, executable string, trusted bool) {
	if strings.TrimSpace(os.Getenv("ELISA_LD")) != "" {
		return nil, "", false
	}
	selected, err := exec.LookPath("ld")
	if err != nil || filepath.Base(selected) != "ld" {
		return nil, "", false
	}
	resolved, err := filepath.EvalSymlinks(selected)
	if err != nil {
		return nil, "", false
	}
	switch runtime.GOOS {
	case "darwin":
		// The backend resolves Apple's dispatch shim through xcrun only when the
		// selected command is this canonical path.
		if filepath.Clean(selected) != "/usr/bin/ld" || filepath.Clean(resolved) != "/usr/bin/ld" {
			return nil, "", false
		}
	case "linux":
		if !buildCachePathWithin(resolved, "/usr/bin") && !buildCachePathWithin(resolved, "/bin") {
			return nil, "", false
		}
	default:
		return nil, "", false
	}
	if _, err := toolchainNativeContentStamp(selected); err != nil {
		return nil, "", false
	}
	// On macOS the actual linker is the xcrun-selected toolchain binary, not
	// /usr/bin/ld. Validate that file too before admitting the complete object.
	command, executable, err = backend.NewIncrementalObjectSession(1, 1).ObjectMergerCacheInputs()
	if err != nil {
		return nil, "", false
	}
	if _, err := toolchainNativeContentStamp(executable); err != nil {
		return nil, "", false
	}
	return command, executable, true
}

func buildCachePathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}
