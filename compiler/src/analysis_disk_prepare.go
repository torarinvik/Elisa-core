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
		if analysisDiskBootstrapEnabled() {
			buildArtifact := analysisBuildArtifact(artifact, program.source)
			if _, statErr := os.Stat(buildArtifact.path); statErr == nil && !analysisDiskBaselineSourceMatches(artifact, program.source) {
				pending.buildTried = true
				pending.build, pending.buildErr = restoreAnalysisDiskBuildCheckpoint(artifact, program, options)
				if analysisDiskCheckpointShadows(artifact, pending.build) {
					pending.build = nil
				}
				if pending.build != nil {
					return
				}
			}
		}
		load := func(candidate analysisDiskArtifact) error {
			var preparationDone chan analysisScratchPreparation
			err := candidate.withPayload(func(data []byte) error {
				pending.loaded = true
				pending.bytes = len(data)
				if analysisDiskLikelyBodyEdit(artifact, program.source) {
					filename, source, tokens := program.filename, program.source, program.tokenCache
					prepared := semantic.NewLazyParsedRetainedScratch(func() (*ast.File, error) {
						file, ok := parseProgramWithTokenCache(filename, source, io.Discard, tokens)
						if !ok {
							return nil, fmt.Errorf("independent source parse rejected")
						}
						return file, nil
					}, options)
					pending.preparation = analysisScratchPreparation{prepared: prepared}
				} else {
					preparationDone = startAnalysisScratchPreparation(program, options)
				}
				// RestoreAnalysisState copies any strings and byte slices it retains,
				// so the mapping can be released as soon as this call returns.
				pending.state, pending.err = semantic.RestoreAnalysisState(data)
				return nil
			})
			if preparationDone != nil {
				pending.preparation = <-preparationDone
			}
			return err
		}
		err := load(artifact)
		// A family hint never changes the exact artifact's identity or header.
		// Only a missing exact artifact may try a validated added-file baseline.
		if !pending.loaded && os.IsNotExist(err) {
			if candidate, ok := analysisDiskFamilyBaseline(program, options); ok {
				err = load(candidate)
				pending.familyReplay = err == nil && pending.loaded
			}
		}
		if err != nil {
			pending.err = err
			return
		}
	}()
	return pending
}

