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
// final-check phase also counts against the unit limit. A non-positive limit
// means no limit. A declaration is indivisible: callers must still enforce their
// process timeout. Finishing checks run in a subsequent step when a body limit
// is reached, so that publication need not share the last body's time budget.
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
	if b.postNext == 0 {
		r.bodyEnd = len(a.diagnostics)
	}
	if b.next < len(r.decls) || completed > 0 && (maxDeclarations > 0 && completed >= maxDeclarations || budget > 0 && time.Since(started) >= budget) {
		return nil, nil, nil
	}
	// Count post phases as scheduling units too, so tests and callers can force
	// a checkpoint at each boundary without changing any semantic policy.
	for b.postNext < len(analysisPostPhaseNames) {
		if completed > 0 && (maxDeclarations > 0 && completed >= maxDeclarations || budget > 0 && time.Since(started) >= budget) {
			return nil, nil, nil
		}
		a.runPostPhase(b.postNext, r.decls, r.options, nil)
		b.LastStats.LastPostPhase = analysisPostPhaseNames[b.postNext]
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
	if b.done != (b.retained.result != nil) {
		return fmt.Errorf("analysis build completion mismatch")
	}
	return nil
}

// MarshalState publishes only an idle checkpoint. Unsupported graph values
// reject the entire checkpoint; they are never silently dropped.
func (b *AnalysisBuild) MarshalState() ([]byte, error) {
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
	return semanticStateRegistry().Encode(b)
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
