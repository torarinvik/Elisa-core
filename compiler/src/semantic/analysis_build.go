package semantic

import (
	"bytes"
	"fmt"
	"reflect"
	"time"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// AnalysisBuild is an unfinished initial analysis. It never exposes a semantic
// Result until every declaration and whole-program check has completed. Between
// declarations it can be saved and restored by a later ordinary process.
// Checkpoints admit only the exact same source and invocation, unlike the
// completed RetainedAnalysis, which supports incremental edits.
type AnalysisBuild struct {
	retained    *RetainedAnalysis
	file        *ast.File
	loweredFile *ast.File
	next        int
	postNext    int
	started     bool
	done        bool
	LastStats   AnalysisBuildStats
	lateStarted bool
	lateNext    int
	lateLast    map[*ast.FuncDecl]int
}

// AnalysisBuildStats describes the latest step, including the indivisible
// declaration that most exceeded the requested time slice.
type AnalysisBuildStats struct {
	Declarations    int
	PostPhases      int
	LastPostPhase   string
	Elapsed         time.Duration
	Slowest         time.Duration
	SlowestPosition lexer.Pos
	PostFrames      int
}

// Access is serialized by pendingRetainedMu, as with AnalyzeRetained.
var pendingAnalysisBuild *AnalysisBuild

// BeginAnalysisBuild completes the declaration phase and stops before bodies.
// The two ASTs must be independent parses of src. They become owned by the build.
func BeginAnalysisBuild(file, pristine *ast.File, src []byte, options AnalyzeOptions) (*AnalysisBuild, error) {
	if file == nil || pristine == nil {
		return nil, fmt.Errorf("missing build AST")
	}
	r := &RetainedAnalysis{options: options, pristine: pristine, src: src, windows: map[*ast.FuncDecl][2]int{}, rbDeps: map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}, frameDiags: map[*ast.FuncDecl][]Diagnostic{}}
	build := &AnalysisBuild{retained: r}
	pendingRetainedMu.Lock()
	defer pendingRetainedMu.Unlock()
	pendingRetained, pendingAnalysisBuild = r, build
	defer func() { pendingRetained = nil; pendingAnalysisBuild = nil }()
	AnalyzeWithOptions(file, options)
	if r.a == nil || build.file == nil {
		return nil, fmt.Errorf("declaration phase did not create a resumable analysis")
	}
	return build, nil
}