// analysisDiskBaselineSourceMatches is only a restore-ordering hint. The
// source sidecar never admits a semantic state; RestoreAnalysisState,
// MatchesInvocation, and ResultForSource remain the completed-state gates.
func analysisDiskBaselineSourceMatches(artifact analysisDiskArtifact, source []byte) bool {
	matched := false
	err := artifact.withBaselineSource(func(baseline []byte) error {
		matched = bytes.Equal(baseline, source)
		return nil
	})
	return err == nil && matched
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
func startAnalysisScratchPreparation(program *loadedProgram, options semantic.AnalyzeOptions) chan analysisScratchPreparation {
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
	return done
}

// restoreAnalysisWithScratch keeps the byte-slice restoration helper used by
// package tests. Production disk restoration uses withPayload directly so a
// mapped artifact is released before waiting for eager scratch preparation.
func restoreAnalysisWithScratch(data []byte, program *loadedProgram, options semantic.AnalyzeOptions, lazy bool) (*semantic.RetainedAnalysis, analysisScratchPreparation, error) {
	if lazy {
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
	done := startAnalysisScratchPreparation(program, options)
	state, err := semantic.RestoreAnalysisState(data)
	return state, <-done, err
}

// analysisDiskBaselineSource is the source the stored state was analyzed from.
func (a analysisDiskArtifact) baselineSourcePath() string { return a.path + ".src" }

// analysisDiskLikelyBodyEdit is a bounded scheduling guess, never an admission
// certificate. Trim equal prefix/suffix lines and defer scratch only when the
// remaining changed lines are indented or blank. Semantic Update still checks
// the complete body certificate and materializes independent scratch on decline.
func analysisDiskLikelyBodyEdit(artifact analysisDiskArtifact, source []byte) bool {
	if semantic.RetainedFastPrepareSetting() != "on" {
		return false
	}
	likely := false
	err := artifact.withBaselineSource(func(baseline []byte) error {
		likely = analysisDiskLikelyBodyEditSources(baseline, source)
		return nil
	})
	return err == nil && likely
}

// analysisDiskLikelyBodyEditSources preserves bytes.Split's line semantics
// without allocating a slice for every line in either source. It is only a
// scheduling hint; semantic Update still checks the complete body certificate.
func analysisDiskLikelyBodyEditSources(before, after []byte) bool {
	if bytes.Equal(before, after) {
		return true
	}
	beforeCount, afterCount := bytes.Count(before, []byte("\n"))+1, bytes.Count(after, []byte("\n"))+1
	beforeForward := analysisDiskForwardLines{data: before}
	afterForward := analysisDiskForwardLines{data: after}
	prefixLimit := min(beforeCount, afterCount)
	prefix := 0
	for prefix < prefixLimit {
		beforeNext, afterNext := beforeForward, afterForward
		beforeLine, beforeOK := beforeNext.next()
		afterLine, afterOK := afterNext.next()
		if !beforeOK || !afterOK || !bytes.Equal(beforeLine, afterLine) {
			break
		}
		beforeForward, afterForward = beforeNext, afterNext
		prefix++
	}

	beforeReverse := analysisDiskReverseLines{data: before, end: len(before)}
	afterReverse := analysisDiskReverseLines{data: after, end: len(after)}
	suffixLimit := min(beforeCount-prefix, afterCount-prefix)
	suffix := 0
	for suffix < suffixLimit {
		beforeNext, afterNext := beforeReverse, afterReverse
		beforeLine, beforeOK := beforeNext.next()
		afterLine, afterOK := afterNext.next()
		if !beforeOK || !afterOK || !bytes.Equal(beforeLine, afterLine) {
			break
		}
		beforeReverse, afterReverse = beforeNext, afterNext
		suffix++
	}

	for remaining := beforeCount - prefix - suffix; remaining > 0; remaining-- {
		line, ok := beforeForward.next()
		if !ok {
			return false
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) != 0 && (!analysisDiskIndented(line) || analysisDiskContractClause(trimmed)) {
			return false
		}
	}
	for remaining := afterCount - prefix - suffix; remaining > 0; remaining-- {
		line, ok := afterForward.next()
		if !ok {
			return false
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) != 0 && (!analysisDiskIndented(line) || analysisDiskContractClause(trimmed)) {
			return false
		}
	}
	return true
}

type analysisDiskForwardLines struct {
	data   []byte
	offset int
	done   bool
}

func (lines *analysisDiskForwardLines) next() ([]byte, bool) {
	if lines.done {
		return nil, false
	}
	relativeEnd := bytes.IndexByte(lines.data[lines.offset:], '\n')
	if relativeEnd < 0 {
		lines.done = true
		return lines.data[lines.offset:], true
	}
	end := lines.offset + relativeEnd
	line := lines.data[lines.offset:end]
	lines.offset = end + 1
	return line, true
}

type analysisDiskReverseLines struct {
	data []byte
	end  int
	done bool
}

func (lines *analysisDiskReverseLines) next() ([]byte, bool) {
	if lines.done {
		return nil, false
	}
	if lines.end == 0 {
		lines.done = true
		return lines.data[:0], true
	}
	relativeStart := bytes.LastIndexByte(lines.data[:lines.end], '\n')
	if relativeStart < 0 {
		lines.done = true
		return lines.data[:lines.end], true
	}
	line := lines.data[relativeStart+1 : lines.end]
	lines.end = relativeStart
	return line, true
}

// analysisDiskContractClause is a scheduling hint, not an admission check.
// Leading function-body contract statements are lifted into the declaration
// phase, so prepare the independent scratch eagerly when their text changes.
// Protocol methods also permit changes and preserves clauses in their block.
func analysisDiskContractClause(line []byte) bool {
	for _, keyword := range [...]string{"requires", "ensure", "decreases", "uses", "changes", "preserves", "fulfills"} {
		if !bytes.HasPrefix(line, []byte(keyword)) {
			continue
		}
		if len(line) == len(keyword) {
			return true
		}
		next := line[len(keyword)]
		if next == ' ' || next == '\t' || next == '(' || next == '[' || next == ':' || next == '*' {
			return true
		}
	}
	return false
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

// analysisDiskBuildPreferredRemaining is the largest number of unfinished
// declarations for which an exact checkpoint is preferred over a completed
// baseline. A distant checkpoint costs several bounded runs to finish, while
// restoring the baseline and updating it to the current source costs seconds.
var analysisDiskBuildPreferredRemaining = 256

// analysisDiskCheckpointShadows reports whether an exact checkpoint is distant
// from completion while the completed baseline it would shadow exists.
func analysisDiskCheckpointShadows(artifact analysisDiskArtifact, checkpoint *analysisDiskBuildCheckpoint) bool {
	if checkpoint == nil || checkpoint.build == nil {
		return false
	}
	if _, err := os.Stat(artifact.path); err != nil {
		return false
	}
	done, total := checkpoint.build.Progress()
	return total-done > analysisDiskBuildPreferredRemaining
}
