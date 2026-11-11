package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
	if len(transcript.Stdout) > buildCacheStreamLimit || len(transcript.Stderr) > buildCacheStreamLimit {
		return fmt.Errorf("cached diagnostic stream exceeds limit")
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "stdout.bin"), transcript.Stdout, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "stderr.bin"), transcript.Stderr, 0600); err != nil {
		return err
	}
	metadataTranscript := transcript
	metadataTranscript.Format = buildCacheRawStreams
	metadataTranscript.StdoutLength, metadataTranscript.StderrLength = len(transcript.Stdout), len(transcript.Stderr)
	metadataTranscript.Stdout, metadataTranscript.Stderr = nil, nil
	metadata, err := json.Marshal(metadataTranscript)
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
	"ELISACORE_PARTITION_DECLINE_DEBUG":       true,
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

// compilerCacheEnvKey excludes wrapper settings and lexer capacity reporting.
// Test-result caching deliberately keeps them: executed programs may read their
// environment even when the compiler itself does not.
func compilerCacheEnvKey() []string {
	ignored := map[string]bool{
		"ELISACORE_LEXER_CAPACITY_REPORT": true,
		"ELISA_REBUILD_SUPERVISED":        true,
		"ELISA_STAGE1_SEED_MAX_RSS_KB":    true, "ELISA_STAGE1_SEED_OPT_LEVEL": true,
		"ELISA_STAGE1_RSS_POLL_SECONDS": true, "ELISA_STAGE1_SEED_TIMEOUT_WRAPPED": true,
		"ELISA_STAGE1_SEED_BOOTSTRAP_BIN": true, "ELISA_STAGE1_SEED_BOOTSTRAP_KIND": true,
		"ELISA_STAGE1_SEED_CLANG": true, "ELISA_STAGE0_SOURCE_ROOT": true,
		"ELISA_STAGE1_BIN": true, "ELISA_STAGE1_OBJECT": true, "ELISA_RUNTIME_OBJ": true,
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
		"ELISACORE_PARTITION_DECLINE_DEBUG",
		"ELISACORE_PART_KEEP",
		"ELISACORE_RETAINED_PROFILE_DIR",
		"ELISACORE_RETAINED_HEAPPROFILE",
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
	// Source expansion and hashing the pinned compiler image are independent.
	// The normal cache-key path still validates the stamp and handles errors.
	// Join the worker even when loading or cache-key construction fails.
	compilerStampDone := make(chan struct{})
	go func() {
		_, _ = compilerSourceStamp()
		close(compilerStampDone)
	}()
	defer func() { <-compilerStampDone }()
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
