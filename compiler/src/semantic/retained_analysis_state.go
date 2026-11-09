package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"sync"
	"time"
)

// RetainedAnalysis keeps a finished whole-program analysis for checked updates.
// Supported body/declaration edits rebuild their affected semantic state against
// a fresh declaration phase, then rerun the required whole-program checks. The
// resulting graph supplies both diagnostics and code generation.
//
// Callers must honor Update errors and fall back to fresh analysis: a late
// rejection may already have consumed or discarded the retained graph.
type RetainedAnalysis struct {
	plainDefaults *retainedPlainDefaultLifecycle

	// funcNameUsage* is an exact retained snapshot of value-position identifier
	// names, split by declaration owner so a body-only replay can replace only
	// the edited owners' contributions. The pending fields are invocation-local
	// and must be empty whenever this state is serialized.
	funcNameUsageContributions  map[ast.Decl]map[string]bool
	funcNameUsageOwnerCounts    map[ast.Decl]int
	funcNameUsageFunctionOwners map[ast.Decl]string
	funcNameUsageValueCounts    map[string]int
	funcNameUsageFunctionCounts map[string]int
	funcNameUsageOwnerTotal     int
	funcNameUsageReady          bool
	funcNameUsagePending        bool
	funcNameUsageRebuild        bool
	funcNameUsageChangedOwners  []ast.Decl
	funcNameUsageTracking       bool
	funcNameUsageOwnerLookup    map[*ast.FuncDecl]ast.Decl
	funcNameUsageTouchedOwners  map[ast.Decl]bool
	funcNameUsageUnknownTouched bool

	// DeferProofReportCoordinates lets one Update skip source relocation of the
	// proof report when this process will neither print that report nor persist
	// this state. Facts stay contribution-correct but carry pre-edit coordinates,
	// so a state that skipped relocation refuses publication and further updates.
	DeferProofReportCoordinates bool
	proofCoordinatesDeferred    bool
	indexProofsUpdating         bool                                      // invocation-local: invalidate before ordinary function reanalysis
	indexProofsByFunc           map[*ast.FuncDecl]map[*ast.IndexExpr]bool // reverse ownership, only during Update
	indexProofsByDecl           map[ast.Node]map[*ast.IndexExpr]bool
	// classify is the full analysis's program-wide classification state (fast preparation).
	classify   *retainedClassifyState
	appendOnly *retainedAppendOnlyState
	tables     *retainedFactTables
	digestOf   map[*ast.FuncDecl]string // local-fact digest of each spliced edit, computed before analysis rewrote it
	// Persist invalidation across MarshalState/RestoreAnalysisState. A scratch-
	// backed update can preserve the live analysis without rebuilding the fast
	// preparer's declaration-phase snapshots.
	fastPrepareInvalidated            string
	a                                 *Analyzer
	result                            *Result
	decls                             []scopedDecl
	resolvedTypeNameSourceOwners      map[ast.TypeExpr]resolvedTypeNameSourceOwner // immutable pre-body source ownership for the live AST
	resolvedTypeNameSourceOwnersValid bool
	post                              func([]scopedDecl, map[*ast.FuncDecl]*FuncType)
	pristine                          *ast.File                            // a parse of src, for comparison; see texts
	texts                             map[*ast.FuncDecl]funcComparisonText // pristine functions whose bodies were spliced away
	src                               []byte                               // the expanded source pristine was parsed from

	depth      int
	windows    map[*ast.FuncDecl][2]int // body-phase diagnostics emitted by each top-level analyzeFunc
	bodyEnd    int                      // len(diagnostics) when analyzeDecls returned
	lateFrames []returnBorrowLateFrame  // evaluateLateReturnBorrowFrames consumes them; keep a copy

	// Late return-borrow pass reuse (retained_return_borrow.go).
	rbStack     []*ast.FuncDecl
	rbDeps      map[*ast.FuncDecl]map[*ast.FuncDecl]bool // owner -> summaries it consulted
	rbSummaries map[*ast.FuncDecl]returnBorrowFlow       // final summaries of the last late pass
	frameDiags  map[*ast.FuncDecl][]Diagnostic           // each late frame's diagnostics
	rbInvalid   map[*ast.FuncDecl]bool                   // owners to recompute in this update
	rbChanged   []*ast.FuncDecl                          // functions whose bodies this update replaced
	rbSameFlow  map[*ast.FuncDecl]bool                   // changed functions whose return-borrow summary this update keeps
	rbCallers   map[*ast.FuncDecl][]*ast.FuncDecl        // possible callee -> possible caller, current bodies
	rbStaleDeps map[*ast.FuncDecl]map[*ast.FuncDecl]bool // pre-update deps of rbInvalid
	rbCutoff    bool                                     // the late pass may narrow rbInvalid
	rbUpdating  bool
	perms       retainedPermissions
	bodyUses    map[*ast.FuncDecl]retainedBodyUses // exact, idle snapshot body dependencies

	options AnalyzeOptions
	pre     *preBodyState // declaration-phase results the retained state was built from

	LastStats RetainedUpdateStats
}

