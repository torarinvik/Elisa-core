package semantic

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
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

	indexProofsUpdating bool                                      // invocation-local: invalidate before ordinary function reanalysis
	indexProofsByFunc   map[*ast.FuncDecl]map[*ast.IndexExpr]bool // reverse ownership, only during Update
	indexProofsByDecl   map[ast.Node]map[*ast.IndexExpr]bool
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

var (
	pendingRetainedMu sync.Mutex
	pendingRetained   *RetainedAnalysis
)

func takePendingRetained() *RetainedAnalysis {
	r := pendingRetained
	pendingRetained = nil
	return r
}

// AnalyzeRetained runs a full analysis of file and keeps it for Update.
// pristine must be a separate parse of the same source that analysis never
// touches. Calls are serialized; do not run other analyses concurrently.
func AnalyzeRetained(file, pristine *ast.File, src []byte, options AnalyzeOptions) (*Result, *RetainedAnalysis) {
	r := &RetainedAnalysis{options: options, pristine: pristine, src: src, windows: map[*ast.FuncDecl][2]int{}, rbDeps: map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}, frameDiags: map[*ast.FuncDecl][]Diagnostic{}}
	pendingRetainedMu.Lock()
	pendingRetained = r
	result := AnalyzeWithOptions(file, options)
	pendingRetained = nil
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

