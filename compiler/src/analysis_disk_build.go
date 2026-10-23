package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func analysisBuildArtifact(complete analysisDiskArtifact, source []byte) analysisDiskArtifact {
	sum := sha256.New()
	sum.Write([]byte(complete.key))
	sum.Write([]byte{0})
	sum.Write(source)
	key := hex.EncodeToString(sum.Sum(nil))
	return analysisDiskArtifact{key: key, path: filepath.Join(filepath.Dir(complete.path), key+".building")}
}

// An unfinished checkpoint is separate from the reusable completed state and
// admits only this exact source revision. A stopped build returns an error and
// emits no artifact; running the same build again resumes in an ordinary fresh
// process. This qualification mode does not run a background worker.
func advanceAnalysisDiskBuild(program *loadedProgram, file *ast.File, options semantic.AnalyzeOptions, complete analysisDiskArtifact, stderr io.Writer) (*semantic.Result, *semantic.RetainedAnalysis) {
	artifact := analysisBuildArtifact(complete, program.source)
	var build *semantic.AnalysisBuild
	started := time.Now()
	if data, err := artifact.read(); err == nil {
		restored, err := semantic.RestoreAnalysisBuild(data)
		if err == nil && !restored.MatchesInvocation(program.filename, program.source, options) {
			err = fmt.Errorf("checkpoint invocation mismatch")
		}
		if err == nil {
			build = restored
			done, total := build.Progress()
			post, phases := build.PostProgress()
			debugAnalysisDisk(stderr, "bootstrap restore completed=%d/%d post=%d/%d bytes=%d in=%v", done, total, post, phases, len(data), time.Since(started).Round(time.Millisecond))
		} else {
			debugAnalysisDisk(stderr, "declined checkpoint: %v", err)
		}
	}
	incomplete := func(err error) *semantic.Result {
		return &semantic.Result{File: file, Diagnostics: []semantic.Diagnostic{{Severity: semantic.DiagnosticSeverityError, Message: err.Error()}}}
	}
	if build == nil {
		// A resumed build owns its pristine AST already. Only a new build needs
		// another parse; on the full compiler this is millions of unused tokens.
		pristine, ok := parseProgram(program.filename, program.source, io.Discard)
		if !ok {
			return semantic.AnalyzeWithOptions(file, options), nil
		}
		var err error
		build, err = semantic.BeginAnalysisBuild(file, pristine, program.source, options)
		if err != nil {
			return incomplete(err), nil
		}
	}
	limit, _ := strconv.Atoi(os.Getenv("ELISACORE_ANALYSIS_BOOTSTRAP_DECLS"))
	seconds, _ := strconv.Atoi(os.Getenv("ELISACORE_ANALYSIS_BOOTSTRAP_SECONDS"))
	if seconds < 1 || seconds > 15 {
		seconds = 3
	}
	result, state, err := build.Advance(limit, time.Duration(seconds)*time.Second)
	if err != nil {
		return incomplete(err), nil
	}
	stats := build.LastStats
	debugAnalysisDisk(stderr, "bootstrap step declarations=%d post=%d last=%s in=%v slowest=%v at=%v", stats.Declarations, stats.PostPhases, stats.LastPostPhase, stats.Elapsed.Round(time.Millisecond), stats.Slowest.Round(time.Millisecond), stats.SlowestPosition)
	if result != nil {
		debugAnalysisDisk(stderr, "bootstrap complete")
		return result, state
	}
	done, total := build.Progress()
	post, phases := build.PostProgress()
	saveStart := time.Now()
	data, err := build.MarshalState()
	if err == nil {
		current, valid := analysisDiskArtifactFor(program, options)
		if !valid || current.key != complete.key {
			err = fmt.Errorf("analysis inputs changed during build")
		} else {
			err = artifact.publish(data)
		}
	}
	if err != nil {
		return incomplete(fmt.Errorf("initial analysis stopped at %d/%d declarations, %d/%d final-check phases; checkpoint not saved: %w", done, total, post, phases, err)), nil
	}
	debugAnalysisDisk(stderr, "bootstrap checkpoint completed=%d/%d post=%d/%d bytes=%d in=%v", done, total, post, phases, len(data), time.Since(saveStart).Round(time.Millisecond))
	return incomplete(fmt.Errorf("initial analysis checkpoint saved (%d/%d declarations, %d/%d final-check phases); rerun the same build to continue", done, total, post, phases)), nil
}
