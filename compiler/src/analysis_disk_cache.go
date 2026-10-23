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
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
	"elisacore/src/smt"
)

// Opt-in during qualification. This mode always runs in this process and never
// forwards to a retained server. Only portable, complete idle state is admitted.
func analysisDiskCacheEnabled() bool { return os.Getenv("ELISACORE_ANALYSIS_CACHE") == "1" }

const analysisStateMaxBytes = 2 << 30

type analysisDiskArtifact struct{ key, path string }

func analysisDiskArtifactFor(program *loadedProgram, options semantic.AnalyzeOptions) (analysisDiskArtifact, bool) {
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
	for _, entry := range buildCacheEnvKey() {
		testRunnerCacheWriteString(h, entry)
	}
	paths, err := json.Marshal(diagnosticPathInputs(program.filename, program.source))
	if err != nil {
		return analysisDiskArtifact{}, false
	}
	testRunnerCacheWriteBytes(h, "diagnostic-paths", paths)
	origins := append([]string(nil), program.sourceFiles...)
	sort.Strings(origins)
	for _, origin := range origins {
		testRunnerCacheWriteString(h, "source-authority="+origin)
	}
	if err := writeNativeCacheTarget(h, options.TargetTriple); err != nil {
		return analysisDiskArtifact{}, false
	}
	if options.EnableSMT {
		selector := smt.ResolveBinary("")
		testRunnerCacheWriteString(h, "smt-selector="+selector)
		if selected, err := exec.LookPath(selector); err == nil {
			stamp, err := toolchainContentStamp(selected)
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
	data, err := io.ReadAll(io.LimitReader(f, analysisStateMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > analysisStateMaxBytes || !bytes.HasPrefix(data, header) {
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

func analyzeSemanticFromDisk(program *loadedProgram, file *ast.File, options semantic.AnalyzeOptions, stderr io.Writer) *semantic.Result {
	artifact, eligible := analysisDiskArtifactFor(program, options)
	if !eligible {
		return semantic.AnalyzeWithOptions(file, options)
	}
	var state *semantic.RetainedAnalysis
	var result *semantic.Result
	unchanged := false
	started := time.Now()
	if data, err := artifact.read(); err == nil {
		restored, err := semantic.RestoreAnalysisState(data)
		if err == nil && !restored.MatchesInvocation(program.filename, options) {
			err = fmt.Errorf("analysis invocation mismatch")
		}
		if err == nil {
			state = restored
			debugAnalysisDisk(stderr, "restore %s bytes=%d in=%v", artifact.key[:12], len(data), time.Since(started).Round(time.Millisecond))
			if cached, ok := state.ResultForSource(program.source); ok {
				result = cached
				unchanged = true
				debugAnalysisDisk(stderr, "reuse unchanged revision")
			} else {
				scratch, ok := parseProgram(program.filename, program.source, io.Discard)
				if ok {
					updateStart := time.Now()
					result, err = state.Update(file, scratch, program.source)
					if err == nil {
						debugAnalysisDisk(stderr, "update changed=%d in=%v", state.LastStats.Changed, time.Since(updateStart).Round(time.Millisecond))
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
		pristine, ok := parseProgram(program.filename, program.source, io.Discard)
		if !ok {
			return semantic.AnalyzeWithOptions(file, options)
		}
		coldStart := time.Now()
		if os.Getenv("ELISACORE_ANALYSIS_BOOTSTRAP") == "1" {
			result, state = advanceAnalysisDiskBuild(program, file, pristine, options, artifact, stderr)
		} else {
			result, state = semantic.AnalyzeRetained(file, pristine, program.source, options)
		}
		debugAnalysisDisk(stderr, "cold analysis in=%v", time.Since(coldStart).Round(time.Millisecond))
	}
	if state != nil && !unchanged {
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
		}
	}
	return result
}