// UpdatePrepared is Update with an optional prepared scratch phase; it is used
// only when it was prepared from scratch itself.
func (r *RetainedAnalysis) UpdatePrepared(file, scratch *ast.File, src []byte, prepared *RetainedScratch) (*Result, error) {
	r.indexProofsUpdating = true
	r.indexProofsByFunc = map[*ast.FuncDecl]map[*ast.IndexExpr]bool{}
	if r.a != nil {
		for expr, proven := range r.a.indexBoundsProven {
			if !proven || expr == nil {
				continue
			}
			fn, owned := r.a.indexBoundsProofOwners[expr]
			if !owned || fn == nil {
				r.indexProofsUpdating = false
				r.indexProofsByFunc = nil
				r.indexProofsByDecl = nil
				r.LastStats = RetainedUpdateStats{Fallback: "retained index proof has no exact semantic ownership; retry with a full analysis"}
				return nil, fmt.Errorf("%s", r.LastStats.Fallback)
			}
			r.rememberFunctionIndexProof(fn, expr)
		}
	}
	if r.a != nil {
		for expr, site := range r.a.indexBoundsRuntimeSites {
			if expr == nil || site.Owner == nil {
				r.indexProofsUpdating, r.indexProofsByFunc, r.indexProofsByDecl = false, nil, nil
				r.LastStats = RetainedUpdateStats{Fallback: "retained runtime bounds site has no exact lexical ownership; retry with a full analysis"}
				return nil, fmt.Errorf("%s", r.LastStats.Fallback)
			}
			r.rememberFunctionIndexProof(site.Owner, expr)
		}
	}
	defer func() { r.indexProofsUpdating = false; r.indexProofsByFunc = nil; r.indexProofsByDecl = nil }()
	start := time.Now()
	defer func() { r.bodyUses = nil }()
	r.LastStats = RetainedUpdateStats{}
	r.rbSameFlow = map[*ast.FuncDecl]bool{}
	fail := func(format string, args ...any) (*Result, error) {
		r.LastStats.Fallback = fmt.Sprintf(format, args...)
		return nil, fmt.Errorf("%s", r.LastStats.Fallback)
	}
	a := r.a
	if a == nil {
		return fail("retained analysis was discarded by an earlier update")
	}
	oldDecls, newDecls := retainedWalkDecls(r.pristine), retainedWalkDecls(file)
	// Pair declarations in order. A new declaration with no partner is accepted
	// only as an added top-level function with a fresh name: it is declared in the
	// live analysis below and checked against the scratch declaration phase.
	topLevel := make(map[ast.Decl]bool, len(file.Decls))
	for _, d := range file.Decls {
		topLevel[d] = true
	}
	// A function directly inside `module`/`extend` blocks is added the same way,
	// in its block's namespace; memberOf maps it to its innermost block.
	memberOf := map[ast.Decl]*ast.NamespaceDecl{}
	namespaceOf := map[*ast.NamespaceDecl]string{}
	var collectMembers func(decls []ast.Decl, namespace string)
	collectMembers = func(decls []ast.Decl, namespace string) {
		for _, d := range decls {
			if n, ok := d.(*ast.NamespaceDecl); ok && !n.Const {
				full := joinQualifiedName(namespace, n.Name)
				namespaceOf[n] = full
				for _, m := range n.Decls {
					memberOf[m] = n
				}
				collectMembers(n.Decls, full)
			}
		}
	}
	collectMembers(file.Decls, "")
	newNames := map[string]bool{}
	oldValueNames := map[string]bool{} // consts, globals and functions: names an added const must not take
	oldTypeNames := map[string]bool{}  // structs and enums: names an added struct must not take
	oldFunctionNames := retainedQualifiedFunctionNames(r.pristine)
	newFunctionNames := retainedQualifiedFunctionNames(file)
	oldFuncNameCount := map[string]int{}
	for _, d := range oldDecls {
		if f, ok := d.(*ast.FuncDecl); ok {
			if oldFunctionNames[f] == "" {
				oldFunctionNames[f] = f.Name
			}
			oldFuncNameCount[oldFunctionNames[f]]++
		}
		switch n := d.(type) {
		case *ast.StructDecl:
			oldTypeNames[n.Name] = true
		case *ast.EnumDecl:
			oldTypeNames[n.Name] = true
		case *ast.FuncDecl:
			oldValueNames[n.Name] = true
		case *ast.ConstDecl:
			oldValueNames[n.Name] = true
		case *ast.GlobalDecl:
			oldValueNames[n.Name] = true
		}
	}
	newValueNames := map[string]bool{} // consts, globals and functions of the edited program
	newFuncNameCount := map[string]int{}
	newDeclIndex := make(map[ast.Decl]int, len(newDecls))
	for j, d := range newDecls {
		newDeclIndex[d] = j
		if f, ok := d.(*ast.FuncDecl); ok {
			if newFunctionNames[f] == "" {
				newFunctionNames[f] = f.Name
			}
			newFuncNameCount[newFunctionNames[f]]++
		}
	}
	newTypeNames := map[string]bool{} // structs and enums of the edited program
	for _, d := range newDecls {
		switch n := d.(type) {
		case *ast.StructDecl:
			newTypeNames[n.Name] = true
		case *ast.EnumDecl:
			newTypeNames[n.Name] = true
		case *ast.FuncDecl:
			newNames[n.Name] = true
			newValueNames[n.Name] = true
		case *ast.ConstDecl:
			newValueNames[n.Name] = true
		case *ast.GlobalDecl:
			newValueNames[n.Name] = true
		}
	}
	oldTop := make(map[ast.Decl]bool, len(r.pristine.Decls))
	for _, d := range r.pristine.Decls {
		oldTop[d] = true
	}
	// Removing a direct module/extend member follows the same live-symbol
	// pruning as removing a top-level function. Keep oldTop separate because
	// signature edits also use it to verify that a declaration stayed in scope.
	oldRemovable := map[ast.Decl]bool{}
	var collectRemovable func([]ast.Decl)
	collectRemovable = func(decls []ast.Decl) {
		for _, d := range decls {
			oldRemovable[d] = true
			if ns, ok := d.(*ast.NamespaceDecl); ok && !ns.Const {
				collectRemovable(ns.Decls)
			}
		}
	}
	collectRemovable(r.pristine.Decls)
	var removed []*ast.FuncDecl
	var pairs [][2]int
	var added []*ast.FuncDecl
	var addedConsts []ast.Decl           // added typed consts and globals with fresh names
	var removedConsts []ast.Decl         // consts and globals whose name is gone from the edited program
	var addedStructs []*ast.StructDecl   // added plain structs with fresh names
	var removedStructs []*ast.StructDecl // plain structs whose name is gone from the edited program
	insertedBefore := map[int]bool{}     // pair index preceded by an added declaration
	overloadNames := map[string]bool{}   // qualified names with an edited overload set
	usingsAdded, usingsRemoved := 0, 0   // wildcard `using` lines added or removed
	for i, j := 0, 0; i < len(oldDecls) || j < len(newDecls); {
		if i < len(oldDecls) && j < len(newDecls) && retainedSameDeclKind(oldDecls[i], newDecls[j]) {
			pairs = append(pairs, [2]int{i, j})
			i, j = i+1, j+1
			continue
		}
		// A removed struct or const: its name is gone from the edited source.
		if i < len(oldDecls) {
			if ost, ok := oldDecls[i].(*ast.StructDecl); ok && retainedPlainStruct(ost) && !newTypeNames[ost.Name] && !newValueNames[ost.Name] {
				removedStructs = append(removedStructs, ost)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
		}
		if i < len(oldDecls) {
			if oc, ok := oldDecls[i].(*ast.ConstDecl); ok && oc.Type != nil && !newValueNames[oc.Name] {
				removedConsts = append(removedConsts, oc)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
			if og, ok := oldDecls[i].(*ast.GlobalDecl); ok && og.Type != nil && !newValueNames[og.Name] {
				removedConsts = append(removedConsts, og)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
		}
		// A removed wildcard `using`: its block's declarations take their new usings below.
		if i < len(oldDecls) && retainedWildcardUsing(oldDecls[i]) {
			usingsRemoved++
			insertedBefore[len(pairs)] = true
			i++
			continue
		}
		// A removed top-level or module/extend function: its name is gone.
		if i < len(oldDecls) {
			if of, ok := oldDecls[i].(*ast.FuncDecl); ok && oldRemovable[of] && newFuncNameCount[oldFunctionNames[of]] == 0 && retainedPlainFunction(of) {
				removed = append(removed, of)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
			// A removed overload: the name survives in another same-named function, which is
			// re-declared with the rest of its overload set below.
			if of, ok := oldDecls[i].(*ast.FuncDecl); ok && newFuncNameCount[oldFunctionNames[of]] != 0 && retainedPlainFunction(of) &&
				oldFuncNameCount[oldFunctionNames[of]] > newFuncNameCount[oldFunctionNames[of]] &&
				(j == len(newDecls) || !retainedSameDeclKind(of, newDecls[j])) {
				overloadNames[oldFunctionNames[of]] = true
				removed = append(removed, of)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
		}
		if j == len(newDecls) {
			return fail("declaration count changed (%d -> %d)", len(oldDecls), len(newDecls))
		}
		if retainedWildcardUsing(newDecls[j]) {
			usingsAdded++
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		if sd, ok := newDecls[j].(*ast.StructDecl); ok && retainedPlainStruct(sd) && (topLevel[sd] || memberOf[sd] != nil) && !oldTypeNames[sd.Name] && !oldValueNames[sd.Name] {
			addedStructs = append(addedStructs, sd)
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		if cd, ok := newDecls[j].(*ast.ConstDecl); ok && cd.Type != nil && (topLevel[cd] || memberOf[cd] != nil) && !oldValueNames[cd.Name] && !newNames[cd.Name] {
			addedConsts = append(addedConsts, cd)
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		if gd, ok := newDecls[j].(*ast.GlobalDecl); ok && gd.Type != nil && (topLevel[gd] || memberOf[gd] != nil) && !oldValueNames[gd.Name] && !newNames[gd.Name] {
			addedConsts = append(addedConsts, gd)
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		nf, ok := newDecls[j].(*ast.FuncDecl)
		if ok && oldFuncNameCount[newFunctionNames[nf]] != 0 && newFuncNameCount[newFunctionNames[nf]] > oldFuncNameCount[newFunctionNames[nf]] &&
			(topLevel[nf] || memberOf[nf] != nil) && retainedPlainFunction(nf) {
			// An overload of an existing name: registration order decides which one owns the
			// bare name, so every same-named function is re-declared with it below.
			overloadNames[newFunctionNames[nf]] = true
			added = append(added, nf)
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		if !ok || !(topLevel[nf] || memberOf[nf] != nil) || oldFuncNameCount[newFunctionNames[nf]] != 0 || !retainedPlainFunction(nf) {
			return fail("declaration set changed at %s", retainedLineKey(newDecls[j].Pos()))
		}
		added = append(added, nf)
		insertedBefore[len(pairs)] = true
		j++
	}
	if len(pairs)+len(removed)+len(removedConsts)+len(removedStructs)+usingsRemoved != len(oldDecls) {
		return fail("declaration count changed (%d -> %d)", len(oldDecls), len(newDecls))
	}
	type fnKey struct {
		pos  string
		name string
	}
	// The analyzed AST also holds declarations the analyzer synthesized, so pair
	// its functions by source position and name rather than by index.
	type declKey struct {
		pos  string
		kind reflect.Type
	}
	liveOther := map[declKey][]ast.Decl{}
	liveFns := map[fnKey][]*ast.FuncDecl{}
	for _, d := range retainedWalkDecls(a.file) {
		if f, ok := d.(*ast.FuncDecl); ok {
			k := fnKey{retainedLineKey(f.Pos()), f.Name}
			liveFns[k] = append(liveFns[k], f)
		} else {
			k := declKey{retainedLineKey(d.Pos()), reflect.TypeOf(d)}
			liveOther[k] = append(liveOther[k], d)
		}
	}
	scoped := make(map[*ast.FuncDecl]scopedDecl, len(r.decls))
	for _, sd := range r.decls {
		if f, ok := sd.Decl.(*ast.FuncDecl); ok {
			scoped[f] = sd
		}
	}
	type splice struct {
		live *ast.FuncDecl
		edit *ast.FuncDecl // the function in file, found again in the prepared scratch parse
	}
	var splices []splice
	var shadowEdits [][3]*ast.FuncDecl             // live, old parse, new parse of each edited body (fast-prepare shadow)
	var movedSplices []ast.Decl                    // edited functions that also moved: header shifts, body is replaced
	pairedNew := map[*ast.FuncDecl]*ast.FuncDecl{} // live function -> its declaration in file
	replaced := map[string]bool{}                  // fingerprint keys of functions whose signature changed
	var shifted []ast.Decl
	var shiftedOther []ast.Decl
	// Lists an added declaration was appended to; sorted back into source order at the end.
	appendedInto := map[*ast.NamespaceDecl]bool{}
	appendedTop := false
	var structPairs [][2]*ast.StructDecl // edited plain structs: (previous, edited)
	var constPairs [][2]*ast.ConstDecl   // typed consts with a new value: (previous, edited)
	var constEnumPairs [][2]*ast.ConstEnumDecl
	var enumPairs [][2]*ast.EnumDecl      // edited plain enums: (previous, edited)
	var hierarchyPairs [][2]*ast.EnumDecl // edited refinements of an enum hierarchy
	newTexts := map[*ast.FuncDecl]funcComparisonText{}
	lineDelta := map[string]int{}
	marks := map[string][][2]int{} // per file: (old line, delta from that line on)
	oldSpans := retainedSpans(oldDecls, r.src)
	newSpans := retainedSpans(newDecls, src)
	for k, pair := range pairs {
		i, j := pair[0], pair[1]
		od, nd := oldDecls[i], newDecls[j]
		op, np := od.Pos(), nd.Pos()
		if op.File != np.File || op.Col != np.Col {
			return fail("declaration %d moved", i)
		}
		// Any line movement must come from a declaration whose text changed; the
		// span compare below makes that declaration a splice. A span ends at the next
		// `#line` directive, so the text above a file region's first declaration is in no
		// span and holds no declaration: a movement there (comments, blank lines) is a
		// plain shift.
		if d := np.Line - op.Line; d != lineDelta[op.File] {
			if !insertedBefore[k] && k > 0 && oldSpans[i-1] == newSpans[pairs[k-1][1]] && oldDecls[i-1].Pos().File == op.File {
				return fail("lines moved after an unchanged declaration at %s", retainedLineKey(op))
			}
			lineDelta[op.File] = d
			marks[op.File] = append(marks[op.File], [2]int{op.Line, d})
		}
		sameText := oldSpans[i] == newSpans[j]
		ofn, isFn := od.(*ast.FuncDecl)
		if !isFn {
			// A container's span is its own header up to its first member. Comment and blank
			// lines trailing a declaration move with an insertion after it; they are not part of
			// what the declaration declares.
			if sameText || retainedTrimTrailing(oldSpans[i]) == retainedTrimTrailing(newSpans[j]) {
				if lineDelta[op.File] != 0 {
					candidates := liveOther[declKey{retainedLineKey(op), reflect.TypeOf(od)}]
					if len(candidates) != 1 {
						return fail("%d analyzed candidates for %T at %s", len(candidates), od, retainedLineKey(op))
					}
					shiftedOther = append(shiftedOther, candidates[0])
				}
				continue
			}
			if !sameText {
				if osd, ok := od.(*ast.StructDecl); ok {
					if nsd, ok := nd.(*ast.StructDecl); ok && osd.Name == nsd.Name && retainedStructFieldEditAllowed(osd, nsd) {
						structPairs = append(structPairs, [2]*ast.StructDecl{osd, nsd})
						continue
					}
				}
				if oed, ok := od.(*ast.EnumDecl); ok {
					if ned, ok := nd.(*ast.EnumDecl); ok && oed.Name == ned.Name && retainedPlainEnum(oed) && retainedPlainEnum(ned) {
						enumPairs = append(enumPairs, [2]*ast.EnumDecl{oed, ned})
						continue
					}
					if ned, ok := nd.(*ast.EnumDecl); ok && oed.Name == ned.Name && oed.Parent == ned.Parent &&
						retainedRefinementEnum(oed) && retainedRefinementEnum(ned) {
						hierarchyPairs = append(hierarchyPairs, [2]*ast.EnumDecl{oed, ned})
						continue
					}
				}
				if oe, ok := od.(*ast.ConstEnumDecl); ok {
					if ne, ok := nd.(*ast.ConstEnumDecl); ok && retainedConstEnumEditAllowed(oe, ne, oldSpans[i], newSpans[j]) {
						constEnumPairs = append(constEnumPairs, [2]*ast.ConstEnumDecl{oe, ne})
						continue
					}
				}
				if ocd, ok := od.(*ast.ConstDecl); ok {
					if ncd, ok := nd.(*ast.ConstDecl); ok && retainedConstValueEdit(ocd, ncd, oldSpans[i], newSpans[j]) {
						constPairs = append(constPairs, [2]*ast.ConstDecl{ocd, ncd})
						continue
					}
				}
				if os.Getenv("ELISACORE_RETAINED_DEBUG_SPANS") != "" {
					fmt.Fprintf(os.Stderr, "[span-old %T]\n%q\n[span-new]\n%q\n", od, oldSpans[i], newSpans[j])
				}
				return fail("declaration at %s changed", retainedLineKey(op))
			}
			continue
		}
		nfn, ok := nd.(*ast.FuncDecl)
		if !ok {
			return fail("declaration kind changed at %s", retainedLineKey(op))
		}
		cands := liveFns[fnKey{retainedLineKey(op), ofn.Name}]
		if len(cands) != 1 {
			return fail("%d analyzed candidates for %s at %s", len(cands), ofn.Name, retainedLineKey(op))
		}
		live := cands[0]
		pairedNew[live] = nfn
		forceReplace := overloadNames[oldFunctionNames[ofn]] || overloadNames[newFunctionNames[nfn]]
		if sameText && !forceReplace {
			if lineDelta[op.File] != 0 {
				shifted = append(shifted, live)
			}
			continue
		}
		oldText, newText := r.comparisonText(ofn), retainedText(nfn)
		if oldText.header != newText.header || forceReplace {
			// A new signature is a removal of the old declaration and an addition
			// of the new one: callers are re-analyzed, the new one is checked
			// against scratch.
			// A member of a `module`/`extend` block is replaced in its block the same way.
			sameScope := oldTop[ofn] && topLevel[nfn]
			namespace := ""
			if block := memberOf[nfn]; !sameScope && block != nil && !oldTop[ofn] && !topLevel[nfn] {
				if sd, known := scoped[live]; known && sd.Namespace == namespaceOf[block] {
					sameScope, namespace = true, sd.Namespace
				}
			}
			genericRename := retainedGenericFunctionBinderRename(ofn, nfn)
			if !sameScope || (!(retainedPlainFunction(ofn) && retainedPlainFunction(nfn)) && !genericRename) {
				return fail("signature of %s changed", ofn.Name)
			}
			if genericRename && retainedGenericFunctionHasNonBodyUsers(oldDecls, ofn) {
				return fail("generic function %s has non-body consumers", ofn.Name)
			}
			removed = append(removed, ofn)
			added = append(added, nfn)
			replaced[namespace+"::"+ofn.Name+"#0"] = true
			if forceReplace {
				// Every occurrence of an overloaded name is re-declared.
				for k := 1; k < newFuncNameCount[newFunctionNames[nfn]]; k++ {
					replaced[fmt.Sprintf("%s::%s#%d", namespace, ofn.Name, k)] = true
				}
			}
			continue
		}
		if oldText.gensyms != newText.gensyms {
			return fail("%s mentions renamed synthesized declarations", ofn.Name)
		}
		if _, known := scoped[live]; !known {
			return fail("%s is not a top-level analyzed function", ofn.Name)
		}
		if np.Line != op.Line {
			movedSplices = append(movedSplices, live)
		}
		splices = append(splices, splice{live, nfn})
		shadowEdits = append(shadowEdits, [3]*ast.FuncDecl{live, ofn, nfn})
		newTexts[nfn] = newText
	}
	reportDeclarationImport := false
	var relocateProofReport func(*Analyzer) string
	if r.a != nil {
		changedOwners := make([]*ast.FuncDecl, 0, len(splices))
		for _, edit := range splices {
			changedOwners = append(changedOwners, edit.live)
		}
		declarationChange := len(added)+len(removed)+len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs)+len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs)+usingsAdded+usingsRemoved != 0
		onlyFunctionRemovals := len(removed) != 0 && len(added)+len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs)+len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs)+usingsAdded+usingsRemoved == 0
		removedOwners := make([]*ast.FuncDecl, 0, len(removed))
		if onlyFunctionRemovals {
			for _, old := range removed {
				candidates := liveFns[fnKey{retainedLineKey(old.Pos()), old.Name}]
				if len(candidates) != 1 {
					return fail("proof report removed owner cannot be paired")
				}
				removedOwners = append(removedOwners, candidates[0])
			}
			declarationChange = false
		}
		reportDeclarationImport = declarationChange
		reportLocationsChanged := len(marks) != 0
		for live, fresh := range pairedNew {
			if fresh.Pos().Offset != live.Pos().Offset {
				reportLocationsChanged = true
			}
		}
		if reportLocationsChanged && len(r.a.proofReport) != 0 {
			var why string
			relocateProofReport, why = r.prepareProofReportCoordinates(file, marks)
			if why != "" {
				return fail("proof report source relocation: %s", why)
			}
		}
		// A complete coordinate adapter replaces the location-only admission
		// refusal. Dependency and source-journal admission remain authoritative.
		if why := r.admitProofReportUpdate(changedOwners, removedOwners, false, declarationChange); why != "" {
			return fail("%s", why)
		}
	}
	// Bodies are analyzed in the form the declaration phase leaves them in, and
	// that phase's body-dependent results must be what the retained state holds.
	pairing := time.Since(start)
	var pre *preBodyState
	var fastBodies [][]ast.Stmt
	var fastResolvedTypeNameRows map[string]map[ast.TypeExpr]string
	var fastResolvedTypeNameSourceOwners map[ast.TypeExpr]resolvedTypeNameSourceOwner
	bodyEnvironmentUnchanged := len(added)+len(removed)+len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs) == 0 &&
		len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs) == 0 &&
		len(replaced)+len(insertedBefore)+len(overloadNames)+len(appendedInto)+len(movedSplices)+len(shifted)+len(shiftedOther) == 0 &&
		usingsAdded+usingsRemoved == 0 && !appendedTop && retainedNoLineDelta(lineDelta)
	// Location-only movement can use the complete body certificate once. Keep
	// bodyEnvironmentUnchanged stricter: its end-of-update invalidation prevents
	// reusing location-dependent classifier/digest snapshots after this update.
	fastBodyEnvironmentUnchanged := len(added)+len(removed)+len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs) == 0 &&
		len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs) == 0 &&
		len(replaced)+len(insertedBefore)+len(overloadNames)+len(appendedInto) == 0 &&
		usingsAdded+usingsRemoved == 0 && !appendedTop
	if retainedFastPrepareMode == "on" && len(shadowEdits) != 0 && len(shadowEdits) == len(splices) && fastBodyEnvironmentUnchanged {
		var why string
		if !bodyEnvironmentUnchanged {
			why = r.fastLineShiftAuthority(a, marks)
		}
		if why == "" {
			fastBodies, fastResolvedTypeNameRows, why = r.fastPathBodies(a, shadowEdits, scoped, oldDecls)
			if why == "" {
				var ownershipErr error
				fastResolvedTypeNameSourceOwners, ownershipErr = resolvedTypeNameFastBodySourceOwners(shadowEdits, fastBodies, retainedPreBodyFunctionKeys(r.decls))
				if ownershipErr != nil {
					why = ownershipErr.Error()
				}
			}
		}
		if why != "" {
			fastBodies = nil
			fastResolvedTypeNameRows = nil
			fastResolvedTypeNameSourceOwners = nil
			r.LastStats.FastDeclined = why
		}
	}
	if fastBodies != nil {
		// The edit leaves every declaration-level result as the retained baseline holds it.
		// The new prefix is independently relocated; mutating/aliasing the old
		// baseline would invalidate preBodyDifference and later disk revisions.
		diags := append([]Diagnostic(nil), r.pre.diags...)
		owners := retainedDiagnosticOwners(r.decls, nil, func(decl ast.Decl) ast.Decl {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				return pairedNew[fn]
			}
			return nil
		})
		for i := range diags {
			if strings.Contains(diags[i].Message, "__") {
				if owner, shift := owners.ownerAt(diags[i].Pos); owner != nil {
					diags[i].Message = retainedShiftLocalGensyms(diags[i].Message, shift)
				}
			}
			diags[i].Pos = retainedShiftPos(diags[i].Pos, marks)
		}
		pre = &preBodyState{classification: r.pre.classification, fingerprint: r.pre.fingerprint, global: r.pre.global, resolvedTypeNames: r.pre.resolvedTypeNames, resolvedTypeNameSourceOwners: fastResolvedTypeNameSourceOwners, resolvedTypeNameRows: fastResolvedTypeNameRows, diags: diags, plainHeaders: r.pre.plainHeaders, plainGlobal: r.pre.plainGlobal, plainInputs: r.pre.plainInputs, plainIdentities: r.pre.plainIdentities, hasProofReport: r.pre.hasProofReport}
		r.LastStats.FastPrepared = true
	} else if prepared.AcceptsScratch(scratch) {
		if err := prepared.materialize(); err != nil {
			return fail("scratch parse failed: %v", err)
		}
		if prepared.file == nil || prepared.file == file {
			prepared.reject(fmt.Errorf("scratch parse must be independent of the primary file"))
			return fail("scratch parse must be independent of the primary file")
		}
		scratch = prepared.file
		prepared.Wait()
		if prepared.parseErr != nil || prepared.pre == nil {
			return fail("scratch preparation failed: %v", prepared.parseErr)
		}
		pre = prepared.pre
		r.LastStats.ScratchPrepared = true
		r.LastStats.Scratch = prepared.elapsed
	} else {
		if scratch == nil || scratch == file {
			return fail("missing independent scratch parse")
		}
		pendingRetainedMu.Lock()
		scratchStart := time.Now()
		pre = scratchPreBody(scratch, r.options)
		r.LastStats.Scratch = time.Since(scratchStart)
		pendingRetainedMu.Unlock()
	}
	compareStart := time.Now()
	if reportDeclarationImport && pre != nil && pre.proofPrefixError != "" {
		return fail("proof report declaration capture: %s", pre.proofPrefixError)
	}
	// A payload-less enum variant is a const value of the declaration phase.
	constsIn, constsOut, globalsIn := 0, 0, 0
	for _, d := range removedConsts {
		if _, isConst := d.(*ast.ConstDecl); isConst {
			constsOut++
		}
	}
	for _, d := range addedConsts {
		if _, isConst := d.(*ast.ConstDecl); isConst {
			constsIn++
		} else {
			globalsIn++
		}
	}
	if len(enumPairs)+len(hierarchyPairs) != 0 {
		if !retainedEnumStoreSetsEqual(oldDecls, newDecls) {
			return fail("an enum edit changes which enums are recursive or store-backed")
		}
		storeBacked := retainedStoreBackedEnums(newDecls)
		for _, e := range append(append([][2]*ast.EnumDecl(nil), enumPairs...), hierarchyPairs...) {
			sb := storeBacked[e[1].Name]
			if d := retainedConstVariantCount(e[1], sb) - retainedConstVariantCount(e[0], sb); d > 0 {
				constsIn += d
			} else {
				constsOut -= d
			}
		}
	}
	for _, pair := range constEnumPairs {
		if delta := len(pair[1].Members) - len(pair[0].Members); delta > 0 {
			constsIn += delta
		} else {
			constsOut -= delta
		}
	}
	growth := retainedGrowth{addedFuncs: len(added), removedFuncs: len(removed), addedConsts: constsIn, removedConsts: constsOut, addedGlobals: globalsIn, usings: usingsAdded+usingsRemoved != 0}
	for _, st := range removedStructs {
		namespace := ""
		if block := r.oldMemberBlock(st); block != "" {
			namespace = block
		}
		growth.removedStructs = append(growth.removedStructs, joinQualifiedName(namespace, st.Name))
	}
	for _, st := range addedStructs {
		namespace := ""
		if block := memberOf[st]; block != nil && !topLevel[st] {
			namespace = namespaceOf[block]
		}
		growth.addedStructs = append(growth.addedStructs, joinQualifiedName(namespace, st.Name))
	}
	// Build the declaration pairing and using-dependent reanalysis plan before
	// comparing resolved type-name rows. Those planned owners are the only
	// unchanged bodies whose rows a using edit may legitimately change.
	keyOf := func(d ast.Decl) string { return fmt.Sprintf("%s|%T", retainedLineKey(d.Pos()), d) }
	newOfOld := make(map[string]int, len(pairs))
	for _, pair := range pairs {
		newOfOld[keyOf(oldDecls[pair[0]])] = pair[0]<<32 | pair[1]
	}
	lookup := func(d ast.Decl) (int, int, bool) {
		v, ok := newOfOld[keyOf(d)]
		return v >> 32, v & (1<<32 - 1), ok
	}
	newOf := func(d ast.Decl) ast.Decl {
		if _, j, ok := lookup(d); ok {
			return newDecls[j]
		}
		return nil
	}
	spanOf := func(d ast.Decl) (string, string) {
		if i, j, ok := lookup(d); ok {
			return oldSpans[i], newSpans[j]
		}
		return "", ""
	}
	var usingChanges []retainedUsingChange
	if usingsAdded+usingsRemoved != 0 {
		var extra []string
		for _, nf := range added {
			extra = append(extra, nf.Name)
		}
		for _, d := range addedConsts {
			extra = append(extra, retainedValueDeclName(d))
		}
		for _, st := range addedStructs {
			extra = append(extra, st.Name)
		}
		var why string
		if usingChanges, why = r.findUsingChanges(file, newOf, spanOf, extra); why != "" {
			return fail("%s", why)
		}
	}
	warningsChanged := false
	functionKeysBeforeReplay := retainedPreBodyFunctionKeys(r.decls)
	reanalyzedBodyKeys := make(map[string]bool, len(splices))
	for _, splice := range splices {
		if key := functionKeysBeforeReplay[splice.live]; key != "" {
			reanalyzedBodyKeys[key] = true
		}
	}
	for _, change := range usingChanges {
		fn, ok := r.decls[change.index].Decl.(*ast.FuncDecl)
		if !ok || !change.mention {
			continue
		}
		if key := functionKeysBeforeReplay[fn]; key != "" {
			reanalyzedBodyKeys[key] = true
		}
	}
	// The owner certificate is authoritative for body-only and function-set
	// updates. Existing struct/enum/const edit protocols have their own
	// declaration-fact import rules; retain the legacy table-size proof there.
	checkResolvedTypeNames := len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs)+
		len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs)+globalsIn == 0
	diff := preBodyDifference(r.pre, pre, marks, growth, replaced, reanalyzedBodyKeys, checkResolvedTypeNames, &warningsChanged)
	if retainedFastPrepareMode == "shadow" && len(shadowEdits) != 0 && len(added)+len(removed) == 0 {
		r.shadowPredict(a, shadowEdits, scoped, diff)
	}
	r.LastStats.Compare = time.Since(compareStart)
	r.LastStats.Pairing = pairing
	if diff != "" {
		return fail("%s", diff)
	}
	// A removed function leaves the live analysis; every unchanged function that
	// mentions its name is re-analyzed from its prepared body, as cold analysis
	// would now resolve that name differently.
	var removedLive []*ast.FuncDecl
	if len(removed) != 0 {
		gone := map[string]bool{}
		goneFunctions := map[*ast.FuncDecl]bool{}
		for _, of := range removed {
			cands := liveFns[fnKey{retainedLineKey(of.Pos()), of.Name}]
			if len(cands) != 1 {
				return fail("%d analyzed candidates for removed %s", len(cands), of.Name)
			}
			live := cands[0]
			sym := a.funcDeclSymbols[live]
			// An overload set is removed whole, its survivors re-declared with it.
			if sym == nil || a.globalScope.Symbols[sym.Name] != sym || (len(a.ufcsFunctionsByName[sym.Name]) > 1 && !overloadNames[oldFunctionNames[of]]) {
				return fail("removed %s is not a plain global function", of.Name)
			}
			if _, known := scoped[live]; !known {
				return fail("removed %s is not a top-level analyzed function", of.Name)
			}
			removedLive = append(removedLive, live)
			gone[live.Name] = true
			goneFunctions[live] = true
		}
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] || goneFunctions[fn] {
				continue
			}
			if !retainedMentionsAny(fn, gone) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil {
				return fail("%s mentions a removed function but has no edited counterpart", fn.Name)
			}
			splices = append(splices, splice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
		}
	}
	// A newly declared name can repair an unresolved call, or change overload
	// dispatch. Recheck every existing body that mentions it, including bodies
	// that were unchanged in text and currently only carry an undefined-name error.
	if len(added) != 0 {
		names := map[string]bool{}
		for _, fn := range added {
			names[fn.Name] = true
		}
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, fn := range removedLive {
			spliced[fn] = true
		}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] || !retainedMentionsAny(fn, names) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return fail("%s mentions an added function but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, splice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
		}
	}
	// An added or removed `using`: its scope's declarations take their new usings, and the
	// functions there that mention a member of the used module are re-analyzed.
	if usingsAdded+usingsRemoved != 0 {
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, fn := range removedLive {
			spliced[fn] = true // removed, or re-declared with a new signature
		}
		for _, c := range usingChanges {
			fn, ok := r.decls[c.index].Decl.(*ast.FuncDecl)
			if !ok || !c.mention || spliced[fn] {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil {
				return fail("%s mentions a newly used name but has no edited counterpart", fn.Name)
			}
			splices = append(splices, splice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
		}
	}
	// An edited struct: every function whose types or text reach it is re-analyzed from its
	// prepared body against the new field table.
	var structEdits []retainedStructEdit
	var structOnly map[*ast.FuncDecl]bool // struct users spliced only for the struct edit
	if len(structPairs) != 0 {
		var why string
		if structEdits, why = r.findLiveStructEdits(structPairs); why != "" {
			return fail("%s", why)
		}
		targets, names := map[Type]bool{}, map[string]bool{}
		for _, e := range structEdits {
			targets[e.st] = true
			names[e.live.Name] = true
			for _, field := range e.st.Fields {
				if group, ok := field.Type.(*BitGroupType); ok {
					targets[group] = true
				}
			}
		}
		additive := true
		for _, e := range structEdits {
			if !a.retainedAdditiveScalarEdit(e.live, e.edit) {
				additive = false
				break
			}
		}
		a.widenLayoutTargets(targets)
		for name, typ := range a.namedTypes {
			if targets[typ] {
				names[name[strings.LastIndex(name, ".")+1:]] = true
			}
		}
		namePattern := retainedNamesPattern(names)
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		memo := map[Type]bool{}
		var stores map[string]bool
		if additive {
			stores = a.retainedPackedStoresReaching(targets, memo)
		}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] {
				continue
			}
			oldText, newText := spanOf(fn)
			namedInSource := retainedNameTextMatch(oldText, names, namePattern)
			if !namedInSource && newText != oldText {
				namedInSource = retainedNameTextMatch(newText, names, namePattern)
			}
			if !namedInSource && !a.retainedUsesTypes(fn, targets, names, memo) {
				continue
			}
			if additive && newText == oldText && !a.retainedObservesFieldSet(fn, targets, stores, memo) {
				r.LastStats.StructUnobserved++
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil {
				return fail("%s uses an edited struct but has no edited counterpart", fn.Name)
			}
			splices = append(splices, splice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
			if structOnly == nil {
				structOnly = map[*ast.FuncDecl]bool{}
			}
			structOnly[fn] = true
		}
		r.LastStats.StructUsers = len(splices)
	}
	// An edited const value: recheck direct users and callers of dependent value contracts.
	// Other declaration/signature dependencies retain their declaration-phase guard.
	var constLive [][2]*ast.ConstDecl // (live, scratch) per edited const
	var addedConstPrepared []ast.Decl
	var removedConstLive []ast.Decl
	var addedStructPrepared []*ast.StructDecl
	var removedStructLive []scopedDecl
	if len(constPairs)+len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs) != 0 {
		names := map[string]bool{}
		edited := map[ast.Decl]bool{}
		for _, e := range constPairs {
			names[e[0].Name] = true
			edited[e[1]] = true
		}
		for _, d := range addedConsts {
			names[retainedValueDeclName(d)] = true
			edited[d] = true
		}
		for _, d := range removedConsts {
			names[retainedValueDeclName(d)] = true
		}
		for _, sd := range addedStructs {
			names[sd.Name] = true
			edited[sd] = true
		}
		for _, sd := range removedStructs {
			names[sd.Name] = true
		}
		pat := retainedNamesPattern(names)
		contractUsers := map[string]bool{}
		valueEditsOnly := len(constPairs) != 0 && len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs) == 0
		for j, d := range newDecls {
			if edited[d] {
				continue
			}
			if fn, ok := d.(*ast.FuncDecl); ok {
				if pat.MatchString(retainedNormalized(retainedHeader(fn))) {
					if !valueEditsOnly || !retainedConstValueContractOnly(fn, pat) {
						return fail("the signature of %s names an added or edited const or struct", fn.Name)
					}
					contractUsers[fn.Name] = true
				}
			} else if _, ns := d.(*ast.NamespaceDecl); !ns && pat.MatchString(newSpans[j]) {
				return fail("declaration at %s names an added or edited const or struct", retainedLineKey(d.Pos()))
			}
		}
		// A changed contract value can retract a caller's proof even though the
		// function's syntax did not change. Recheck the whole caller closure.
		// Reuse captured name facts only for byte-identical function spans;
		// edited/new functions are scanned from their current syntax.
		unchangedUses := map[*ast.FuncDecl]retainedBodyUses{}
		if len(names) != 0 {
			for live, current := range pairedNew {
				if uses, ok := r.bodyUses[live]; ok {
					oldSpan, newSpan := spanOf(live)
					if oldSpan != "" && oldSpan == newSpan {
						unchangedUses[current] = uses
					}
				}
			}
		}
		mentionsCurrent := func(fn *ast.FuncDecl, selected map[string]bool) bool {
			if uses, ok := unchangedUses[fn]; ok {
				for _, name := range uses.names {
					if selected[name] {
						return true
					}
				}
				return false
			}
			return retainedMentionsAny(fn, selected)
		}
		for grew := len(contractUsers) != 0; grew; {
			grew = false
			for _, d := range newDecls {
				fn, ok := d.(*ast.FuncDecl)
				if ok && !contractUsers[fn.Name] && mentionsCurrent(fn, contractUsers) {
					contractUsers[fn.Name] = true
					grew = true
				}
			}
		}
		liveConsts := map[string]*ast.ConstDecl{}
		liveValues := map[string]ast.Decl{} // consts and globals
		for _, sd := range r.decls {
			switch n := sd.Decl.(type) {
			case *ast.ConstDecl:
				liveConsts[retainedLineKey(n.Pos())+"|"+n.Name] = n
				liveValues[retainedLineKey(n.Pos())+"|"+n.Name] = n
			case *ast.GlobalDecl:
				liveValues[retainedLineKey(n.Pos())+"|"+n.Name] = n
			}
		}
		scratchConsts := map[string]*ast.ConstDecl{}
		scratchValues := map[string]ast.Decl{} // consts and globals
		scratchStructs := map[string]*ast.StructDecl{}
		for _, d := range retainedWalkDecls(scratch) {
			switch n := d.(type) {
			case *ast.GlobalDecl:
				scratchValues[retainedLineKey(n.Pos())+"|"+n.Name] = n
			case *ast.ConstDecl:
				scratchConsts[retainedLineKey(n.Pos())+"|"+n.Name] = n
				scratchValues[retainedLineKey(n.Pos())+"|"+n.Name] = n
			case *ast.StructDecl:
				scratchStructs[retainedLineKey(n.Pos())+"|"+n.Name] = n
			}
		}
		removedTargets := map[Type]bool{}
		if len(removedStructs) != 0 {
			liveStructs := map[string]scopedDecl{}
			for _, sd := range r.decls {
				if st, ok := sd.Decl.(*ast.StructDecl); ok {
					liveStructs[retainedLineKey(st.Pos())+"|"+st.Name] = sd
				}
			}
			for _, ost := range removedStructs {
				sd, ok := liveStructs[retainedLineKey(ost.Pos())+"|"+ost.Name]
				st, _ := a.namedTypes[joinQualifiedName(sd.Namespace, ost.Name)].(*StructType)
				if !ok || st == nil || st.Builtin || len(st.TypeParams) != 0 {
					return fail("removed struct %s not found in the analysis", ost.Name)
				}
				line := sd.Decl.Pos()
				for _, d := range a.diagnostics {
					if d.Pos.File == line.File && d.Pos.Line == line.Line {
						return fail("removed struct %s has diagnostics", ost.Name)
					}
				}
				removedTargets[st] = true
				removedStructLive = append(removedStructLive, sd)
			}
		}
		for _, sd := range addedStructs {
			prepared := scratchStructs[retainedLineKey(sd.Pos())+"|"+sd.Name]
			if prepared == nil {
				return fail("prepared form of added struct %s not found", sd.Name)
			}
			addedStructPrepared = append(addedStructPrepared, prepared)
		}
		for _, e := range constPairs {
			live := liveConsts[retainedLineKey(e[0].Pos())+"|"+e[0].Name]
			prepared := scratchConsts[retainedLineKey(e[1].Pos())+"|"+e[1].Name]
			if live == nil || prepared == nil {
				return fail("const %s not found in the analysis", e[0].Name)
			}
			line := live.Pos()
			for _, d := range a.diagnostics {
				if d.Pos.File == line.File && d.Pos.Line == line.Line {
					return fail("const %s has diagnostics", e[0].Name)
				}
			}
			constLive = append(constLive, [2]*ast.ConstDecl{live, prepared})
		}
		for _, cd := range removedConsts {
			name := retainedValueDeclName(cd)
			live := liveValues[retainedLineKey(cd.Pos())+"|"+name]
			if live == nil {
				return fail("removed %s not found in the analysis", name)
			}
			line := live.Pos()
			for _, d := range a.diagnostics {
				if d.Pos.File == line.File && d.Pos.Line == line.Line {
					return fail("removed %s has diagnostics", name)
				}
			}
			removedConstLive = append(removedConstLive, live)
		}
		for _, d := range addedConsts {
			prepared := scratchValues[retainedLineKey(d.Pos())+"|"+retainedValueDeclName(d)]
			if prepared == nil {
				return fail("prepared form of added %s not found", retainedValueDeclName(d))
			}
			addedConstPrepared = append(addedConstPrepared, prepared)
		}
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		// A struct is named by type expressions and literals, which the identifier scan
		// does not see, so the edited function's text is matched too.
		spanOf := map[ast.Decl]string{}
		if len(addedStructs)+len(removedStructs) != 0 {
			for j, d := range newDecls {
				spanOf[d] = newSpans[j]
			}
		}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] {
				continue
			}
			current := pairedNew[fn]
			if current == nil {
				current = fn
			}
			if !contractUsers[fn.Name] && !mentionsCurrent(current, names) && (len(addedStructs)+len(removedStructs) == 0 || !pat.MatchString(spanOf[pairedNew[fn]])) &&
				(len(removedTargets) == 0 || !a.retainedUsesTypes(fn, removedTargets, names, map[Type]bool{})) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return fail("%s uses an added or edited name but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, splice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
		}
		r.LastStats.ConstUsers = len(splices)
	}
	// Constant-enum values live in the type's member table. Keep that type's identity,
	// but reject declaration-phase value dependencies before mutating it.
	var constEnumEdits []retainedConstEnumEdit
	if len(constEnumPairs) != 0 {
		var why string
		if constEnumEdits, why = r.findLiveConstEnumEdits(constEnumPairs, scratch, newDecls, newSpans); why != "" {
			return fail("%s", why)
		}
		targets, names, members := map[Type]bool{}, map[string]bool{}, map[string]bool{}
		for _, e := range constEnumEdits {
			targets[e.et] = true
			names[e.live.Name] = true
			for _, d := range []*ast.ConstEnumDecl{e.live, e.edit} {
				for _, m := range d.Members {
					members[m.Name] = true
				}
			}
		}
		for name, t := range a.namedTypes {
			if targets[t] {
				names[name[strings.LastIndex(name, ".")+1:]] = true
			}
		}
		a.widenLayoutTargets(targets)
		// Layout queries name their operand without necessarily recording its type
		// as an expression type. Include containing records and their aliases.
		for name, typ := range a.namedTypes {
			if targets[typ] {
				names[name[strings.LastIndex(name, ".")+1:]] = true
			}
		}
		namePattern := retainedNamesPattern(names)
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		memo := map[Type]bool{}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] {
				continue
			}
			_, sourceText := spanOf(fn)
			if !retainedNameTextMatch(sourceText, names, namePattern) && !retainedConstEnumShorthand(pairedNew[fn], members) && !a.retainedUsesTypes(fn, targets, names, memo) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return fail("%s uses an edited constant enum but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, splice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
		}
		r.LastStats.EnumUsers = len(splices)
	}
	// An edited enum: every function whose types or text reach it is re-analyzed against the
	// new variant table.
	var enumEdits []retainedEnumEdit
	if len(enumPairs) != 0 {
		var why string
		if enumEdits, why = r.findLiveEnumEdits(enumPairs, scratch); why != "" {
			return fail("%s", why)
		}
		targets, names := map[Type]bool{}, map[string]bool{}
		for k, e := range enumEdits {
			targets[e.et] = true
			names[e.live.Name] = true
			for _, v := range enumPairs[k][0].Variants {
				names[v.Name] = true
			}
			for _, v := range enumPairs[k][1].Variants {
				names[v.Name] = true
			}
		}
		a.widenLayoutTargets(targets)
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		memo := map[Type]bool{}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] || !a.retainedUsesTypes(fn, targets, names, memo) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return fail("%s uses an edited enum but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, splice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
		}
		r.LastStats.EnumUsers = len(splices)
	}
	// An edited refinement of an enum hierarchy: the hierarchy is recomputed, and the functions
	// whose text can observe the edit are re-analyzed.
	var hierarchyEdits []retainedEnumEdit
	var hierarchyMembers []scopedDecl
	if len(hierarchyPairs) != 0 {
		var why string
		if hierarchyEdits, hierarchyMembers, why = r.findLiveHierarchyEdits(hierarchyPairs, scratch); why != "" {
			return fail("%s", why)
		}
		edited, members := map[string]bool{}, map[string]bool{}
		editedTypes := map[Type]bool{}
		for _, e := range hierarchyEdits {
			editedTypes[e.et] = true
		}
		for name, t := range a.namedTypes {
			// The type's own name and any alias of it, as a bare token.
			short := name[strings.LastIndex(name, ".")+1:]
			if editedTypes[t] {
				edited[short] = true
			}
			if et, ok := t.(*EnumType); ok && et != nil {
				for _, sd := range hierarchyMembers {
					if sd.Decl == et.Decl {
						members[short] = true
					}
				}
			}
		}
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, fn := range removedLive {
			spliced[fn] = true
		}
		users := 0
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] {
				continue
			}
			oldSpan, newSpan := spanOf(fn)
			if pairedNew[fn] == nil {
				oldSpan = unparse.FormatDecl(fn)
			}
			if !retainedHierarchyUsers(oldSpan, edited, members) && !retainedHierarchyUsers(newSpan, edited, members) {
				continue
			}
			nfn := pairedNew[fn]
			if nfn == nil || !retainedPlainFunction(fn) {
				return fail("%s uses an edited enum but cannot be re-analyzed alone", fn.Name)
			}
			splices = append(splices, splice{fn, nfn})
			newTexts[nfn] = retainedText(nfn)
			spliced[fn] = true
			users++
			if oldSpan == newSpan {
				// Unchanged text re-analyzed for its match coverage: a variant it never
				// names cannot change how its body forwards borrows.
				r.rbSameFlow[fn] = true
			}
		}
		r.LastStats.EnumUsers += users
	}
	// A re-analyzed unchanged function takes its prepared body, which already has its new lines:
	// only its header shifts.
	if len(shifted) != 0 {
		splicedNow := map[ast.Decl]bool{}
		for _, sp := range splices {
			splicedNow[sp.live] = true
		}
		kept := shifted[:0]
		for _, d := range shifted {
			if splicedNow[d] {
				movedSplices = append(movedSplices, d)
				continue
			}
			kept = append(kept, d)
		}
		shifted = kept
	}
	bodies := make([][]ast.Stmt, len(splices))
	if fastBodies != nil && len(fastBodies) != len(splices) {
		return fail("fast preparation does not cover %d re-analyzed functions", len(splices)-len(fastBodies))
	}
	for i, s := range splices {
		if fastBodies != nil {
			bodies[i] = fastBodies[i]
			continue
		}
		prepared := pre.prepared[retainedLineKey(s.edit.Pos())+"|"+s.edit.Name]
		if prepared == nil {
			return fail("prepared form of %s not found", s.edit.Name)
		}
		if retainedFastPrepareMode == "shadow" {
			r.shadowFastPrepare(a, s.live, s.edit, prepared, scoped[s.live])
		}
		bodies[i] = prepared.Body
	}
	// Indexed declaration defaults can have consumers outside body reanalysis.
	// Their exact recorded origin must not survive an edit unless its owner is
	// actually rebuilt by this invocation; unsupported cases decline pre-mutation.
	if len(structEdits) != 0 {
		planned, removed := map[*ast.FuncDecl]bool{}, map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			planned[sp.live] = true
		}
		for _, fn := range removedLive {
			planned[fn], removed[fn] = true, true
		}
		for _, edit := range structEdits {
			if !retainedIndexProofDefaultsChanged(edit.live, edit.edit) {
				continue
			}
			names := map[string]bool{edit.live.Name: true}
			targets := map[Type]bool{edit.st: true}
			a.widenLayoutTargets(targets)
			for name, typ := range a.namedTypes {
				if targets[typ] {
					names[name[strings.LastIndex(name, ".")+1:]] = true
				}
			}
			pattern := retainedNamesPattern(names)
			for _, sd := range r.decls {
				if sd.Decl != edit.live && retainedIndexProofNonBodyDefaultUses(sd.Decl, names, pattern) {
					return fail("edited struct default %s has a declaration/default consumer requiring full reanalysis", edit.live.Name)
				}
			}
			for _, site := range a.indexBoundsRuntimeSites {
				if site.Origin != edit.live {
					continue
				}
				owner, body := site.Owner.(*ast.FuncDecl)
				if !body || (!removed[owner] && (site.Header || !planned[owner])) {
					return fail("edited struct default %s has a runtime bounds site outside reanalyzed bodies", edit.live.Name)
				}
			}
			for expr, origin := range a.indexBoundsProofOrigins {
				if origin != edit.live || !a.indexBoundsProven[expr] {
					continue
				}
				owner, body := a.indexBoundsProofOwners[expr].(*ast.FuncDecl)
				if !body || (!removed[owner] && (a.indexBoundsProofHeaders[expr] || !planned[owner])) {
					return fail("edited struct default %s has an index proof outside reanalyzed bodies", edit.live.Name)
				}
			}
		}
	}
	// Preserve the old permission-equation numbering before changing the set.
	var permissionBefore []scopedDecl
	var permissionBeforeTypes []*FuncType
	var permissionKnownTypes map[*FuncType]bool
	if len(added) > 0 || len(removed) > 0 {
		permissionBefore = permissionFunctionDecls(r.decls)
		for _, sd := range permissionBefore {
			permissionBeforeTypes = append(permissionBeforeTypes, permissionDeclType(a, sd))
		}
		permissionKnownTypes = map[*FuncType]bool{}
		for _, ft := range a.functionTypes {
			if ft != nil {
				permissionKnownTypes[ft] = true
			}
		}
		for _, sym := range a.globalScope.Symbols {
			if sym != nil {
				if ft, ok := sym.Type.(*FuncType); ok && ft != nil {
					permissionKnownTypes[ft] = true
				}
			}
		}
	}
	var retirePlainGlobalFacts func() error
	var plainCoordinates *plainCoordinatePlan
	var plainRemoved []*retainedPlainDefaultProduct
	var plainBodies []*retainedPlainBodyRetirement
	plainRebound := map[*ast.FuncDecl]bool{}
	plainCommitted := false
	plainManaged := pre.plainAdmission
	if r.plainDefaults != nil && r.pre.plainInputs != "" && r.pre.plainInputs == pre.plainInputs {
		for _, fn := range removedLive {
			if _, tracked := r.plainDefaults.Candidates[fn]; tracked {
				plainManaged = true
			}
		}
		for _, splice := range splices {
			if _, tracked := r.plainDefaults.Candidates[splice.live]; tracked {
				plainManaged = true
			}
		}
	}
	if plainManaged {
		if r.plainDefaults == nil || r.plainDefaults.Poisoned || r.plainDefaults.Updating {
			return fail("plain default contribution baseline unavailable")
		}
		var err error
		plainRemoved, err = r.preflightPlainDefaultRemoval(removedLive)
		if err != nil {
			return fail("plain default transaction: %v", err)
		}
		for _, splice := range splices {
			retirement, err := r.preflightPlainBodyRetirement(splice.live)
			if err != nil {
				return fail("plain body transaction: %v", err)
			}
			plainBodies = append(plainBodies, retirement)
		}
		retirePlainGlobalFacts, err = r.preparePlainGlobalFactRetirement(pre, plainRemoved)
		if err != nil {
			return fail("plain global fact preflight: %v", err)
		}
		bodyChanged := map[*ast.FuncDecl]bool{}
		for _, splice := range splices {
			bodyChanged[splice.live] = true
		}
		plainCoordinates, err = r.planPlainSourceCoordinates(pairedNew, bodyChanged)
		if err != nil {
			return fail("plain coordinate preflight: %v", err)
		}
		state := r.plainDefaults
		state.Updating = true
		defer func() {
			state.Updating = false
			if !plainCommitted {
				state.Poisoned = true
				r.a = nil
				r.result = nil
			}
		}()
		for _, product := range plainRemoved {
			if err := cleanupRetainedPlainDefaultProduct(a, r.result, product); err != nil {
				return fail("plain default cleanup: %v", err)
			}
		}
	}
	// Removals go first: a changed signature re-adds the same name below.
	retireTypeNameBodies := make([]*ast.FuncDecl, 0, len(splices))
	for _, splice := range splices {
		retireTypeNameBodies = append(retireTypeNameBodies, splice.live)
	}
	plainRetiredTypeNameRows := map[ast.TypeExpr]bool{}
	for _, retirement := range plainBodies {
		for _, node := range retirement.Nodes {
			if expr, ok := node.(ast.TypeExpr); ok {
				plainRetiredTypeNameRows[expr] = true
			}
		}
	}
	for _, product := range plainRemoved {
		for _, node := range product.Nodes {
			if expr, ok := node.(ast.TypeExpr); ok {
				plainRetiredTypeNameRows[expr] = true
			}
		}
	}
	resolvedTypeNameRetirement, err := prepareResolvedTypeNameRetirement(a, r.resolvedTypeNameSourceOwners, functionKeysBeforeReplay, retireTypeNameBodies, removedLive, plainRetiredTypeNameRows, r.resolvedTypeNameSourceOwnersValid)
	if err != nil {
		return fail("resolved type-name body cleanup: %v", err)
	}
	if len(removedLive) != 0 {
		r.removeLiveFunctions(removedLive)
	}
	// Added functions are declared in the live analysis as the declaration phase
	// declared them in scratch; their results must then match scratch's exactly.
	var addedScoped []scopedDecl
	var addedInto []*ast.NamespaceDecl // live block that receives each added member; nil at top level
	// place scopes an added declaration (nd in file, prepared its scratch form) in the live
	// analysis: its block, namespace, usings and visibility.
	var usingsOf map[ast.Decl][]string
	var liveBlocks map[ast.Decl]ast.Decl // edited block -> its live counterpart
	place := func(nd, prepared ast.Decl, name string) (scopedDecl, *ast.NamespaceDecl, string) {
		if usingsOf == nil {
			// Usings are the block's inherited usings plus its own `using` lines. Two
			// blocks of one namespace can hold different lines, so they come from the
			// edited file's own block structure.
			usingsOf = a.retainedDeclUsings(file)
			_, liveBlocks, _ = a.retainedLiveUsings(a.file, file)
		}
		sd := scopedDecl{Decl: prepared}
		usings, ok := usingsOf[nd]
		if !ok {
			return sd, nil, fmt.Sprintf("usings of added %s not found", name)
		}
		sd.Usings = usings
		var into *ast.NamespaceDecl
		if block := memberOf[nd]; block != nil && !topLevel[nd] {
			sd.Namespace = namespaceOf[block]
			if into, _ = liveBlocks[block].(*ast.NamespaceDecl); into == nil {
				return sd, nil, fmt.Sprintf("block of added %s not found in the analysis", name)
			}
			switch scratch.DeclVisibility[prepared] {
			case "private":
				sd.Private = true
			case "":
				if file.DeclVisibility[nd] != "" {
					return sd, nil, fmt.Sprintf("visibility of added %s not found", name)
				}
			}
			if v := scratch.DeclVisibility[prepared]; v != "" {
				if a.declVisibility == nil {
					a.declVisibility = map[ast.Decl]string{}
				}
				a.declVisibility[prepared] = v
			}
		}
		return sd, into, ""
	}
	if len(added) != 0 {
		// Declared in source order, as the declaration phase registers them: the first of
		// an overloaded name owns its bare symbol.
		sort.SliceStable(added, func(x, y int) bool { return newDeclIndex[added[x]] < newDeclIndex[added[y]] })
		for _, nf := range added {
			prepared := pre.prepared[retainedLineKey(nf.Pos())+"|"+nf.Name]
			if prepared == nil {
				return fail("prepared form of added %s not found", nf.Name)
			}
			sd, into, why := place(nf, prepared, nf.Name)
			if why != "" {
				return fail("%s", why)
			}
			addedScoped = append(addedScoped, sd)
			addedInto = append(addedInto, into)
		}
		// Declaring mutates the live analysis: from here a mismatch poisons it.
		var verifyPlainGlobalReplay func() error
		if plainManaged {
			var err error
			verifyPlainGlobalReplay, err = r.reopenPlainHeaderGlobalProducers(pre, addedScoped)
			if err != nil {
				return fail("plain header global replay: %v", err)
			}
		}
		a.collectValueSymbols(addedScoped)

		if verifyPlainGlobalReplay != nil {
			if err := verifyPlainGlobalReplay(); err != nil {
				return fail("plain header global replay: %v", err)
			}
		}
		for _, sd := range addedScoped {
			fn := sd.Decl.(*ast.FuncDecl)
			if t, ok := pre.targets[fn]; ok {
				a.paramStoreTargets[fn] = t
			}
			if idx, ok := pre.regionIndex[fn]; ok {
				a.regionParamCalleeParamIndex[fn.Name] = idx
			}
		}
		// The classification resets its program-wide tables to the decls it is given; for an
		// added function that is only the added ones, so the rest of the program's entries are
		// kept and the added functions' merged in.
		callerArena, storeBuilders := a.regionPolyReturnsCallerArena, a.packedStoreBuilders
		a.classifyRegionPolymorphicFunctions(addedScoped)
		for ft, v := range callerArena {
			if _, set := a.regionPolyReturnsCallerArena[ft]; !set {
				if a.regionPolyReturnsCallerArena == nil {
					a.regionPolyReturnsCallerArena = map[*FuncType]bool{}
				}
				a.regionPolyReturnsCallerArena[ft] = v
			}
		}
		for ft, v := range storeBuilders {
			if _, set := a.packedStoreBuilders[ft]; !set {
				if a.packedStoreBuilders == nil {
					a.packedStoreBuilders = map[*FuncType]map[string]string{}
				}
				a.packedStoreBuilders[ft] = v
			}
		}
		for _, sd := range addedScoped {
			fn := sd.Decl.(*ast.FuncDecl)
			want := pre.fingerprint[pre.keyOf[fn]]
			if got := a.preBodyFunctionFingerprint(fn); got != want {
				r.a = nil
				return fail("added %s declared differently than a full analysis%s", fn.Name, firstFieldDifference(want, got))
			}
		}
		for i, sd := range addedScoped {
			if addedInto[i] != nil {
				addedInto[i].Decls = append(addedInto[i].Decls, sd.Decl)
				appendedInto[addedInto[i]] = true
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
				appendedTop = true
			}
		}
		r.decls = append(r.decls, addedScoped...)
		if plainManaged && a.fillMayAdoptSet != nil {
			// Exact canonical owner membership is established by the append above.
			// Preserve the materialized whole-program classification across new
			// closed no-call owners. Missing means conservative true to readers,
			// so an actual new false producer must be explicitly represented.
			for _, sd := range addedScoped {
				fn := sd.Decl.(*ast.FuncDecl)
				if err := plainDefaultProductShape(a, fn); err != nil {
					return fail("plain fill-adoption producer: %v", err)
				}
				a.fillMayAdoptSet[fn] = false
			}
		}
	}
	if plainManaged {
		for _, sd := range addedScoped {
			fn := sd.Decl.(*ast.FuncDecl)
			want, known := pre.plainHeaders[pre.keyOf[fn]]
			if !known {
				return fail("added %s lacks a certified scratch header product", fn.Name)
			}
			got, err := captureRetainedPlainDefaultProductPhase(a, fn, true)
			if err != nil {
				return fail("added %s header contribution: %v", fn.Name, err)
			}
			if got.Certificate != want.Certificate {
				return fail("added %s header facts differ from independent scratch analysis", fn.Name)
			}
		}
	}
	// Added consts are scoped like added functions; declaring them cannot disagree with
	// scratch, whose declaration-phase diagnostics already matched.
	var addedConstScoped, addedStructScoped []scopedDecl
	var addedConstInto, addedStructInto []*ast.NamespaceDecl
	for k, cd := range addedConsts {
		sd, into, why := place(cd, addedConstPrepared[k], retainedValueDeclName(cd))
		if why != "" {
			return fail("%s", why)
		}
		addedConstScoped = append(addedConstScoped, sd)
		addedConstInto = append(addedConstInto, into)
	}
	for k, st := range addedStructs {
		sd, into, why := place(st, addedStructPrepared[k], st.Name)
		if why != "" {
			return fail("%s", why)
		}
		if _, exists := a.namedTypes[joinQualifiedName(sd.Namespace, st.Name)]; exists {
			return fail("added struct %s shadows a declared type", st.Name)
		}
		addedStructScoped = append(addedStructScoped, sd)
		addedStructInto = append(addedStructInto, into)
	}
	if len(addedConstScoped) != 0 {
		for k, sd := range addedConstScoped {
			if addedConstInto[k] != nil {
				addedConstInto[k].Decls = append(addedConstInto[k].Decls, sd.Decl)
				appendedInto[addedConstInto[k]] = true
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
				appendedTop = true
			}
		}
		a.collectValueSymbols(addedConstScoped)
		r.decls = append(r.decls, addedConstScoped...)
	}
	// Added structs are declared as the declaration phase declares them.
	if len(addedStructScoped) != 0 {
		for k, sd := range addedStructScoped {
			if addedStructInto[k] != nil {
				addedStructInto[k].Decls = append(addedStructInto[k].Decls, sd.Decl)
				appendedInto[addedStructInto[k]] = true
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
				appendedTop = true
			}
		}
		a.collectNamedTypes(addedStructScoped)
		a.populateStructFields(addedStructScoped)
		a.clearRetainedTypeMemos()
		r.decls = append(r.decls, addedStructScoped...)
	}
	// Past this point the edit is accepted; mutate the retained state.
	r.bodyUses = nil
	r.applyUsingChanges(usingChanges, scoped)
	r.applyConstEnumEdits(constEnumEdits)
	if len(enumEdits) != 0 {
		r.applyEnumEdits(enumEdits)
	}
	if len(hierarchyEdits) != 0 && r.applyHierarchyEdits(hierarchyEdits, hierarchyMembers) {
		r.a = nil // half-applied
		return fail("an enum hierarchy edit moves the zeroed variant")
	}
	if len(structEdits) != 0 {
		if !r.applyStructEdits(structEdits) {
			structOnly = nil
		}
	}
	if len(removedConstLive) != 0 {
		r.removeLiveConsts(removedConstLive)
	}
	if len(removedStructLive) != 0 {
		r.removeLiveStructs(removedStructLive)
	}
	// Edited consts are evaluated first, then added functions: the edited bodies use both.
	var changed []scopedDecl
	if len(constLive) != 0 {
		changed = append(changed, r.applyConstEdits(constLive)...)
	}
	changed = append(changed, addedConstScoped...)
	changed = append(changed, addedScoped...)
	drop := map[int]bool{}
	for _, fn := range removedLive {
		if w, ok := r.windows[fn]; ok {
			for k := w[0]; k < w[1]; k++ {
				drop[k] = true
			}
		}
	}
	// Diagnostics sit at pre-edit positions; index the functions before any is shifted.
	owners := retainedDiagnosticOwners(r.decls, append(addedScoped, addedConstScoped...), newOf)
	for _, d := range movedSplices {
		retainedShiftDecl(d, marks)
	}
	spliced := make(map[*ast.FuncDecl]bool, len(splices))
	for _, s := range splices {
		spliced[s.live] = true
	}
	for _, retirement := range plainBodies {
		if err := r.cleanupPlainBodyRetirement(retirement); err != nil {
			return fail("plain body cleanup: %v", err)
		}
		plainRebound[retirement.Product.Owner] = true
	}
	if err := resolvedTypeNameRetirement.apply(a); err != nil {
		r.a = nil
		return fail("resolved type-name body cleanup: %v", err)
	}
	for _, splice := range splices {
		key := functionKeysBeforeReplay[splice.live]
		if pre.prepared != nil {
			prepared := pre.prepared[retainedLineKey(splice.edit.Pos())+"|"+splice.edit.Name]
			if prepared == nil {
				r.a = nil
				return fail("resolved type-name prepared function %s is missing", splice.edit.Name)
			}
			key = pre.keyOf[prepared]
		}
		if key == "" {
			r.a = nil
			return fail("resolved type-name body owner %s is missing", splice.edit.Name)
		}
		rows := pre.resolvedTypeNameRows[key]
		if pre.resolvedTypeNames.functions[key].body != "" && rows == nil {
			r.a = nil
			return fail("resolved type-name body rows for %s are missing", splice.edit.Name)
		}
		if err := importResolvedTypeNameBodyRows(a, rows); err != nil {
			r.a = nil
			return fail("resolved type-name body import: %v", err)
		}
	}
	for i, s := range splices {
		s.live.Body = bodies[i]
		changed = append(changed, scoped[s.live])
	}
	// A body-phase window holds what one function's analysis emitted, including what it
	// emitted for a callee it analyzed first (nested, or in its parallel worker). Each
	// diagnostic goes with the function it lies in: dropped when that one is re-analyzed,
	// kept when it is not, whichever window holds it.
	for fn, w := range r.windows {
		for k := w[0]; k < w[1] && k < len(a.diagnostics); k++ {
			owner := owners.owner(a.diagnostics[k].Pos)
			if owner == nil || owner == fn {
				owner = fn
			}
			if spliced[owner] {
				drop[k] = true
			}
		}
	}
	for _, d := range shifted {
		retainedShiftDecl(d, marks)
		if fn, ok := d.(*ast.FuncDecl); ok {
			a.refreshRetainedFactPositions(a.functionAnalyses[fn], marks)
		}
	}
	for _, d := range shiftedOther {
		retainedShiftDeclOwn(d, marks)
	}
	// Complete producer positions supersede line-only movement inside this
	// closed plain transaction. Fresh replacement bodies already carry positions.
	plainCoordinates.apply()
	if reportDeclarationImport {
		if why := a.importProofPrefix(pre); why != "" {
			// Syntax/types have already been updated. Never reuse a partial
			// revision after a failed source-identity rebind.
			r.a = nil
			return fail("proof report declaration import: %s", why)
		}
	}
	r.LastStats.Diff = time.Since(start)
	// Keep the body-phase diagnostics of unchanged functions; the changed ones
	// and everything the whole-program passes emitted are recomputed.
	// Changed declaration-phase warnings: the live prefix is replaced by the edited program's.
	declPrefix := 0
	if warningsChanged {
		declPrefix = len(r.pre.diags)
		for k := 0; k < declPrefix; k++ {
			drop[k] = true
		}
	}
	kept := make([]Diagnostic, 0, r.bodyEnd)
	if warningsChanged {
		kept = append(kept, pre.diags...)
	}
	// A kept diagnostic may spell a body-local gensym (`__quantifier_<offset>`) in its text;
	// the function it lies in moved by some bytes, and a fresh analysis names it anew.
	for k := 0; k < r.bodyEnd && k < len(a.diagnostics); k++ {
		if !drop[k] {
			d := a.diagnostics[k]
			if strings.Contains(d.Message, "__") {
				if owner, shift := owners.ownerAt(d.Pos); owner != nil {
					d.Message = retainedShiftLocalGensyms(d.Message, shift)
				}
			}
			d.Pos = retainedShiftPos(d.Pos, marks)
			kept = append(kept, d)
		}
	}
	for fn, diags := range r.frameDiags {
		if shift := owners.shiftOf(fn); shift != 0 {
			for i := range diags {
				diags[i].Message = retainedShiftLocalGensyms(diags[i].Message, shift)
			}
		}
	}
	a.diagnostics = kept
	a.reportedDiagnostics = make(map[diagnosticIdentity]bool, len(kept))
	for _, d := range kept {
		a.reportedDiagnostics[diagnosticIdentity{Pos: d.Pos, Severity: d.Severity, Message: d.Message}] = true
	}
	a.warnOnceSeen = nil
	// Windows index into the diagnostics list, which was just compacted; the
	// changed functions get fresh windows below, the rest are re-based.
	r.rebaseWindows(drop)
	if warningsChanged {
		for fn, w := range r.windows {
			r.windows[fn] = [2]int{w[0] + len(pre.diags), w[1] + len(pre.diags)}
		}
	}
	r.invalidateReturnBorrow(changed, marks)
	a.invalidateStorageViewCalleeWriteCaches()
	a.regionPointeesMemo = nil
	// Binder and scan indexes describe syntax; replacing a body invalidates both.
	for _, sd := range changed {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			delete(a.storeFlowBinderIndexes, fn)
			delete(a.storeScanIndexCache, fn)
		}
	}
	for _, fn := range removedLive {
		delete(a.storeFlowBinderIndexes, fn)
		delete(a.storeScanIndexCache, fn)
	}
	// Parameter-write reachability and its local facts depend on function bodies
	// and call resolution. A retained semantic edit can change any transitive
	// path, so invalidate both caches before re-analyzing edited bodies.
	if !bodyEnvironmentUnchanged || len(splices) != 0 || len(removedLive) != 0 {
		a.storeScanParamWriteCache = nil
		a.storeScanParamFactsCache = nil
	}
	if len(changed) != 0 || len(removedLive) != 0 {
		// A body edit can change flow through every transitive caller and can
		// split or join call cycles. These results predate the replacement bodies.
		// Every body whose call resolution an added or removed name changes mentions that
		// name and is re-analyzed, so it is among the changed functions; a removed function
		// seeds its callers.
		fns := make([]*ast.FuncDecl, 0, len(changed)+len(removedLive))
		for _, sd := range changed {
			if fn, ok := sd.Decl.(*ast.FuncDecl); ok && !structOnly[fn] {
				fns = append(fns, fn)
			}
		}
		fns = append(fns, removedLive...)
		a.invalidateStoreFlowSummariesFrom(fns)
		a.storeFlowSCC = nil
		a.cyclicFuncSet = nil
		if len(addedScoped) != 0 || len(removedLive) != 0 {
			// New/removed overloads also change name-based edge resolution in
			// otherwise unchanged bodies.
			a.callDeclsByName = nil
			a.callEdgeCache = nil
		} else {
			for _, sd := range changed {
				if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
					delete(a.callEdgeCache, fn)
				}
			}
		}
	}
	r.rbCutoff = len(added) == 0 && len(removed) == 0 && os.Getenv("ELISACORE_RETAINED_RB_CUTOFF") != "0"
	permissionUsable := len(added) == 0 && len(removed) == 0
	var permissionAppended map[*ast.FuncDecl]*FuncType
	var permissionReset []*ast.FuncDecl
	if len(added) > 0 && len(removed) == 0 {
		permissionAppended, permissionUsable = r.perms.appendedFunctions(a, permissionBefore, permissionBeforeTypes, permissionKnownTypes, permissionFunctionDecls(r.decls), changed)
	}
	if !permissionUsable {
		if plan, ok := r.perms.reindexedFunctions(a, permissionBefore, permissionBeforeTypes, permissionKnownTypes, permissionFunctionDecls(r.decls), changed); ok {
			permissionAppended, permissionReset, permissionUsable = plan.fresh, plan.reset, true
		}
	}
	r.perms.beginUpdate(changed, permissionUsable, marks)
	for _, fn := range permissionReset {
		r.perms.edited[fn] = true
	}
	a.returnBorrowLateFrames = append([]returnBorrowLateFrame{}, r.lateFrames...)
	a.returnBorrowLateEnabled = true
	bodiesStart := time.Now()
	a.retainedProofsScoped = true
	r.clearFuncNameUsageUpdateTracking()
	if bodyEnvironmentUnchanged {
		// Track every body actually analyzed, including recursive-summary and
		// return-borrow replay. Args/ResolvedArgs aliases stay inside one call
		// owner and are deduplicated within that owner's scan. Default argument
		// expressions are cloned before insertion. Refine expansion reuses the
		// declaration's Base type and may reuse unchanged predicate Ident leaves;
		// body analysis reads these shared nodes without mutating them. Rewrites
		// update fields on the analyzed declaration's body nodes and do not rename
		// shared identifier leaves in place.
		// An unrecognized function owner forces the final index update to rebuild.
		r.funcNameUsageTracking = true
		r.funcNameUsageOwnerLookup = retainedFuncNameUsageFunctionOwnerMap(r.decls)
		r.funcNameUsageTouchedOwners = map[ast.Decl]bool{}
	}
	returnSummariesBefore := r.snapshotReturnBorrowInvalidationSummaries(a)
	// A re-analyzed body is the same declaration node with a new body: the first analysis's
	// mark would skip the cyclic-summary convergence a fresh analysis runs before it, leaving
	// a recursive call to read the summary this update invalidated.
	for _, sd := range changed {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			delete(a.ambientFillAnalyzed, fn)
		}
	}
	replacedDisjointCallers := map[*ast.FuncDecl]bool{}
	for _, sd := range changed {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok && !replacedDisjointCallers[fn] {
			a.removeDisjointCallSitesForCaller(fn)
			replacedDisjointCallers[fn] = true
		}
	}
	returnBorrowSummaryConverged := true
	// Structural additions/removals/reindexing are complete. This membership
	// snapshot lives only through body replay; summary rounds still read live
	// FuncTypes and dynamic bodyEdited/summaryReanalyzed masks on every scan.
	permissionInventory := newRetainedPermissionInventory(r.decls)
	r.perms.withInitialRowsUsingInventory(a, r.decls, permissionInventory, func() {
		a.analyzeDecls(changed)
		returnBorrowSummaryConverged = r.reanalyzeReturnBorrowSummaryCallers(a, returnSummariesBefore, len(pre.diags), permissionInventory)
	})
	if !returnBorrowSummaryConverged {
		// The live analyzer has already had the edited bodies and some SCC members
		// reanalyzed. Do not let a later Update reuse this partial state.
		a.retainedProofsScoped = false
		a.closeSMT()
		r.a = nil
		return fail("retained return-borrow summary cycle did not converge; retry with a full analysis")
	}
	a.retainedProofsScoped = false
	r.LastStats.Bodies = time.Since(bodiesStart)
	r.rbUpdating = true
	postStart := time.Now()
	r.funcNameUsagePending = true
	r.funcNameUsageRebuild = !bodyEnvironmentUnchanged
	r.funcNameUsageChangedOwners = nil
	if bodyEnvironmentUnchanged {
		owners := map[ast.Decl]bool{}
		for owner := range r.funcNameUsageTouchedOwners {
			owners[owner] = true
		}
		if r.funcNameUsageUnknownTouched {
			r.funcNameUsageRebuild = true
		}
		for _, sd := range changed {
			if sd.Decl != nil {
				owners[sd.Decl] = true
			}
		}
		// Return-borrow summary convergence can reanalyze callers that were not
		// part of the source splice list. Their lowered call argument fields are
		// declaration-owned syntax too, so include the entire invalidation closure.
		var functionOwners map[*ast.FuncDecl]ast.Decl
		if len(r.rbInvalid) != 0 {
			functionOwners = r.funcNameUsageOwnerLookup
			if functionOwners == nil {
				functionOwners = retainedFuncNameUsageFunctionOwnerMap(r.decls)
			}
		}
		for fn := range r.rbInvalid {
			owner := functionOwners[fn]
			if owner == nil {
				r.funcNameUsageRebuild = true
				break
			}
			owners[owner] = true
		}
		for owner := range owners {
			r.funcNameUsageChangedOwners = append(r.funcNameUsageChangedOwners, owner)
		}
	}
	r.post(r.decls, permissionAppended)
	r.funcNameUsagePending = false
	r.funcNameUsageRebuild = false
	r.funcNameUsageChangedOwners = nil
	r.clearFuncNameUsageUpdateTracking()
	r.LastStats.Post = time.Since(postStart)
	r.rbUpdating = false
	if retirePlainGlobalFacts != nil {
		if err := retirePlainGlobalFacts(); err != nil {
			return fail("plain global fact retirement: %v", err)
		}
	}
	a.closeSMT()
	r.result.FuncDisjointParams = a.funcDisjointParams
	// file is the next comparison base. Its spliced bodies now belong to the
	// analyzed AST and get rewritten there, so keep their text from before.
	// Code generation emits declarations in list order; a fresh analysis lists them as written.
	if len(appendedInto) != 0 || appendedTop {
		currentOffsets := make(map[retainedDeclPosition]int, len(newDecls))
		for _, decl := range newDecls {
			pos := decl.Pos()
			currentOffsets[retainedDeclPosition{pos.File, pos.Line, pos.Col}] = pos.Offset
		}
		for ns := range appendedInto {
			retainedSortDeclsBySource(ns.Decls, currentOffsets)
		}
		if appendedTop {
			retainedSortDeclsBySource(a.file.Decls, currentOffsets)
		}
	}
	if retainedFastPrepareMode != "" && !bodyEnvironmentUnchanged {
		r.invalidateFastPrepareBaseline("declaration environment changed; fresh retained baseline required")
	}
	nextTypeNameOwners := make(map[ast.TypeExpr]resolvedTypeNameSourceOwner, len(r.resolvedTypeNameSourceOwners))
	for expr, owner := range r.resolvedTypeNameSourceOwners {
		nextTypeNameOwners[expr] = owner
	}
	typeNameOwnersValid := r.resolvedTypeNameSourceOwnersValid && pre != nil && pre.resolvedTypeNames.failure == ""
	if typeNameOwnersValid {
		removedFunctions := map[*ast.FuncDecl]bool{}
		for _, fn := range removedLive {
			removedFunctions[fn] = true
			oldKey := functionKeysBeforeReplay[fn]
			for expr, owner := range nextTypeNameOwners {
				if owner.owner == "fn:"+oldKey {
					delete(nextTypeNameOwners, expr)
				}
			}
		}
		newFunctionKeys := map[*ast.FuncDecl]string{}
		for live, oldKey := range functionKeysBeforeReplay {
			if removedFunctions[live] {
				continue
			}
			newKey := oldKey
			if newDecl := pairedNew[live]; newDecl != nil && pre.prepared != nil {
				if preparedFn := pre.prepared[retainedLineKey(newDecl.Pos())+"|"+newDecl.Name]; preparedFn != nil {
					if preparedKey := pre.keyOf[preparedFn]; preparedKey != "" {
						newKey = preparedKey
					}
				}
			}
			newFunctionKeys[live] = newKey
		}
		ownerKeyRewrites := make(map[string]string, len(newFunctionKeys))
		for live, oldKey := range functionKeysBeforeReplay {
			if newKey := newFunctionKeys[live]; newKey != "" && newKey != oldKey {
				ownerKeyRewrites["fn:"+oldKey] = "fn:" + newKey
			}
		}
		for expr, owner := range nextTypeNameOwners {
			if newOwner, ok := ownerKeyRewrites[owner.owner]; ok {
				owner.owner = newOwner
				nextTypeNameOwners[expr] = owner
			}
		}
		replaceBodyOwner := map[string]bool{}
		for _, splice := range splices {
			key := newFunctionKeys[splice.live]
			if key == "" {
				key = functionKeysBeforeReplay[splice.live]
			}
			replaceBodyOwner["fn:"+key] = true
		}
		for expr, owner := range nextTypeNameOwners {
			if replaceBodyOwner[owner.owner] && owner.region == "body" {
				delete(nextTypeNameOwners, expr)
			}
		}
		for _, splice := range splices {
			key := newFunctionKeys[splice.live]
			if key == "" {
				key = functionKeysBeforeReplay[splice.live]
			}
			preparedFn := (*ast.FuncDecl)(nil)
			if pre.prepared != nil {
				preparedFn = pre.prepared[retainedLineKey(splice.edit.Pos())+"|"+splice.edit.Name]
			}
			preparedKey := key
			if preparedFn != nil && pre.keyOf[preparedFn] != "" {
				preparedKey = pre.keyOf[preparedFn]
			}
			for expr, owner := range pre.resolvedTypeNameSourceOwners {
				if owner.owner == "fn:"+preparedKey && owner.region == "body" {
					owner.owner = "fn:" + key
					nextTypeNameOwners[expr] = owner
				}
			}
		}
		for _, scoped := range addedScoped {
			fn, ok := scoped.Decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			key := pre.keyOf[fn]
			if key == "" {
				continue
			}
			for expr, owner := range pre.resolvedTypeNameSourceOwners {
				if owner.owner == "fn:"+key {
					nextTypeNameOwners[expr] = owner
				}
			}
		}
		if len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs)+len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs)+globalsIn != 0 {
			typeNameOwnersValid = false
		}
	}
	r.resolvedTypeNameSourceOwners = nextTypeNameOwners
	r.resolvedTypeNameSourceOwnersValid = typeNameOwnersValid
	r.pristine, r.src, r.texts, r.pre = file, src, newTexts, pre.comparisonState()
	if relocateProofReport != nil {
		if why := relocateProofReport(a); why != "" {
			r.a = nil
			return fail("proof report source relocation: %s", why)
		}
	}
	a.publishLiveProofReport()
	if a.proofLifecycle.Unknown != "" {
		r.a = nil
		return fail("proof report authority: %s", a.proofLifecycle.Unknown)
	}
	r.result.ProofReport = append([]ProofFact(nil), a.proofReport...)
	r.result.ElisionSummary = a.currentElisionSummary()
	r.result.Diagnostics = publishedResultDiagnostics(a.diagnostics)
	r.result.SMTProfile = a.smtStats
	r.LastStats.Changed = len(changed)
	r.LastStats.Added = len(added)
	r.LastStats.Removed = len(removed)
	r.LastStats.Shifted = len(shifted)
	if plainManaged {
		r.prunePlainDefaultRetainedOwners(plainRemoved)
		identityProducts := append([]*retainedPlainDefaultProduct(nil), plainRemoved...)
		for _, retirement := range plainBodies {
			identityProducts = append(identityProducts, retirement.Product)
		}
		if err := r.prunePlainDeadIdentities(identityProducts); err != nil {
			return fail("plain identity commit: %v", err)
		}
		if err := r.verifyPlainDefaultRemoval(plainRemoved); err != nil {
			return fail("plain default commit: %v", err)
		}
	}
	for _, retirement := range plainBodies {
		if err := r.verifyPlainBodyRetirement(retirement); err != nil {
			return fail("plain retired body commit: %v", err)
		}
	}
	r.refreshPlainDefaultProductsAfterBodyCleanup(plainRebound)
	if plainManaged {
		plainCommitted = true
	}
	r.LastStats.Total = time.Since(start)
	return r.result, nil
}