// Advance analyzes at most maxDeclarations source-order declarations, or stops
// at the next declaration or final-check boundary after budget expires. Each
// final-check phase also counts against the unit limit. The late return-borrow
// phase can yield between frame checks when its time budget expires. A non-positive limit
// means no limit. A declaration is indivisible: callers must still enforce their
// process timeout. Bounded calls leave finishing checks for a subsequent step
// after the last body, so publication need not share that body's time budget.
// Calls may not overlap any other semantic analysis.
func (b *AnalysisBuild) Advance(maxDeclarations int, budget time.Duration) (*Result, *RetainedAnalysis, error) {
	if err := b.validate(); err != nil {
		return nil, nil, err
	}
	if b.done {
		return b.retained.result, b.retained, nil
	}
	pendingRetainedMu.Lock()
	defer pendingRetainedMu.Unlock()
	r := b.retained
	a := r.a
	defer a.closeSMT()
	started := time.Now()
	b.LastStats = AnalysisBuildStats{}
	defer func() { b.LastStats.Elapsed = time.Since(started) }()
	if !b.started {
		a.recordVerifiedTaskGroupSubmitsInDecls(r.decls)
		b.started = true
	}
	completed := 0
	for b.next < len(r.decls) {
		if completed > 0 && (maxDeclarations > 0 && completed >= maxDeclarations || budget > 0 && time.Since(started) >= budget) {
			break
		}
		declStart := time.Now()
		a.analyzeScopedDecl(r.decls[b.next])
		if elapsed := time.Since(declStart); elapsed > b.LastStats.Slowest {
			b.LastStats.Slowest, b.LastStats.SlowestPosition = elapsed, r.decls[b.next].Decl.Pos()
		}
		b.LastStats.Declarations++
		b.next++
		completed++
	}
	if b.postNext == 0 && !b.lateStarted {
		r.bodyEnd = len(a.diagnostics)
	}
	if b.next < len(r.decls) || completed > 0 && (maxDeclarations > 0 && completed >= maxDeclarations || budget > 0 && time.Since(started) >= budget) {
		return nil, nil, nil
	}
	// Give final checks their own bounded step after the last body, even when
	// that body left unused budget. A final check and checkpoint serialization
	// may otherwise exceed the process deadline together with the last bodies.
	// completed>0 lets a restored boundary proceed and leaves empty builds alone.
	if completed > 0 && b.postNext == 0 && (maxDeclarations > 0 || budget > 0) {
		return nil, nil, nil
	}
	// Count post phases as scheduling units too, so tests and callers can force
	// a checkpoint at each boundary without changing any semantic policy.
	for b.postNext < len(analysisPostPhaseNames) {
		if completed > 0 && (maxDeclarations > 0 && completed >= maxDeclarations || budget > 0 && time.Since(started) >= budget) {
			return nil, nil, nil
		}
		b.LastStats.LastPostPhase = analysisPostPhaseNames[b.postNext]
		if b.postNext == 0 {
			complete := false
			postTimed(analysisPostPhaseNames[0], func() { complete = b.advanceLateReturnBorrowFrames(started, budget) })
			if !complete {
				return nil, nil, nil
			}
		} else {
			a.runPostPhase(b.postNext, r.decls, r.options, nil)
		}
		b.postNext++
		b.LastStats.PostPhases++
		completed++
	}
	if completed > 0 && (maxDeclarations > 0 && completed >= maxDeclarations || budget > 0 && time.Since(started) >= budget) {
		return nil, nil, nil
	}
	result := a.finishProgramResult(b.file, b.loweredFile, r.decls, r.options, nil)
	r.result = result
	b.done = true
	return result, r, nil
}

// Stop only between complete frame checks. All summaries and frame diagnostics
// stay in the checkpoint; the final result remains unavailable until every phase
// completes. A frame is indivisible, just like a body declaration.
func (b *AnalysisBuild) advanceLateReturnBorrowFrames(started time.Time, budget time.Duration) bool {
	r := b.retained
	a := r.a
	if !b.lateStarted {
		r.lateFrames = append(r.lateFrames[:0], a.returnBorrowLateFrames...)
		a.prepareLateReturnBorrowFrames()
		b.lateLast = lastReturnBorrowFrames(r.lateFrames)
		b.lateStarted = true
	}
	frames := r.lateFrames
	consumed := 0
	for b.lateNext < len(frames) {
		if consumed > 0 && budget > 0 && time.Since(started) >= budget {
			return false
		}
		index := b.lateNext
		late := frames[index]
		if b.lateLast[late.frame.fn] == index {
			frameStart := time.Now()
			a.evaluateLateReturnBorrowFrame(late)
			if elapsed := time.Since(frameStart); elapsed > b.LastStats.Slowest {
				b.LastStats.Slowest, b.LastStats.SlowestPosition = elapsed, late.frame.fn.Pos()
			}
		}
		b.lateNext++
		b.LastStats.PostFrames++
		consumed++
	}
	if len(frames) > 0 {
		r.keepReturnBorrowSummaries(a.returnBorrowFuncSummaries)
	}
	b.lateLast = nil
	return true
}

func (b *AnalysisBuild) Progress() (completed, total int) {
	if b == nil || b.retained == nil {
		return 0, 0
	}
	return b.next, len(b.retained.decls)
}

