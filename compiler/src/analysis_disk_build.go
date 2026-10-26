package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// analysisDiskBuildCheckpoint is fully decoded and invocation-checked before
// it is handed from the restoration future to the synchronous build advance.
// Its capacity hint and timing preserve the ordinary restore path's sizing and
// diagnostics without decoding the same checkpoint twice.
type analysisDiskBuildCheckpoint struct {
	artifact     analysisDiskArtifact
	build        *semantic.AnalysisBuild
	capacityHint int
	bytes        int
	started      time.Time
}

func restoreAnalysisDiskBuildCheckpoint(complete analysisDiskArtifact, program *loadedProgram, options semantic.AnalyzeOptions) (*analysisDiskBuildCheckpoint, error) {
	artifact := analysisBuildArtifact(complete, program.source)
	started := time.Now()
	data, err := artifact.read()
	if err != nil {
		return nil, err
	}
	build, err := semantic.RestoreAnalysisBuild(data)
	if err == nil && !build.MatchesInvocation(program.filename, program.source, options) {
		err = fmt.Errorf("checkpoint invocation mismatch")
	}
	if err != nil {
		return nil, err
	}
	// Bound eager reservation independently of the admitted state size, just
	// like the synchronous restore path.
	capacityHint := min(len(data)+len(data)/16, 1<<30)
	return &analysisDiskBuildCheckpoint{artifact: artifact, build: build, capacityHint: capacityHint, bytes: len(data), started: started}, nil
}

func validateAnalysisDiskBuildCheckpoint(complete analysisDiskArtifact, program *loadedProgram, options semantic.AnalyzeOptions, checkpoint *analysisDiskBuildCheckpoint) error {
	if checkpoint == nil || checkpoint.build == nil {
		return fmt.Errorf("missing restored build checkpoint")
	}
	current := analysisBuildArtifact(complete, program.source)
	if checkpoint.artifact.key != current.key {
		return fmt.Errorf("checkpoint artifact changed before handoff")
	}
	if !checkpoint.build.MatchesInvocation(program.filename, program.source, options) {
		return fmt.Errorf("checkpoint invocation changed before handoff")
	}
	return nil
}

// An unfinished checkpoint is separate from the reusable completed state and
// admits only this exact source revision. A stopped build returns an error and
// emits no artifact; running the same build again resumes in an ordinary fresh
// process. This qualification mode does not run a background worker.
func advanceAnalysisDiskBuild(program *loadedProgram, file *ast.File, options semantic.AnalyzeOptions, complete analysisDiskArtifact, stderr io.Writer) (*semantic.Result, *semantic.RetainedAnalysis) {
	return advanceAnalysisDiskBuildWithCheckpoint(program, file, options, complete, stderr, nil, false)
}