// rebaseWindows maps the recorded diagnostic windows onto the compacted list.
func (r *RetainedAnalysis) rebaseWindows(drop map[int]bool) {
	if len(drop) == 0 {
		return
	}
	maxK := 0
	for _, w := range r.windows {
		if w[1] > maxK {
			maxK = w[1]
		}
	}
	// below[k] counts dropped indices smaller than k.
	below := make([]int, maxK+1)
	for k := 1; k <= maxK; k++ {
		below[k] = below[k-1]
		if drop[k-1] {
			below[k]++
		}
	}
	remap := func(k int) int { return k - below[k] }
	for fn, w := range r.windows {
		if drop[w[0]] {
			delete(r.windows, fn)
			continue
		}
		r.windows[fn] = [2]int{remap(w[0]), remap(w[1])}
	}
}

// funcComparisonText is what Update compares for a function whose source changed.
type funcComparisonText struct {
	header         string // signature, gensyms normalized
	gensyms        string // offset-named identifiers the function mentions
	bodyTypeSyntax string // pointer-free source TypeExpr fingerprint for a projected body
}

func retainedText(fn *ast.FuncDecl) funcComparisonText {
	return funcComparisonText{
		header:  retainedNormalized(retainedHeader(fn)),
		gensyms: retainedSharedGensyms(unparse.FormatDecl(fn)),
	}
}