// PostProgress reports completed whole-program checks. Body progress stays at
// its final count while these phases are resumed across processes.
func (b *AnalysisBuild) PostProgress() (int, int) {
	if b == nil {
		return 0, len(analysisPostPhaseNames)
	}
	return b.postNext, len(analysisPostPhaseNames)
}

func (b *AnalysisBuild) MatchesInvocation(filename string, src []byte, options AnalyzeOptions) bool {
	return b != nil && b.retained != nil && b.file != nil && b.file.Filename == filename && bytes.Equal(b.retained.src, src) && reflect.DeepEqual(b.retained.options, options)
}

func (b *AnalysisBuild) validate() error {
	if b == nil || b.retained == nil || b.retained.a == nil || b.file == nil || b.loweredFile == nil || b.retained.a.retained != b.retained || b.next < 0 || b.next > len(b.retained.decls) {
		return fmt.Errorf("incomplete analysis build")
	}
	if b.postNext < 0 || b.postNext > len(analysisPostPhaseNames) || b.postNext > 0 && b.next != len(b.retained.decls) || b.done && b.postNext != len(analysisPostPhaseNames) {
		return fmt.Errorf("analysis build phase mismatch")
	}
	if !b.lateStarted && (b.lateNext != 0 || b.postNext > 0) || b.lateStarted && (b.next != len(b.retained.decls) || b.lateNext < 0 || b.lateNext > len(b.retained.lateFrames) || b.postNext > 0 && b.lateNext != len(b.retained.lateFrames)) {
		return fmt.Errorf("analysis build late-frame mismatch")
	}
	if !b.lateStarted && b.lateLast != nil || b.postNext > 0 && b.lateLast != nil || b.lateStarted && b.postNext == 0 && b.lateNext < len(b.retained.lateFrames) && b.lateLast == nil {
		return fmt.Errorf("analysis build late-frame index mismatch")
	}
	if b.done != (b.retained.result != nil) {
		return fmt.Errorf("analysis build completion mismatch")
	}
	return nil
}

// MarshalState publishes only an idle checkpoint. Unsupported graph values
// reject the entire checkpoint; they are never silently dropped.
func (b *AnalysisBuild) MarshalState() ([]byte, error) {
	return b.MarshalStateWithCapacity(0)
}

// MarshalStateWithCapacity uses a prior checkpoint's size as an allocation hint.
// It preserves the same schema and validation as MarshalState.
func (b *AnalysisBuild) MarshalStateWithCapacity(capacity int) ([]byte, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}
	r := b.retained
	a := r.a
	if b.done {
		return nil, fmt.Errorf("analysis build is already complete")
	}
	if r.rbUpdating || r.depth != 0 || len(r.rbStack) != 0 || a.parallel != nil || len(a.storageViewLoopUseFrames) != 0 || len(a.returnBorrowAliasFrames) != 0 || len(a.returnBorrowAliasJournal) != 0 || len(a.returnBorrowDeferFrames) != 0 || a.smtSolver != nil {
		return nil, fmt.Errorf("analysis build is not idle")
	}
	if a.smtUnavailable {
		return nil, fmt.Errorf("solver availability was transient; retry analysis in the next process")
	}
	return semanticStateRegistry().EncodeFramedWithCapacity(b, capacity)
}

func RestoreAnalysisBuild(data []byte) (*AnalysisBuild, error) {
	value, err := semanticStateRegistry().Decode(data)
	if err != nil {
		return nil, err
	}
	b, ok := value.(*AnalysisBuild)
	if !ok {
		return nil, fmt.Errorf("not an analysis build checkpoint")
	}
	if err := b.validate(); err != nil {
		return nil, err
	}
	if b.done {
		return nil, fmt.Errorf("completed analysis is not a build checkpoint")
	}
	b.retained.post = func(decls []scopedDecl, appended map[*ast.FuncDecl]*FuncType) {
		b.retained.a.runPostPhases(decls, b.retained.options, nil, appended)
	}
	return b, nil
}