// RetainedUpdateStats describes the last Update.
type RetainedUpdateStats struct {
	Changed                   int
	Shifted                   int
	Diff                      time.Duration
	Bodies                    time.Duration
	Post                      time.Duration
	ScratchPrepared           bool   // the scratch phase ran ahead, overlapped with the driver's parse
	FastPrepared              bool   // edited bodies were prepared without waiting for the scratch
	FastDeclined              string // why a body-only edit still needed the scratch
	FastAdded                 bool   // added functions were declared without the whole-program scratch
	Total                     time.Duration
	Fallback                  string
	Added                     int
	Removed                   int
	StructUsers               int // functions re-analyzed for an edited struct (with the edited ones)
	StructUnobserved          int // struct users kept: an additive scalar edit they cannot observe
	ConstUsers                int // functions re-analyzed for an edited const (with the edited ones)
	EnumUsers                 int // functions re-analyzed for an edited enum (with the edited ones)
	Pairing, Scratch, Compare time.Duration
	// Invalidated counts functions whose late return-borrow results were recomputed.
	Invalidated int
}

var pendingRetainedMu sync.Mutex

// AnalyzeRetained runs a full analysis of file and keeps it for Update.
// pristine must be a separate parse of the same source that analysis never
// touches. Calls are serialized; do not run other analyses concurrently.
func AnalyzeRetained(file, pristine *ast.File, src []byte, options AnalyzeOptions) (*Result, *RetainedAnalysis) {
	r := &RetainedAnalysis{options: options, pristine: pristine, src: src, windows: map[*ast.FuncDecl][2]int{}, rbDeps: map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}, frameDiags: map[*ast.FuncDecl][]Diagnostic{}}
	pendingRetainedMu.Lock()
	result := analyzeWithLookupRecorder(file, options, nil, nil, nil, &analysisSetup{retained: r})
	pendingRetainedMu.Unlock()
	if r.a == nil {
		return result, nil
	}
	r.result = result
	r.refreshPlainDefaultProducts()
	return result, r
}

func (r *RetainedAnalysis) capture(a *Analyzer, decls []scopedDecl, post func([]scopedDecl, map[*ast.FuncDecl]*FuncType)) {
	r.a, r.decls, r.post = a, decls, post
}

// pruneFastPrepareDigests drops declarations from older comparison parses. The
// digest map is part of the retained snapshot graph, so stale keys would keep
// their source ASTs (including bodies) alive after pristine advances.
func (r *RetainedAnalysis) pruneFastPrepareDigests(currentDecls []ast.Decl) {
	if r == nil || len(r.digestOf) == 0 {
		return
	}
	current := make(map[*ast.FuncDecl]struct{})
	for _, decl := range currentDecls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			current[fn] = struct{}{}
		}
	}
	for fn := range r.digestOf {
		if _, ok := current[fn]; !ok {
			delete(r.digestOf, fn)
		}
	}
}

// Update applies an edit given a fresh, unanalyzed parse of the edited source.
// Changed function bodies move from file into the retained AST, and file
// becomes the base the next edit is compared against, so the caller must not
// analyze file afterwards. A non-nil error names why the edit is not a
// body-only change; the retained state and file are then untouched, and the
// caller should start over with a full analysis.
func (r *RetainedAnalysis) Update(file, scratch *ast.File, src []byte) (*Result, error) {
	return r.UpdatePrepared(file, scratch, src, nil)
}

// RetainedScratch is a scratch parse with its declaration phase already run,
// so a server can overlap that phase with its own parse of the request.
type RetainedScratch struct {
	file         *ast.File
	pre          *preBodyState
	elapsed      time.Duration
	ready        chan struct{} // closed once pre is set; nil when prepared synchronously
	once         sync.Once
	lazy         *AnalyzeOptions           // set: Wait runs the phase on demand
	parse        func() (*ast.File, error) // optional independent parse, materialized only on decline
	parseOnce    sync.Once
	parseErr     error
	parseElapsed time.Duration
}