func (r *RetainedAnalysis) comparisonText(fn *ast.FuncDecl) funcComparisonText {
	if text, ok := r.texts[fn]; ok {
		return text
	}
	return retainedText(fn)
}

// The parser names synthesized declarations and auto arenas after their byte
// offset (`__auto_10116619`, `__MachineMode_<hash>_10116619`), so an edit renames
// them in every later declaration of the expansion without changing it.
var retainedGensym = regexp.MustCompile(`(__[A-Za-z][A-Za-z0-9_]*?)_[0-9]+\b`)

// Auto arenas and runtime quantifier results are bindings local to the body or
// expression block that declares them.
var retainedLocalGensym = regexp.MustCompile(`^__(auto|quantifier)_[0-9]+$`)

var retainedLocalGensymRef = regexp.MustCompile(`\b__(auto|quantifier)_([0-9]+)\b`)

// retainedLocalGensymName recognizes the exact language of retainedLocalGensym
// without paying regexp-engine overhead in reflective source scans. Keep the
// regexp as an independent oracle in tests; this predicate only classifies a
// spelling and does not grant binder authority.
func retainedLocalGensymName(name string) bool {
	const (
		autoPrefix       = "__auto_"
		quantifierPrefix = "__quantifier_"
	)
	var suffix string
	switch {
	case strings.HasPrefix(name, autoPrefix):
		suffix = name[len(autoPrefix):]
	case strings.HasPrefix(name, quantifierPrefix):
		suffix = name[len(quantifierPrefix):]
	default:
		return false
	}
	if len(suffix) == 0 {
		return false
	}
	for i := 0; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return false
		}
	}
	return true
}

