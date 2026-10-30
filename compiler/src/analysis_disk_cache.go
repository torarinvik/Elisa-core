package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/semantic"
	"elisacore/src/smt"
)

// Standalone builds use complete portable disk state by default. Explicit
// legacy controls retain priority; no default selection starts a retained server.
func analysisDiskCacheEnabled() bool {
	switch os.Getenv("ELISACORE_ANALYSIS_CACHE") {
	case "1":
		return true // preserve explicit disk selection, including server override
	case "0":
		return false
	case "":
		return buildCacheEnabled() && os.Getenv("ELISACORE_RETAINED_SERVER") == ""
	default:
		return false
	}
}
func analysisDiskBootstrapEnabled() bool {
	value := os.Getenv("ELISACORE_ANALYSIS_BOOTSTRAP")
	return value == "" || value == "1"
}

// These environment controls ask for per-analysis output or validation that a
// completed semantic snapshot cannot replay. Backend-only observers are
// intentionally absent: codegen still runs on a semantic-state hit and can
// produce their output from the current parsed source.
var analysisDiskRequestObserverVariables = [...]string{
	"ELISA_SMT_RLIMIT_LOG",
	"ELISA_SMT_TRACE",
	"ELISA_SEMANTIC_INTERNAL_PARITY_OUT",
	"ELISA_DUMP_DEATHTIME",
	"ELISA_DUMP_REGION_STACKS",
	"ELISA_REGION_DEBUG",
	"ELISA_ELEMSTATE_TRACE",
	"ELISA_STORE_PAYLOAD_TRACE",
	"ELISA_STORESCAN_TRACE",
	"ELISA_STORE_TRACE",
	"ELISA_RECLAIM_TRACE",
	"ELISA_PARALLEL_GRAPH_REPORT",
	"ELISA_PARALLEL_SHARING_REPORT",
	"ELISA_PARALLEL_DECLINE_LOG",
	"ELISA_POST_TIMING",
	"ELISA_DECL_TIMING",
	// These shadow/check modes validate work performed by the analyzer. A
	// restored snapshot would skip the validation for this invocation.
	"ELISA_REGION_FIXPOINT_SHADOW",
	"ELISA_TRACKED_SHARE_CHECK",
	"ELISA_SMT_PREAMBLE_SHADOW",
}

func analysisDiskRequestObserver() string {
	// Shadow fast preparation validates the current invocation against a fresh
	// scratch analysis. Replaying semantic or object-cache state would skip that
	// comparison, and a retained daemon may have captured a different mode at
	// startup, so keep this request in the local uncached path.
	if os.Getenv("ELISACORE_RETAINED_FAST_PREPARE") == "shadow" {
		return "ELISACORE_RETAINED_FAST_PREPARE"
	}
	for _, name := range analysisDiskRequestObserverVariables {
		if os.Getenv(name) != "" {
			return name
		}
	}
	return ""
}

func analysisDiskRequestObserverEnabled() bool { return analysisDiskRequestObserver() != "" }

// A retained server is a separate process with semantic observer globals
// captured at startup. Keep observer requests in the client process so their
// output/validation switches describe this invocation, not the server's
// startup environment. Backend parity also depends on a lexer package global
// captured at startup, but it does not require bypassing semantic disk state.
func retainedRequestRequiresLocalProcess() bool {
	// The outer s0cache certificate records process-local SMT taint. A retained
	// server has independent semantic state, so its result cannot be certified
	// by this client process. Keep certificate-backed requests local.
	if os.Getenv("ELISACORE_CACHE_CERTIFICATE") != "" {
		return true
	}
	return analysisDiskRequestObserverEnabled() || os.Getenv("ELISA_BACKEND_PARITY_OUT") != ""
}

const analysisStateMaxBytes = 2 << 30

type analysisDiskArtifact struct{ key, path string }

func analysisDiskArtifactFor(program *loadedProgram, options semantic.AnalyzeOptions) (analysisDiskArtifact, bool) {
	return analysisDiskArtifactForAuthority(program, options, true)
}