// NewPendingRetainedScratch returns a scratch whose declaration phase Prepare runs later,
// typically on another goroutine. UpdatePrepared waits for it only when it needs it.
func NewPendingRetainedScratch(scratch *ast.File) *RetainedScratch {
	return &RetainedScratch{file: scratch, ready: make(chan struct{})}
}

// NewLazyRetainedScratch returns a scratch whose declaration phase runs only when an update
// needs it, on the caller's goroutine, so it never competes with work that may make it moot.
func NewLazyRetainedScratch(scratch *ast.File, options AnalyzeOptions) *RetainedScratch {
	s := NewPendingRetainedScratch(scratch)
	s.lazy = &options
	return s
}

// NewLazyParsedRetainedScratch defers both parsing and declaration preparation.
// parse must return a fresh, independent AST of the exact source being updated.
// It runs at most once on demand; no goroutine starts here.
func NewLazyParsedRetainedScratch(parse func() (*ast.File, error), options AnalyzeOptions) *RetainedScratch {
	if parse == nil {
		parse = func() (*ast.File, error) { return nil, fmt.Errorf("missing independent scratch parser") }
	}
	s := NewLazyRetainedScratch(nil, options)
	s.parse = parse
	return s
}

// AcceptsScratch validates the caller's explicit scratch identity. A nil AST is
// admitted only for a lazy parser; it does not identify an ordinary scratch.
func (s *RetainedScratch) AcceptsScratch(scratch *ast.File) bool {
	if s == nil {
		return false
	}
	if s.parse != nil {
		return scratch == nil
	}
	return scratch != nil && s.file == scratch
}

func (s *RetainedScratch) materialize() error {
	if s.parse != nil {
		s.parseOnce.Do(func() {
			started := time.Now()
			defer func() { s.parseElapsed = time.Since(started) }()
			s.file, s.parseErr = s.parse()
			if s.parseErr == nil && s.file == nil {
				s.parseErr = fmt.Errorf("scratch parser returned no file")
			}
		})
	}
	return s.parseErr
}

// reject completes an unstarted preparation without analyzing its AST. An owner
// may still join the rejected future; joining must never prepare a primary AST
// that UpdatePrepared has just rejected as an alias.
func (s *RetainedScratch) reject(err error) {
	s.once.Do(func() {
		s.parseErr = err
		if s.ready != nil {
			close(s.ready)
		}
	})
}

// Prepare runs a pending scratch's declaration phase; later calls do nothing.
func (s *RetainedScratch) Prepare(options AnalyzeOptions) {
	s.once.Do(func() {
		defer close(s.ready)
		if err := s.materialize(); err != nil {
			s.elapsed = s.parseElapsed
			return
		}
		done := PrepareRetainedScratch(s.file, options)
		s.pre, s.elapsed = done.pre, s.parseElapsed+done.elapsed
	})
}

// Wait blocks until a pending scratch is prepared, preparing a lazy one first.
func (s *RetainedScratch) Wait() {
	if s == nil || s.ready == nil {
		return
	}
	if s.lazy != nil {
		s.Prepare(*s.lazy)
	}
	<-s.ready
}

// Discard cancels an unstarted lazy scratch without doing analysis. For a
// pending scratch, it joins the owner's preparation so no request work escapes
// into a later request's environment or working directory. The owner must call
// it before UpdatePrepared starts or after UpdatePrepared returns; concurrent
// use of one scratch by UpdatePrepared and Discard is unsupported.
func (s *RetainedScratch) Discard() {
	if s == nil || s.ready == nil {
		return
	}
	if s.lazy != nil {
		s.reject(fmt.Errorf("retained scratch discarded"))
		return
	}
	s.Wait()
}

// PrepareRetainedScratch runs Update's scratch declaration phase ahead of time.
// The scratch file is analyzed in place and must only be passed to
// UpdatePrepared, with the same options the retained analysis was built with.
func PrepareRetainedScratch(scratch *ast.File, options AnalyzeOptions) *RetainedScratch {
	pendingRetainedMu.Lock()
	defer pendingRetainedMu.Unlock()
	start := time.Now()
	pre := scratchPreBody(scratch, options)
	return &RetainedScratch{file: scratch, pre: pre, elapsed: time.Since(start)}
}