// retainedShiftLocalGensyms renames the body-local gensyms in text for a body that moved by
// shift bytes.
func retainedShiftLocalGensyms(text string, shift int) string {
	if shift == 0 || !strings.Contains(text, "__") {
		return text
	}
	return retainedLocalGensymRef.ReplaceAllStringFunc(text, func(name string) string {
		m := retainedLocalGensymRef.FindStringSubmatch(name)
		n, err := strconv.Atoi(m[2])
		if err != nil {
			return name
		}
		return "__" + m[1] + "_" + strconv.Itoa(n+shift)
	})
}

func retainedNormalized(text string) string { return retainedGensym.ReplaceAllString(text, "${1}_") }

// retainedSharedGensyms lists the offset-named identifiers a body mentions that
// may name declarations outside it. A spliced body keeps its new names while the
// retained declarations keep the old ones, so these must not change.
func retainedSharedGensyms(text string) string {
	var out []string
	for _, name := range retainedGensym.FindAllString(text, -1) {
		if !retainedLocalGensymName(name) {
			out = append(out, name)
		}
	}
	return strings.Join(out, " ")
}

// retainedHeader is fn without its statements. Its contracts are part of it (the
// formatter does not print them): callers' proofs read them, so a contract edit is a
// signature edit.
func retainedHeader(fn *ast.FuncDecl) string {
	c := *fn
	c.Body = nil
	var b strings.Builder
	b.WriteString(unparse.FormatDecl(&c))
	for _, part := range []struct {
		name string
		v    any
	}{
		{"requires", fn.Requires}, {"requires-proofs", fn.RequiresProofs},
		{"ensure", fn.EnsureValues}, {"ensure-proofs", fn.EnsureProofs},
		{"ensures", fn.Ensures}, {"decreases", fn.Decreases}, {"decreases-wild", fn.DecreasesWild},
		{"changes", fn.Changes}, {"preserves", fn.Preserves}, {"fulfills", fn.Fulfills},
		{"contract-includes", fn.ContractIncludes}, {"uses", fn.Uses}, {"param-contracts", fn.ParamContracts},
		{"forbids", fn.Forbids},
	} {
		b.WriteString("\n" + part.name + ": ")
		retainedWriteSyntax(&b, reflect.ValueOf(part.v))
	}
	return b.String()
}