func analysisDiskArtifactForAuthority(program *loadedProgram, options semantic.AnalyzeOptions, includeOrigins bool) (analysisDiskArtifact, bool) {
	if program == nil || program.file != nil || len(program.easm) != 0 || !isSurfaceSourcePath(program.filename) {
		return analysisDiskArtifact{}, false
	}
	h := sha256.New()
	testRunnerCacheWriteString(h, "semantic-disk-state-v1")
	stamp, err := compilerSourceStamp()
	if err != nil {
		return analysisDiskArtifact{}, false
	}
	testRunnerCacheWriteString(h, stamp)
	cwd, err := os.Getwd()
	if err != nil {
		return analysisDiskArtifact{}, false
	}
	testRunnerCacheWriteString(h, cwd)
	testRunnerCacheWriteString(h, program.filename)
	encoded, err := json.Marshal(options)
	if err != nil {
		return analysisDiskArtifact{}, false
	}
	testRunnerCacheWriteBytes(h, "options", encoded)
	for _, entry := range compilerCacheEnvKey() {
		name, _, _ := strings.Cut(entry, "=")
		// These controls only select backend emission strategy. They cannot change
		// semantic facts or diagnostics, so switching codegen modes must still be
		// able to restore the same portable analysis snapshot.
		if analysisDiskBackendStrategyEnv[name] {
			continue
		}
		testRunnerCacheWriteString(h, entry)
	}
	paths, err := json.Marshal(diagnosticPathInputs(program.filename, program.source))
	if err != nil {
		return analysisDiskArtifact{}, false
	}
	testRunnerCacheWriteBytes(h, "diagnostic-paths", paths)
	origins := append([]string(nil), program.sourceFiles...)
	sort.Strings(origins)
	if includeOrigins {
		for _, origin := range origins {
			testRunnerCacheWriteString(h, "source-authority="+origin)
		}
	}
	target, err := backend.ResolveTargetIdentity(options.TargetTriple)
	if err != nil {
		return analysisDiskArtifact{}, false
	}
	writeResolvedNativeCacheTarget(h, target)
	if options.EnableSMT {
		selector := smt.ResolveBinary("")
		testRunnerCacheWriteString(h, "smt-selector="+selector)
		if selected, err := exec.LookPath(selector); err == nil {
			stamp, err := toolchainNativeContentStamp(selected)
			if err != nil {
				return analysisDiskArtifact{}, false
			}
			testRunnerCacheWriteString(h, selected)
			testRunnerCacheWriteString(h, stamp)
		} else {
			testRunnerCacheWriteString(h, "smt-unavailable")
		}
	}
	root, err := buildCacheRoot()
	if err != nil {
		return analysisDiskArtifact{}, false
	}
	key := hex.EncodeToString(h.Sum(nil))
	return analysisDiskArtifact{key: key, path: filepath.Join(root, "analysis", key+".state")}, true
}

// These variables change only how the backend schedules or partitions code
// generation. They do not affect analysis facts, diagnostics, or source
// authority, so they must not fragment the portable semantic-state cache.
var analysisDiskBackendStrategyEnv = map[string]bool{
	"ELISACORE_OBJECT_PARTITIONS":           true,
	"ELISACORE_PARALLEL_IR":                 true,
	"ELISACORE_EXPERIMENT_PARTITION_SUBMIT": true,
	"ELISACORE_PARALLEL_OPT":                true,
	"ELISACORE_PARALLEL_OPT_IMPORT":         true,
	"ELISACORE_IR_IMPORTS":                  true,
	"ELISACORE_NATIVE_FRAGMENTS":            true,
	"ELISACORE_PARTITION_SCHEDULE":          true,
}

