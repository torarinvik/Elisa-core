package main

import (
	"bytes"
	"crypto/sha256"
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
	"strings"

	"elisacore/src/backend"
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
	optionsKey := buildCacheOptionsIdentity(options)
	environment := compilerCacheEnvKey()
	hash := sha256.New()
	if err := writeNativeCacheTarget(hash, options.targetTriple); err != nil {
		return buildCacheArtifact{}, false
	}
	testRunnerCacheWriteString(hash, "build-object-v3-transcript")
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

	stamp, err := compilerSourceStamp()
	if err != nil {
		return buildCacheArtifact{}, false
	}
	testRunnerCacheWriteString(hash, "compiler-stamp="+stamp)

	if clangPath, err := exec.LookPath("clang"); err == nil {
		testRunnerCacheWriteString(hash, "clang="+clangPath)
		stamp, stampErr := toolchainContentStamp(clangPath)
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
	if options.enableSMT {
		solver := smt.ResolveBinary("")
		testRunnerCacheWriteString(hash, "smt-selector="+solver)
		if selected, err := exec.LookPath(solver); err == nil {
			stamp, err := toolchainContentStamp(selected)
			if err != nil {
				return buildCacheArtifact{}, false
			}
			testRunnerCacheWriteString(hash, "smt-executable="+selected)
			testRunnerCacheWriteString(hash, "smt-content="+stamp)
		} else {
			testRunnerCacheWriteString(hash, "smt-executable=unavailable")
		}
	}

	// Native fragments merge their objects with the request's selected linker.
	// The environment key records ELISA_LD's spelling, but cannot detect an
	// executable replaced in place or a different `ld` selected through PATH.
	// The persistent object cache can return before the backend's fragment key is
	// consulted, so include the resolved command and strong executable digest in
	// the outer key whenever that path can run.
	if buildCacheUsesNativeFragmentMerger(options) {
		merger := backend.NewIncrementalObjectSession(1, 1)
		command, executable, err := merger.ObjectMergerCacheInputs()
		if err != nil {
			return buildCacheArtifact{}, false
		}
		stamp, err := toolchainContentStamp(executable)
		if err != nil {
			return buildCacheArtifact{}, false
		}
		identity, err := json.Marshal(struct {
			Command    []string
			Executable string
			Digest     string
		}{Command: command, Executable: executable, Digest: stamp})
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

func buildCacheUsesNativeFragmentMerger(options cliOptions) bool {
	return buildCacheEnabled() &&
		os.Getenv("ELISACORE_PARTITION_CACHE") != "0" &&
		os.Getenv("ELISACORE_NATIVE_FRAGMENTS") == "1" &&
		effectiveOptimizationLevel(options) != backend.OptimizationLevel0
}

// publishCachedBuildObject atomically installs a freshly built object into the
// cache (stage into a temp dir, then rename into place). A concurrent publisher
// that already won the race leaves the object present, which we treat as success.
func publishCachedBuildObject(artifact buildCacheArtifact, builtObject string, transcript buildCacheTranscript) error {
	if artifact.dir == "" || artifact.object == "" || strings.TrimSpace(builtObject) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(artifact.dir), 0o755); err != nil {
		return err
	}
	if existing, valid := readBuildCacheTranscript(artifact); valid && buildCacheObjectMatches(artifact, existing) {
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
	objectHash, err := buildCacheObjectHash(stageObject)
	if err != nil {
		return err
	}
	transcript.ObjectSHA256 = objectHash
	transcript.Key = artifact.key
	transcript.StdoutSHA256 = buildCacheStreamHash(transcript.Stdout)
	transcript.StderrSHA256 = buildCacheStreamHash(transcript.Stderr)
	metadata, err := json.Marshal(transcript)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "transcript.json"), metadata, 0600); err != nil {
		return err
	}
	// Missing/corrupt metadata cannot authorize a hit. Remove only this cache
	// entry so the atomic directory publication can repair it.
	if existing, valid := readBuildCacheTranscript(artifact); !valid || !buildCacheObjectMatches(artifact, existing) {
		_ = os.RemoveAll(artifact.dir)
	}
	if err := os.Rename(stagingDir, artifact.dir); err != nil {
		if existing, valid := readBuildCacheTranscript(artifact); valid && buildCacheObjectMatches(artifact, existing) {
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
	return tryBuildObjectCacheWith(options, stdout, stderr, loadProgramInput, runLoadedProgramWithOptions)
}

// The callbacks make snapshot ownership testable without running a native tool.
func tryBuildObjectCacheWith(options cliOptions, stdout, stderr io.Writer, load func(string, io.Writer) (*loadedProgram, bool), build func(cliOptions, *loadedProgram, io.Writer, io.Writer) int) (int, bool) {
	if !buildCacheEnabled() || !isPureObjectEmit(options) || buildCacheRequestObserverEnabled() {
		return 0, false
	}
	program, okLoad := load(options.filename, stderr)
	if !okLoad {
		return 1, true
	}
	artifact, ok := buildCacheObjectArtifactForProgramOrHandoff(options, program)
	if !ok {
		return 0, false
	}
	outputPath := outputPathForEmit(options.filename, options.output, ".o")

	if code, hit := restoreCachedBuild(options, artifact, stdout, stderr); hit {
		return code, true
	}

	var capturedOut, capturedErr buildCacheCapture
	capturedStatus := &analysisCommandStatus{Writer: io.MultiWriter(stderr, &capturedErr)}
	rc := build(options, program, io.MultiWriter(stdout, &capturedOut), capturedStatus)
	if capturedStatus.incomplete {
		rc = analysisIncompleteExitCode
	}
	if rc == 0 && !program.objectCacheIneligible && !capturedOut.overflow && !capturedErr.overflow {
		current, valid := buildCacheObjectArtifactForProgram(options, program)
		if !valid || current.key != artifact.key {
			return rc, true
		}
		if err := publishCachedBuildObject(artifact, outputPath, buildCacheTranscript{Stdout: capturedOut.Bytes(), Stderr: capturedErr.Bytes()}); err != nil {
			debugBuildCache(stderr, "store-failed", artifact)
		} else {
			debugBuildCache(stderr, "miss-store", artifact)
		}
	}
	return rc, true
}

// buildCacheEnvIgnored lists controls that do not change the supported pure-object
// result or its semantic transcript. Per-request observers bypass cache admission.
// Warning policy and every other compiler variable remain part of the key.
var buildCacheEnvIgnored = map[string]bool{
	"ELISA_SMT_RLIMIT_LOG":                    true,
	"ELISA_SMT_TRACE":                         true,
	"ELISACORE_BUILD_CACHE":                   true,
	"ELISACORE_ANALYSIS_CACHE_DEBUG":          true,
	"ELISACORE_ANALYSIS_BOOTSTRAP":            true,
	"ELISACORE_ANALYSIS_BOOTSTRAP_DECLS":      true,
	"ELISACORE_ANALYSIS_BOOTSTRAP_SECONDS":    true,
	"ELISACORE_RETAINED_SERVER":               true,
	"ELISACORE_RETAINED_IDLE":                 true,
	"ELISACORE_RETAINED_AUTO_MIN":             true,
	"ELISAC_BUILD_CACHE_DIR":                  true,
	"ELISACORE_SMT_CACHE_DIR":                 true,
	"ELISACORE_BUILD_TIMING":                  true,
	"ELISACORE_CACHE_DEBUG":                   true,
	"ELISACORE_CPUPROFILE":                    true,
	"ELISACORE_CPUPROFILE_SECONDS":            true,
	"ELISACORE_MEMPROFILE":                    true,
	"ELISACORE_MEMPROFILE_SECONDS":            true,
	"ELISACORE_GOROUTINEPROFILE":              true,
	"ELISACORE_BLOCKPROFILE":                  true,
	"ELISACORE_KEEP_TEST_TEMP":                true,
	"ELISACORE_TEST_CACHE":                    true,
	"ELISACORE_TEST_CACHE_DEBUG":              true,
	"ELISACORE_TEST_PHASE_DEBUG":              true,
	"ELISACORE_TEST_TIMING":                   true,
	"ELISACORE_LLVM_PASS_DEBUG":               true,
	"ELISACORE_NO_OBJCACHE":                   true,
	"ELISACORE_RUNTIME_OBJECT_CACHE":          true,
	"ELISACORE_EMIT_LL":                       true,
	"ELISACORE_DUMP_MODULE_ON_VERIFY_FAILURE": true,
	"ELISA_KEEP_TEST_BINARY":                  true,
	"ELISA_BACKEND_PARITY_OUT":                true,
	"ELISA_SEMANTIC_INTERNAL_PARITY_OUT":      true,
	"ELISA_DUMP_DEATHTIME":                    true,
	"ELISA_DUMP_REGION_STACKS":                true,
	"ELISA_REGION_DEBUG":                      true,
}

// buildCacheEnvKey returns the sorted NAME=VALUE pairs of every ELISA_/ELISACORE_ variable
// that can influence code generation. Most codegen flags (-ffast-math, -fstrict-fp,
// -fnoalias, -fbounds-check, -mcpu, ...) reach the backend ONLY through the environment, so
// a key built from cliOptions alone served e.g. a non-strict-FP object to a -fstrict-fp
// build. A deny-list (not an allow-list) keeps any future env-driven flag keyed by default.
func buildCacheEnvKey() []string {
	var entries []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "ELISACORE_") && !strings.HasPrefix(name, "ELISA_") {
			continue
		}
		if buildCacheEnvIgnored[name] {
			continue
		}
		entries = append(entries, kv)
	}
	sort.Strings(entries)
	return entries
}

// compilerCacheEnvKey excludes settings consumed only by the Stage1 wrapper.
// Test-result caching deliberately keeps them: executed programs may read their
// environment even when the compiler itself does not.
func compilerCacheEnvKey() []string {
	ignored := map[string]bool{
		"ELISA_STAGE1_SEED_MAX_RSS_KB": true, "ELISA_STAGE1_SEED_OPT_LEVEL": true,
		"ELISA_STAGE1_SEED_BOOTSTRAP_BIN": true, "ELISA_STAGE1_SEED_BOOTSTRAP_KIND": true,
		"ELISA_STAGE1_SEED_CLANG": true, "ELISA_STAGE0_SOURCE_ROOT": true,
	}
	entries := buildCacheEnvKey()
	filtered := entries[:0]
	for _, entry := range entries {
		name, _, _ := strings.Cut(entry, "=")
		if !ignored[name] {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// These modes observe this invocation or publish files outside the object
// artifact. Replaying an earlier invocation cannot satisfy those effects.
// Profile durations and goroutine/block destinations are dormant without their
// CPU/memory parent profile; those parent controls already require this bypass.
func buildCacheRequestObserverEnabled() bool {
	if analysisDiskRequestObserverEnabled() {
		return true
	}
	if os.Getenv("ELISACORE_ANALYSIS_CACHE_DEBUG") == "1" {
		return true
	}
	for _, name := range []string{
		"ELISACORE_BUILD_TIMING",
		"ELISACORE_LLVM_PASS_DEBUG",
		"ELISACORE_DUMP_MODULE_ON_VERIFY_FAILURE",
		"ELISACORE_CPUPROFILE",
		"ELISACORE_MEMPROFILE",
		"ELISACORE_EMIT_LL",
		"ELISACORE_LEXER_CAPACITY_REPORT",
		"ELISACORE_RETAINED_DEBUG_SPANS",
		"ELISACORE_RETAINED_FAST_DEBUG",
		"ELISA_BACKEND_PARITY_OUT",
	} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

// restoreCachedBuild is shared by the CLI's pre-server lookup and normal builds.
// It never compiles or starts a server on a miss.
func restoreCachedBuild(options cliOptions, artifact buildCacheArtifact, stdout, stderr io.Writer) (int, bool) {
	outputPath := outputPathForEmit(options.filename, options.output, ".o")
	transcript, validTranscript := readBuildCacheTranscript(artifact)
	if validTranscript && usableCachedFile(artifact.object, false) {
		if err := ensureOutputParentExists(outputPath); err == nil {
			if err := restoreBuildCacheObject(artifact, outputPath, transcript); err == nil {
				if _, err := io.Copy(stdout, bytes.NewReader(transcript.Stdout)); err != nil {
					return 1, true
				}
				if _, err := io.Copy(stderr, bytes.NewReader(transcript.Stderr)); err != nil {
					return 1, true
				}
				debugBuildCache(stderr, "hit", artifact)
				return 0, true
			}
		}
		// Any copy/parent failure: fall back to a normal build.
	}

	return 0, false
}

// tryBuildObjectCacheHit handles an existing disk artifact before daemon discovery.
// parseArgs applies codegen flags to the environment, just as the ordinary CLI does.
func tryBuildObjectCacheHit(args []string, stdout, stderr io.Writer) (int, bool) {
	code, handled, _ := tryBuildObjectCacheHitWithProgram(args, stdout, stderr)
	return code, handled
}

// A cache miss hands its exact loaded source snapshot to the local build. The
// snapshot belongs only to this invocation; successful forwarding discards it.
func tryBuildObjectCacheHitWithProgram(args []string, stdout, stderr io.Writer) (int, bool, *loadedProgram) {
	return tryBuildObjectCacheHitUsing(args, stdout, stderr, loadProgramInput)
}

func tryBuildObjectCacheHitUsing(args []string, stdout, stderr io.Writer, load func(string, io.Writer) (*loadedProgram, bool)) (int, bool, *loadedProgram) {
	if !buildCacheEnabled() || buildCacheRequestObserverEnabled() || len(args) == 0 || isProjectCommand(args[0]) {
		return 0, false, nil
	}
	options, err := parseArgs(args)
	if err != nil || !isPureObjectEmit(options) {
		return 0, false, nil
	}
	if configureCLIProfile(options) != nil {
		return 0, false, nil
	}
	program, loaded := load(options.filename, io.Discard)
	if !loaded {
		return 0, false, nil
	}
	artifact, ok := buildCacheObjectArtifactForProgram(options, program)
	if !ok {
		return 0, false, nil
	}
	if code, hit := restoreCachedBuild(options, artifact, stdout, stderr); hit {
		return code, true, nil
	}
	program.cacheHandoff = &buildCacheArtifactHandoff{artifact: artifact}
	return 0, false, program
}