// retainedWriteSyntax prints v without source positions: expressions and statements
// through the formatter, other values field by field.
func retainedWriteSyntax(b *strings.Builder, v reflect.Value) {
	if !v.IsValid() {
		b.WriteString("<nil>")
		return
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			b.WriteString("<nil>")
			return
		}
		switch n := v.Interface().(type) {
		case ast.Expr:
			b.WriteString(unparse.FormatExpr(n))
			return
		case ast.Stmt:
			b.WriteString(unparse.FormatStmt(n))
			return
		}
		retainedWriteSyntax(b, v.Elem())
		return
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			retainedWriteSyntax(b, v.Index(i))
			b.WriteString(";")
		}
		b.WriteString("]")
	case reflect.Struct:
		if v.Type() == retainedPosType {
			return
		}
		b.WriteString("{")
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			retainedWriteSyntax(b, v.Field(i))
			b.WriteString(",")
		}
		b.WriteString("}")
	case reflect.Map:
		b.WriteString(fmt.Sprint(v.Len()))
	default:
		fmt.Fprint(b, v.Interface())
	}
}

func retainedLineKey(p lexer.Pos) string { return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col) }

var retainedDeclType = reflect.TypeOf((*ast.Decl)(nil)).Elem()

// retainedWalkDecls lists every declaration in source order, descending into
// declaration containers (modules, impls, conditional blocks) but not bodies.
func retainedWalkDecls(file *ast.File) []ast.Decl {
	var out []ast.Decl
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			// An interface holding a declaration is recorded through the pointer it holds,
			// so each declaration is listed once.
			if v.Kind() == reflect.Pointer && v.Type().Implements(retainedDeclType) {
				d := v.Interface().(ast.Decl)
				out = append(out, d)
				if _, isFn := d.(*ast.FuncDecl); isFn {
					return
				}
			}
			visit(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				switch f := v.Field(i); f.Kind() {
				case reflect.Slice, reflect.Interface, reflect.Pointer:
					visit(f)
				}
			}
		case reflect.Slice:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return
			}
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	if file == nil {
		return nil
	}
	for i := range file.Decls {
		visit(reflect.ValueOf(&file.Decls[i]).Elem())
	}
	return out
}

