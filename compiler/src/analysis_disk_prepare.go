package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// This future belongs to one invocation. Its owner always joins it, including
// parse failures. Nothing survives to answer another compiler request.
type analysisDiskRestoration struct {
	artifact     analysisDiskArtifact
	started      time.Time
	done         chan struct{}
	state        *semantic.RetainedAnalysis
	build        *analysisDiskBuildCheckpoint
	buildTried   bool
	buildErr     error
	preparation  analysisScratchPreparation
	err          error
	loaded       bool
	familyReplay bool
	bytes        int
}

func startAnalysisDiskRestoration(program *loadedProgram, options semantic.AnalyzeOptions) *analysisDiskRestoration {
	artifact, eligible := analysisDiskArtifactFor(program, options)
	if !eligible {
		return nil
	}
	// Established before either parser starts. Sharing tokens does not share AST
	// identity or semantic mutations, and does not retain a server/session cache.
	if program.tokenCache == nil {
		program.tokenCache = newRequestSourceTokenCache()
	}
	pending := &analysisDiskRestoration{artifact: artifact, started: time.Now(), done: make(chan struct{})}
	go func() {
		defer close(pending.done)
		// During a resumable bootstrap, an exact unfinished build is usually
		// more valuable than restoring an older completed baseline and preparing
		// a full scratch graph. A .src byte match is only an ordering hint: the
		// completed snapshot still needs its normal restore and source check.
		if os.Getenv("ELISACORE_ANALYSIS_BOOTSTRAP") == "1" {
			buildArtifact := analysisBuildArtifact(artifact, program.source)
			if _, statErr := os.Stat(buildArtifact.path); statErr == nil && !analysisDiskBaselineSourceMatches(artifact, program.source) {
				pending.buildTried = true
				pending.build, pending.buildErr = restoreAnalysisDiskBuildCheckpoint(artifact, program, options)
				if pending.build != nil {
					return
				}
			}
		}
		data, err := artifact.read()
		// A family hint never changes the exact artifact's identity or header.
		// Only a missing exact artifact may try a validated added-file baseline.
		if os.IsNotExist(err) {
			if candidate, ok := analysisDiskAddedFilesBaseline(program, options); ok {
				data, err = candidate.read()
				pending.familyReplay = err == nil
			}
		}
		if err != nil {
			pending.err = err
			return
		}
		pending.loaded, pending.bytes = true, len(data)
		lazy := analysisDiskLikelyBodyEdit(artifact, program.source)
		pending.state, pending.preparation, pending.err = restoreAnalysisWithScratch(data, program, options, lazy)
	}()
	return pending
}

// analysisDiskBaselineSourceMatches is only a restore-ordering hint. The
// source sidecar never admits a semantic state; RestoreAnalysisState,
// MatchesInvocation, and ResultForSource remain the completed-state gates.
func analysisDiskBaselineSourceMatches(artifact analysisDiskArtifact, source []byte) bool {
	baseline, err := os.ReadFile(artifact.baselineSourcePath())
	return err == nil && bytes.Equal(baseline, source)
}

func (p *analysisDiskRestoration) join() {
	if p != nil {
		<-p.done
	}
}

type analysisScratchPreparation struct {
	file     *ast.File
	prepared *semantic.RetainedScratch
	elapsed  time.Duration
}

// Hydration only allocates and reconnects the checked graph; it does not run
// semantic analysis. A separate parse/declaration phase can therefore overlap
// it. Always join preparation before returning, including rejected snapshots,
// so fallback analysis and later requests never inherit background work.
// With lazy set, neither parsing nor declarations run until an update needs them.
func restoreAnalysisWithScratch(data []byte, program *loadedProgram, options semantic.AnalyzeOptions, lazy bool) (*semantic.RetainedAnalysis, analysisScratchPreparation, error) {
	if lazy {
		// Keep invocation inputs, not a second AST. No parsing/declaration work
		// starts until UpdatePrepared rejects its fast body preparation.
		filename, source, tokens := program.filename, program.source, program.tokenCache
		prepared := semantic.NewLazyParsedRetainedScratch(func() (*ast.File, error) {
			file, ok := parseProgramWithTokenCache(filename, source, io.Discard, tokens)
			if !ok {
				return nil, fmt.Errorf("independent source parse rejected")
			}
			return file, nil
		}, options)
		state, err := semantic.RestoreAnalysisState(data)
		return state, analysisScratchPreparation{prepared: prepared}, err
	}
	done := make(chan analysisScratchPreparation, 1)
	go func() {
		started := time.Now()
		var preparation analysisScratchPreparation
		if file, ok := parseProgramWithTokenCache(program.filename, program.source, io.Discard, program.tokenCache); ok {
			preparation.file = file
			preparation.prepared = semantic.PrepareRetainedScratch(file, options)
		}
		preparation.elapsed = time.Since(started)
		done <- preparation
	}()
	state, err := semantic.RestoreAnalysisState(data)
	preparation := <-done
	return state, preparation, err
}

// analysisDiskBaselineSource is the source the stored state was analyzed from.
func (a analysisDiskArtifact) baselineSourcePath() string { return a.path + ".src" }

// analysisDiskLikelyBodyEdit is a bounded scheduling guess, never an admission
// certificate. Trim equal prefix/suffix lines and defer scratch only when the
// remaining changed lines are indented or blank. Semantic Update still checks
// the complete body certificate and materializes independent scratch on decline.
func analysisDiskLikelyBodyEdit(artifact analysisDiskArtifact, source []byte) bool {
	if os.Getenv("ELISACORE_RETAINED_FAST_PREPARE") != "on" {
		return false
	}
	baseline, err := os.ReadFile(artifact.baselineSourcePath())
	if err != nil {
		return false
	}
	before, after := bytes.Split(baseline, []byte("\n")), bytes.Split(source, []byte("\n"))
	for len(before) != 0 && len(after) != 0 && bytes.Equal(before[0], after[0]) {
		before, after = before[1:], after[1:]
	}
	for len(before) != 0 && len(after) != 0 && bytes.Equal(before[len(before)-1], after[len(after)-1]) {
		before, after = before[:len(before)-1], after[:len(after)-1]
	}
	for _, lines := range [][][]byte{before, after} {
		for _, line := range lines {
			if len(bytes.TrimSpace(line)) != 0 && !analysisDiskIndented(line) {
				return false
			}
		}
	}
	return true
}

func analysisDiskIndented(line []byte) bool {
	return len(line) != 0 && (line[0] == ' ' || line[0] == '\t')
}

// publishBaselineSource records the source a published state was analyzed from.
func (a analysisDiskArtifact) publishBaselineSource(source []byte) error {
	f, err := os.CreateTemp(filepath.Dir(a.path), ".analysis-source-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(source)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), a.baselineSourcePath())
}