// Probe the configured storage by actual I/O, not mode bits (which can lie
// under ACLs, containers or privileged users). This is a best-effort prerequisite
// for automatic checkpoints, never cache admission or semantic authority.
func analysisDiskCheckpointStorageAvailable(artifact analysisDiskArtifact) error {
	dir := filepath.Dir(artifact.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".analysis-checkpoint-probe-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write([]byte{0})
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Remove(file.Name())
}

func (a analysisDiskArtifact) header() []byte { return []byte("ELISACSTATE1\n" + a.key + "\n") }
func (a analysisDiskArtifact) read() ([]byte, error) {
	f, err := os.Open(a.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	header := a.header()
	if !info.Mode().IsRegular() || info.Size() <= int64(len(header)) || info.Size() > analysisStateMaxBytes {
		return nil, fmt.Errorf("invalid analysis cache size")
	}
	// The stat-checked immutable file has a known size. Reading directly into
	// its final allocation avoids repeatedly copying a half-gigabyte snapshot
	// as io.ReadAll grows its buffer. A concurrent size change fails closed.
	data := make([]byte, int(info.Size()))
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, err
	}
	var extra [1]byte
	if n, err := f.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, fmt.Errorf("analysis cache size changed during read")
	}
	if !bytes.HasPrefix(data, header) {
		return nil, fmt.Errorf("analysis cache identity mismatch")
	}
	return data[len(header):], nil
}
func (a analysisDiskArtifact) publish(data []byte) error {
	if len(data)+len(a.header()) > analysisStateMaxBytes {
		return fmt.Errorf("analysis cache state exceeds size limit")
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.path), ".analysis-publish-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(a.header()); err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), a.path)
}

func debugAnalysisDisk(stderr io.Writer, format string, args ...any) {
	if os.Getenv("ELISACORE_ANALYSIS_CACHE_DEBUG") == "1" {
		fmt.Fprintf(stderr, "[analysis-disk] "+format+"\n", args...)
	}
}