var retainedPosType = reflect.TypeOf(lexer.Pos{})

func retainedShiftPos(p lexer.Pos, marks map[string][][2]int) lexer.Pos {
	ms := marks[p.File]
	if len(ms) == 0 || p.Line == 0 {
		return p
	}
	d := 0
	for _, m := range ms {
		if p.Line >= m[0] {
			d = m[1]
		}
	}
	if d != 0 {
		p.Line += d
		if p.EndLine != 0 {
			p.EndLine += d
		}
	}
	return p
}

// retainedShiftDecl moves every position inside an unchanged declaration by
// the line delta of the edit above it. Byte offsets are left as they were: they
// index the expanded buffer, are never printed, and stay consistent within a
// declaration.
func retainedShiftDecl(decl ast.Decl, marks map[string][][2]int) {
	retainedShiftDeclPositions(decl, marks, false)
}

// Nonfunction containers and their members are paired independently. Shift only
// this declaration's own positions so nested members are never shifted twice.
func retainedShiftDeclOwn(decl ast.Decl, marks map[string][][2]int) {
	retainedShiftDeclPositions(decl, marks, true)
}

func retainedShiftDeclPositions(decl ast.Decl, marks map[string][][2]int, ownOnly bool) {
	root := reflect.ValueOf(decl).Pointer()
	seen := map[uintptr]bool{}
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if ownOnly && !v.IsNil() && v.Pointer() != root && v.Type().Implements(retainedDeclType) {
				return
			}
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			visit(v.Elem())
		case reflect.Interface:
			if !v.IsNil() && v.Elem().Kind() == reflect.Pointer {
				visit(v.Elem())
			}
		case reflect.Struct:
			if v.Type() == retainedPosType {
				if v.CanSet() {
					v.Set(reflect.ValueOf(retainedShiftPos(v.Interface().(lexer.Pos), marks)))
				}
				return
			}
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(decl))
}