func advanceAnalysisDiskBuildWithCheckpoint(program *loadedProgram, file *ast.File, options semantic.AnalyzeOptions, complete analysisDiskArtifact, stderr io.Writer, checkpoint *analysisDiskBuildCheckpoint, checkpointTried bool) (*semantic.Result, *semantic.RetainedAnalysis) {
	artifact := analysisBuildArtifact(complete, program.source)
	if checkpoint != nil {
		if err := validateAnalysisDiskBuildCheckpoint(complete, program, options, checkpoint); err != nil {
			debugAnalysisDisk(stderr, "declined checkpoint: %v", err)
			checkpoint = nil
			checkpointTried = false
		}
	}
	var build *semantic.AnalysisBuild
	capacityHint := 0
	started := time.Now()
	if checkpoint != nil {
		build = checkpoint.build
		capacityHint = checkpoint.capacityHint
		started = checkpoint.started
		done, total := build.Progress()
		post, phases := build.PostProgress()
		debugAnalysisDisk(stderr, "bootstrap restore completed=%d/%d post=%d/%d bytes=%d in=%v", done, total, post, phases, checkpoint.bytes, time.Since(started).Round(time.Millisecond))
	} else if !checkpointTried {
		restored, restoreErr := restoreAnalysisDiskBuildCheckpoint(complete, program, options)
		if restoreErr == nil {
			checkpoint = restored
			build = checkpoint.build
			capacityHint = checkpoint.capacityHint
			started = checkpoint.started
			done, total := build.Progress()
			post, phases := build.PostProgress()
			debugAnalysisDisk(stderr, "bootstrap restore completed=%d/%d post=%d/%d bytes=%d in=%v", done, total, post, phases, checkpoint.bytes, time.Since(started).Round(time.Millisecond))
		} else if !os.IsNotExist(restoreErr) {
			debugAnalysisDisk(stderr, "declined checkpoint: %v", restoreErr)
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
	done, total := build.Progress()
	budget := analysisBootstrapBudget(capacityHint, done == total)
	slow := analysisBootstrapSlowHint(artifact, done)
	debugAnalysisDisk(stderr, "bootstrap budget=%v elapsed=%v slow-before=%d", budget.Round(time.Millisecond), time.Since(processStartTime).Round(time.Millisecond), slow)
	watchdog := time.AfterFunc(budget+analysisBootstrapMargin, func() {
		// A body still running here leaves too little time for its checkpoint.
		// Name it so the next step stops before it and gives it a whole budget.
		if running := semantic.RunningDeclaration(); running >= 0 {
			analysisBootstrapRecordSlow(artifact, running)
			debugAnalysisDisk(stderr, "bootstrap deadline reached in declaration %d", running)
		}
	})
	result, state, err := build.AdvanceBefore(limit, budget, slow)
	watchdog.Stop()
	if err != nil {
		return incomplete(err), nil
	}
	stats := build.LastStats
	debugAnalysisDisk(stderr, "bootstrap step declarations=%d post=%d frames=%d last=%s in=%v slowest=%v at=%v", stats.Declarations, stats.PostPhases, stats.PostFrames, stats.LastPostPhase, stats.Elapsed.Round(time.Millisecond), stats.Slowest.Round(time.Millisecond), stats.SlowestPosition)
	if result != nil {
		debugAnalysisDisk(stderr, "bootstrap complete")
		_ = os.Remove(artifact.path + ".slow")
		return result, state
	}
	done, total = build.Progress()
	post, phases := build.PostProgress()
	saveStart := time.Now()
	data, err := build.MarshalStateWithCapacity(capacityHint)
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

// Each bootstrap step must finish inside the 30 s run cap: the process start,
// restore and parse already spent, this step's analysis, then either a
// checkpoint or, once the final checks complete, the full state store and code
// generation. A step whose declarations remain always ends with a checkpoint
// (post phases begin only in a fresh step), so it reserves the checkpoint; the
// final-check step reserves the store and codegen. Analysis stops between
// declarations, so one slow body can still overrun its share; the margin
// absorbs typical bodies and a timed-out step is simply rerun.
// ELISACORE_ANALYSIS_BOOTSTRAP_SECONDS (1-15) keeps a fixed per-step budget.
const (
	analysisBootstrapDeadline    = 30 * time.Second
	analysisBootstrapMargin      = 6 * time.Second
	analysisBootstrapCodegen     = 4 * time.Second
	analysisBootstrapMinBudget   = 3 * time.Second
	analysisBootstrapNanosPerB   = 12 // checkpoint encode+write, measured ~5.5 s for 470 MB
	analysisBootstrapStoreNsPerB = 20 // final store, measured ~11.9 s for 625 MB
	analysisBootstrapFirstBytes  = 400 << 20
)

func analysisBootstrapBudget(stateBytes int, finalChecks bool) time.Duration {
	if seconds, err := strconv.Atoi(os.Getenv("ELISACORE_ANALYSIS_BOOTSTRAP_SECONDS")); err == nil && seconds >= 1 && seconds <= 15 {
		return time.Duration(seconds) * time.Second
	}
	// A body step's checkpoint grows past the restored state; the completed
	// state the final step stores is no larger than the checkpoint it resumed.
	next := max(stateBytes+stateBytes/4, analysisBootstrapFirstBytes)
	reserve := time.Duration(next*analysisBootstrapNanosPerB)*time.Nanosecond + analysisBootstrapMargin
	if finalChecks {
		// Final-check frames are small, so the margin only covers jitter.
		reserve = time.Duration(max(stateBytes, analysisBootstrapFirstBytes)*analysisBootstrapStoreNsPerB)*time.Nanosecond + analysisBootstrapCodegen + analysisBootstrapMargin/3
	}
	budget := analysisBootstrapDeadline - time.Since(processStartTime) - reserve
	return max(budget, analysisBootstrapMinBudget)
}

// analysisBootstrapSlowHint returns the first declaration at or after next that
// an earlier step of this exact build was still analyzing at its deadline, or -1.
func analysisBootstrapSlowHint(artifact analysisDiskArtifact, next int) int {
	data, err := os.ReadFile(artifact.path + ".slow")
	if err != nil {
		return -1
	}
	found := -1
	for _, line := range strings.Fields(string(data)) {
		if index, err := strconv.Atoi(line); err == nil && index >= next && (found < 0 || index < found) {
			found = index
		}
	}
	return found
}

// analysisBootstrapRecordSlow appends a declaration index to the build's hint
// list. The checkpoint directory may not exist before the first checkpoint.
func analysisBootstrapRecordSlow(artifact analysisDiskArtifact, index int) {
	if os.MkdirAll(filepath.Dir(artifact.path), 0700) != nil {
		return
	}
	f, err := os.OpenFile(artifact.path+".slow", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	fmt.Fprintln(f, index)
	f.Close()
}