func analyzeSemanticFromDisk(program *loadedProgram, file *ast.File, options semantic.AnalyzeOptions, stderr io.Writer, pending *analysisDiskRestoration) *semantic.Result {
	artifact, eligible := analysisDiskArtifactFor(program, options)
	if !eligible {
		return semantic.AnalyzeWithOptions(file, options)
	}
	var state *semantic.RetainedAnalysis
	var result *semantic.Result
	var buildCheckpoint *analysisDiskBuildCheckpoint
	checkpointTried := false
	unchanged := false
	replayed := false
	started := time.Now()
	if pending == nil || pending.artifact.key != artifact.key {
		pending.join()
		pending = startAnalysisDiskRestoration(program, options)
	}
	pending.join()
	if pending != nil {
		buildCheckpoint = pending.build
		checkpointTried = pending.buildTried
		if pending.buildTried && pending.buildErr != nil && !os.IsNotExist(pending.buildErr) {
			debugAnalysisDisk(stderr, "declined checkpoint: %v", pending.buildErr)
		}
	}
	if pending != nil && pending.build != nil {
		debugAnalysisDisk(stderr, "prefer exact bootstrap checkpoint over completed baseline")
	} else if pending != nil && pending.loaded {
		started = pending.started
		restored, preparation, err := pending.state, pending.preparation, pending.err
		if err == nil && !restored.MatchesInvocation(program.filename, options) {
			err = fmt.Errorf("analysis invocation mismatch")
		}
		if err == nil {
			state = restored
			if pending.familyReplay {
				debugAnalysisDisk(stderr, "replay source-family baseline")
			}
			debugAnalysisDisk(stderr, "restore %s bytes=%d in=%v", artifact.key[:12], pending.bytes, time.Since(started).Round(time.Millisecond))
			if cached, ok := state.ResultForSource(program.source); ok && !pending.familyReplay {
				result = cached
				unchanged = true
				debugAnalysisDisk(stderr, "reuse unchanged revision")
			} else {
				if preparation.prepared.AcceptsScratch(preparation.file) {
					if preparation.file == nil {
						debugAnalysisDisk(stderr, "scratch parsing deferred until needed")
					} else {
						debugAnalysisDisk(stderr, "prepared scratch in=%v", preparation.elapsed.Round(time.Millisecond))
					}
					updateStart := time.Now()
					result, err = state.UpdatePrepared(file, preparation.file, program.source, preparation.prepared)
					if err == nil {
						// Keep the admitted baseline immutable. Replaying a
						// supported source delta is much cheaper than writing
						// the entire graph, and reuses the same identity domain
						// on repeated edits/reverts in fresh processes.
						replayed = true
						debugAnalysisDisk(stderr, "update changed=%d in=%v fast=%v%s", state.LastStats.Changed, time.Since(updateStart).Round(time.Millisecond), state.LastStats.FastPrepared, retainedFastNote(state.LastStats.FastDeclined))
						debugAnalysisDisk(stderr, "update phases pairing=%v scratch=%v compare=%v bodies=%v post=%v", state.LastStats.Pairing.Round(time.Millisecond), state.LastStats.Scratch.Round(time.Millisecond), state.LastStats.Compare.Round(time.Millisecond), state.LastStats.Bodies.Round(time.Millisecond), state.LastStats.Post.Round(time.Millisecond))
					}
				} else {
					err = fmt.Errorf("scratch parse failed")
				}
				if err != nil {
					debugAnalysisDisk(stderr, "fallback: %v", err)
					state = nil
					result = nil
					// Update can consume its parse even on a late fallback.
					if fresh, ok := parseProgram(program.filename, program.source, io.Discard); ok {
						file = fresh
					}
				}
			}
		} else {
			debugAnalysisDisk(stderr, "declined state: %v", err)
		}
	}
	if result == nil {
		coldStart := time.Now()
		if analysisDiskBootstrapEnabled() {
			if os.Getenv("ELISACORE_ANALYSIS_BOOTSTRAP") == "" {
				if err := analysisDiskCheckpointStorageAvailable(artifact); err != nil {
					// Persistent state is optional. Without writable storage,
					// use the established uncached analyzer rather than turn
					// unfinished progress into a compile failure. The external
					// process cap still applies; no deadline clock is reset.
					debugAnalysisDisk(stderr, "automatic checkpoint storage unavailable: %v; uncached local analysis", err)
					return semantic.AnalyzeWithOptions(file, options)
				}
			}
			result, state = advanceAnalysisDiskBuildWithCheckpoint(program, file, options, artifact, stderr, analysisDiskResumeCheckpoint(buildCheckpoint, pending), checkpointTried)
		} else {
			pristine, ok := parseProgram(program.filename, program.source, io.Discard)
			if !ok {
				return semantic.AnalyzeWithOptions(file, options)
			}
			result, state = semantic.AnalyzeRetained(file, pristine, program.source, options)
		}
		debugAnalysisDisk(stderr, "cold analysis in=%v", time.Since(coldStart).Round(time.Millisecond))
	}
	if replayed {
		debugAnalysisDisk(stderr, "replay baseline; checkpoint unchanged")
	}
	if state != nil && !unchanged && !replayed {
		saveStart := time.Now()
		data, err := state.MarshalState()
		if err == nil {
			current, valid := analysisDiskArtifactFor(program, options)
			if valid && current.key == artifact.key {
				err = artifact.publish(data)
			} else {
				err = fmt.Errorf("analysis inputs changed during build")
			}
		}
		if err != nil {
			debugAnalysisDisk(stderr, "not stored: %v", err)
		} else {
			debugAnalysisDisk(stderr, "store bytes=%d in=%v", len(data), time.Since(saveStart).Round(time.Millisecond))
			// Retain a durable cold boundary until completed publication succeeds.
			building := analysisBuildArtifact(artifact, program.source)
			_ = os.Remove(building.path)
			_ = os.Remove(building.path + ".slow")
			if err := artifact.publishBaselineSource(program.source); err != nil {
				debugAnalysisDisk(stderr, "baseline source not stored: %v", err)
			}
			if err := publishAnalysisDiskFamily(program, options, artifact); err != nil {
				debugAnalysisDisk(stderr, "family not stored: %v", err)
			}
		}
	}
	return result
}