// retainedDeclSourceStart includes leading annotations in the declaration's
// comparison span. Pos() names the declaration keyword, which otherwise leaves
// a first declaration's annotations outside every span and assigns later ones
// to the preceding declaration.
func retainedDeclSourceStart(d ast.Decl) int {
	start := d.Pos().Offset
	var annotations []ast.Annotation
	switch d := d.(type) {
	case *ast.StructDecl:
		annotations = d.Annotations
	case *ast.EnumDecl:
		annotations = d.Annotations
	case *ast.FuncDecl:
		annotations = d.Annotations
	case *ast.StoreDecl:
		annotations = d.Annotations
	case *ast.ExternFuncDecl:
		annotations = d.Annotations
	case *ast.ExternVarDecl:
		annotations = d.Annotations
	case *ast.ExternTypeDecl:
		annotations = d.Annotations
	case *ast.ExportFuncDecl:
		annotations = d.Annotations
	case *ast.ImplDecl:
		annotations = d.Annotations
	}
	for _, annotation := range annotations {
		if offset := annotation.Position.Offset; offset >= 0 && offset < start {
			start = offset
		}
	}
	return start
}

// retainedSpans returns each declaration's source text: from its start to the
// next declaration's start in the expanded buffer (the last runs to the end).
// A container's span is therefore its own header, and comments and blank lines
// count as changes, so a declaration whose positions moved is re-analyzed.
func retainedSpans(decls []ast.Decl, src []byte) []string {
	// A span runs to the next declaration start in the source, not in walk
	// order: synthesized declarations (machine expansions) are walked out of
	// source order, and a walk-order span would cover unrelated edits.
	starts := make([]int, 0, len(decls))
	for _, d := range decls {
		starts = append(starts, retainedDeclSourceStart(d))
	}
	sort.Ints(starts)
	out := make([]string, len(decls))
	for i, d := range decls {
		start := retainedDeclSourceStart(d)
		end := len(src)
		if k := sort.SearchInts(starts, start+1); k < len(starts) {
			end = starts[k]
		}
		if start < 0 || start > end || end > len(src) {
			out[i] = fmt.Sprintf("\x00unordered %d", i) // never equal: forces a fallback or splice
			continue
		}
		span := src[start:end]
		// The span stays in its own file region: text past a directive belongs to the
		// region the directive opens.
		if k := bytes.Index(span, []byte("\n#line ")); k >= 0 {
			span = span[:k+1]
		}
		out[i] = retainedWithoutLineDirectives(span)
	}
	return out
}

// retainedWithoutLineDirectives drops include-splice `#line` directives from a
// span. They restate positions, which the update tracks per file from the
// declarations themselves; an edit above an include renumbers the directive
// that resumes the parent file without changing any declaration.
func retainedWithoutLineDirectives(span []byte) string {
	if !bytes.Contains(span, []byte("#line ")) {
		return string(span)
	}
	var b strings.Builder
	b.Grow(len(span))
	for len(span) > 0 {
		line := span
		if k := bytes.IndexByte(span, '\n'); k >= 0 {
			line, span = span[:k+1], span[k+1:]
		} else {
			span = nil
		}
		if bytes.HasPrefix(line, []byte("#line ")) {
			continue
		}
		b.Write(line)
	}
	return b.String()
}

// retainedSameDeclKind reports whether two declarations can be the same one
// across an edit: the same node type, and for functions the same name.
func retainedSameDeclKind(a, b ast.Decl) bool {
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return false
	}
	switch fa := a.(type) {
	case *ast.FuncDecl:
		return fa.Name == b.(*ast.FuncDecl).Name
	case *ast.ConstDecl:
		// By name, so an added const is not paired with the next one; the same for types.
		return fa.Name == b.(*ast.ConstDecl).Name
	case *ast.StructDecl:
		return fa.Name == b.(*ast.StructDecl).Name
	case *ast.ConstEnumDecl:
		return fa.Name == b.(*ast.ConstEnumDecl).Name
	case *ast.EnumDecl:
		return fa.Name == b.(*ast.EnumDecl).Name
	case *ast.GlobalDecl:
		return fa.Name == b.(*ast.GlobalDecl).Name
	case *ast.UsingDecl:
		fb := b.(*ast.UsingDecl)
		return fa.Name == fb.Name && fa.Member == fb.Member && fa.Alias == fb.Alias
	}
	return true
}

// retainedPlainFunction is a function an update may add or remove in place: not a
// law, generic, hook or overload, whose only global registration is its name.
func retainedPlainFunction(fn *ast.FuncDecl) bool {
	return !fn.IsLaw && !fn.IsContract && !fn.IsLemma && !fn.IsGhost && len(fn.TypeParams) == 0 &&
		len(fn.GenericParams) == 0 && !strings.HasPrefix(fn.Name, "__")
}

// retainedMentionsAny reports whether fn's body, contracts, or parameter defaults
// name any of names. Defaults are scanned separately because they participate in
// retained invalidation without contributing function-body recursion edges.
func retainedMentionsAny(fn *ast.FuncDecl, names map[string]bool) bool {
	hit := false
	check := func(stmts []ast.Stmt) {
		walkStoreScanNodes(stmts, func(node any) {
			if hit {
				return
			}
			switch n := node.(type) {
			case *ast.Ident:
				hit = names[n.Name] || names[fillMayAdoptLastSegment(n.Name)]
			case *ast.FieldExpr:
				hit = names[n.Field]
			case *ast.StructLitExpr:
				hit = names[n.Name] || names[fillMayAdoptLastSegment(n.Name)]
			}
		})
	}
	check(funcScanStmts(fn))
	check(funcDefaultArgStmts(fn))
	return hit
}

// removeLiveFunctions drops removed functions from the live analysis: their
// global registration, per-function tables, diagnostics and late checks.
func (r *RetainedAnalysis) removeLiveFunctions(fns []*ast.FuncDecl) {
	a := r.a
	for _, fn := range fns {
		a.clearFunctionIndexBoundsProofs(fn, true)
	}
	gone := map[*ast.FuncDecl]bool{}
	for _, fn := range fns {
		gone[fn] = true
	}
	survivingNames := map[string]bool{}
	for fn := range a.funcDeclSymbols {
		if !gone[fn] {
			survivingNames[fn.Name] = true
		}
	}
	for _, fn := range fns {
		a.removeDisjointFunction(fn)
		// Removal already queued live callees/dependents through the old
		// caller edges. A dead declaration cannot be a future solver target;
		// retain every live dirty row while retracting this exact owner key.
		delete(a.disjointDirtyCallees, fn)
		sym := a.funcDeclSymbols[fn]
		name := fn.Name
		if sym != nil {
			// A block member is registered under its qualified name.
			name = sym.Name
		}
		delete(a.globalScope.Symbols, name)
		if sym != nil {
			if ft, ok := sym.Type.(*FuncType); ok {
				for _, key := range []string{fn.Name, name} {
					if a.functionTypes[key] == ft {
						delete(a.functionTypes, key)
					}
				}
				delete(a.regionPolyReturnsCallerArena, ft)
			}
		}
		delete(a.funcDeclSymbols, fn)
		delete(a.funcDeclUsings, fn)
		delete(a.declVisibility, fn)
		if a.file != nil {
			delete(a.file.DeclVisibility, fn)
		}
		delete(a.paramStoreTargets, fn)
		if !survivingNames[fn.Name] {
			delete(a.regionParamCalleeParamIndex, fn.Name)
		}
		// The symbol leaves its overload list, which is keyed by the visible name and not
		// by a receiver-mangled symbol name.
		for visible, list := range a.ufcsFunctionsByName {
			kept := list[:0:0]
			for _, s := range list {
				if s != sym {
					kept = append(kept, s)
				}
			}
			if len(kept) == 0 {
				delete(a.ufcsFunctionsByName, visible)
			} else if len(kept) != len(list) {
				a.ufcsFunctionsByName[visible] = kept
			}
		}
		a.ufcsFunctionsVersion++
		a.ufcsByLastSegment = nil
	}
	keep := func(d ast.Decl) bool {
		fn, ok := d.(*ast.FuncDecl)
		return !ok || !gone[fn]
	}
	decls := r.decls[:0]
	for _, sd := range r.decls {
		if keep(sd.Decl) {
			decls = append(decls, sd)
		}
	}
	r.decls = decls
	var prune func(decls []ast.Decl) []ast.Decl
	prune = func(decls []ast.Decl) []ast.Decl {
		out := decls[:0]
		for _, d := range decls {
			if n, ok := d.(*ast.NamespaceDecl); ok {
				n.Decls = prune(n.Decls)
			}
			if keep(d) {
				out = append(out, d)
			}
		}
		return out
	}
	a.file.Decls = prune(a.file.Decls)
	frames := r.lateFrames[:0]
	for _, f := range r.lateFrames {
		if !gone[f.frame.fn] {
			frames = append(frames, f)
		}
	}
	r.lateFrames = frames
}

// retainedTrimTrailing drops the comment and blank lines that end a span.
func retainedTrimTrailing(span string) string {
	lines := strings.Split(span, "\n")
	end := len(lines)
	for end > 1 {
		t := strings.TrimSpace(lines[end-1])
		if t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		end--
	}
	return strings.Join(lines[:end], "\n")
}

type retainedDeclPosition struct {
	file      string
	line, col int
}

func retainedSortDeclsBySource(decls []ast.Decl, currentOffsets map[retainedDeclPosition]int) {
	// Retained declarations keep old physical offsets even when #line attribution
	// leaves their file/line unchanged after an include was inserted before them.
	// Compare in the current expanded source's coordinate system, never a mixture
	// of old retained and newly added physical offsets.
	offset := func(decl ast.Decl) int {
		pos := decl.Pos()
		if current, ok := currentOffsets[retainedDeclPosition{pos.File, pos.Line, pos.Col}]; ok {
			return current
		}
		return pos.Offset // synthesized declarations have no pristine counterpart
	}
	sort.SliceStable(decls, func(i, j int) bool { return offset(decls[i]) < offset(decls[j]) })
}

// Overload registration belongs to a namespace. Bare names remain deliberately
// conservative for caller invalidation, but a separate module's declaration
// must keep its existing type and specialization identities.
func retainedQualifiedFunctionNames(file *ast.File) map[*ast.FuncDecl]string {
	names := map[*ast.FuncDecl]string{}
	var walk func([]ast.Decl, string)
	walk = func(decls []ast.Decl, namespace string) {
		for _, decl := range decls {
			switch n := decl.(type) {
			case *ast.FuncDecl:
				names[n] = joinQualifiedName(namespace, n.Name)
			case *ast.NamespaceDecl:
				walk(n.Decls, joinQualifiedName(namespace, n.Name))
			}
		}
	}
	walk(file.Decls, "")
	return names
}

func retainedNoLineDelta(lineDelta map[string]int) bool {
	for _, d := range lineDelta {
		if d != 0 {
			return false
		}
	}
	return true
}

// Relocation moves syntax, not declaration-time symbol-table identities or source
// coordinates rendered into diagnostic text. Decline these authorities rather
// than pretending that a shifted AST has rerun their producers.
func (r *RetainedAnalysis) fastLineShiftAuthority(a *Analyzer, marks map[string][][2]int) string {
	for _, diagnostic := range r.pre.diags {
		if diagnostic.Severity == "" || diagnostic.Severity == DiagnosticSeverityError {
			return "line movement has declaration-prefix errors"
		}
		// The sentinel warning can render a callee declaration's source position.
		// Padding and builtin-shadow warnings have no such secondary location.
		if strings.Contains(diagnostic.Message, "(declared at ") {
			return "line movement has a source-location-bearing declaration diagnostic"
		}
	}
	for fn, symbol := range a.funcDeclSymbols {
		if fn == nil || symbol == nil || retainedShiftPos(fn.Pos(), marks).Line == fn.Pos().Line {
			continue
		}
		ft, ok := symbol.Type.(*FuncType)
		if !ok || ft == nil {
			continue
		}
		// Match the actual producer's symbol authority. Direct __init__ keeps
		// its ordinary name; only cast hooks and named-type constructor sugar
		// get these location-derived names. FuncType.Name itself is unchanged.
		if fn.Name == "__cast__" && symbol.Name == castHookSymbolName(ft.Name, ft, fn.Pos()) ||
			fn.Name != "__init__" && symbol.Name != ft.Name && symbol.Name == initHookSymbolName(ft.Name, ft, fn.Pos()) {
			return "line movement changes location-derived hook symbol " + symbol.Name
		}
	}
	return ""
}
