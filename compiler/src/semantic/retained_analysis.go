package semantic

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// RetainedAnalysis keeps a finished whole-program analysis alive so a later
// edit that only changes function bodies can be applied in place: the changed
// bodies are spliced into the analyzed AST and re-analyzed, then the
// whole-program passes run again. Everything else (types, symbols, unchanged
// bodies' facts and body-phase diagnostics) is reused.
//
// It only produces diagnostics. Callers that need code generation should run a
// fresh analysis once Update reports no errors, because spliced state was never
// validated against the backend.
type RetainedAnalysis struct {
	a        *Analyzer
	result   *Result
	decls    []scopedDecl
	post     func([]scopedDecl)
	pristine *ast.File                            // a parse of src, for comparison; see texts
	texts    map[*ast.FuncDecl]funcComparisonText // pristine functions whose bodies were spliced away
	src      []byte                               // the expanded source pristine was parsed from

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
	rbCallers   map[*ast.FuncDecl][]*ast.FuncDecl        // consulted -> consulting owners, pre-update
	rbStaleDeps map[*ast.FuncDecl]map[*ast.FuncDecl]bool // pre-update deps of rbInvalid
	rbCutoff    bool                                     // the late pass may narrow rbInvalid
	rbUpdating  bool
	perms       retainedPermissions

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
	ScratchPrepared           bool // the scratch phase ran ahead, overlapped with the driver's parse
	Total                     time.Duration
	Fallback                  string
	Added                     int
	Removed                   int
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
	return result, r
}

func (r *RetainedAnalysis) capture(a *Analyzer, decls []scopedDecl, post func([]scopedDecl)) {
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
	file    *ast.File
	pre     *preBodyState
	elapsed time.Duration
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
	start := time.Now()
	r.LastStats = RetainedUpdateStats{}
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
	oldNames, newNames := map[string]bool{}, map[string]bool{}
	for _, d := range oldDecls {
		if f, ok := d.(*ast.FuncDecl); ok {
			oldNames[f.Name] = true
		}
	}
	for _, d := range newDecls {
		if f, ok := d.(*ast.FuncDecl); ok {
			newNames[f.Name] = true
		}
	}
	oldTop := make(map[ast.Decl]bool, len(r.pristine.Decls))
	for _, d := range r.pristine.Decls {
		oldTop[d] = true
	}
	var removed []*ast.FuncDecl
	var pairs [][2]int
	var added []*ast.FuncDecl
	insertedBefore := map[int]bool{} // pair index preceded by an added declaration
	for i, j := 0, 0; i < len(oldDecls) || j < len(newDecls); {
		if i < len(oldDecls) && j < len(newDecls) && retainedSameDeclKind(oldDecls[i], newDecls[j]) {
			pairs = append(pairs, [2]int{i, j})
			i, j = i+1, j+1
			continue
		}
		// A removed top-level function: its name is gone from the edited source.
		if i < len(oldDecls) {
			if of, ok := oldDecls[i].(*ast.FuncDecl); ok && oldTop[of] && !newNames[of.Name] && retainedPlainFunction(of) {
				removed = append(removed, of)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
		}
		if j == len(newDecls) {
			return fail("declaration count changed (%d -> %d)", len(oldDecls), len(newDecls))
		}
		nf, ok := newDecls[j].(*ast.FuncDecl)
		if !ok || !(topLevel[nf] || memberOf[nf] != nil) || oldNames[nf.Name] || !retainedPlainFunction(nf) {
			return fail("declaration set changed at %s", retainedLineKey(newDecls[j].Pos()))
		}
		added = append(added, nf)
		insertedBefore[len(pairs)] = true
		j++
	}
	if len(pairs)+len(removed) != len(oldDecls) {
		return fail("declaration count changed (%d -> %d)", len(oldDecls), len(newDecls))
	}
	type fnKey struct {
		pos  string
		name string
	}
	// The analyzed AST also holds declarations the analyzer synthesized, so pair
	// its functions by source position and name rather than by index.
	liveFns := map[fnKey][]*ast.FuncDecl{}
	for _, d := range retainedWalkDecls(a.file) {
		if f, ok := d.(*ast.FuncDecl); ok {
			k := fnKey{retainedLineKey(f.Pos()), f.Name}
			liveFns[k] = append(liveFns[k], f)
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
	var movedSplices []ast.Decl                    // edited functions that also moved: header shifts, body is replaced
	pairedNew := map[*ast.FuncDecl]*ast.FuncDecl{} // live function -> its declaration in file
	replaced := map[string]bool{}                  // fingerprint keys of functions whose signature changed
	var shifted []ast.Decl
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
		// span compare below makes that declaration a splice.
		if d := np.Line - op.Line; d != lineDelta[op.File] {
			if !insertedBefore[k] && (k == 0 || oldSpans[i-1] == newSpans[pairs[k-1][1]] || oldDecls[i-1].Pos().File != op.File) {
				return fail("lines moved after an unchanged declaration at %s", retainedLineKey(op))
			}
			lineDelta[op.File] = d
			marks[op.File] = append(marks[op.File], [2]int{op.Line, d})
		}
		sameText := oldSpans[i] == newSpans[j]
		ofn, isFn := od.(*ast.FuncDecl)
		if !isFn {
			// A container's span is its own header up to its first member.
			if !sameText {
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
		if sameText {
			if lineDelta[op.File] != 0 {
				shifted = append(shifted, live)
			}
			continue
		}
		oldText, newText := r.comparisonText(ofn), retainedText(nfn)
		if oldText.header != newText.header {
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
			if !sameScope || !retainedPlainFunction(ofn) || !retainedPlainFunction(nfn) {
				return fail("signature of %s changed", ofn.Name)
			}
			removed = append(removed, ofn)
			added = append(added, nfn)
			replaced[namespace+"::"+ofn.Name+"#0"] = true
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
		newTexts[nfn] = newText
	}
	// Bodies are analyzed in the form the declaration phase leaves them in, and
	// that phase's body-dependent results must be what the retained state holds.
	pairing := time.Since(start)
	var pre *preBodyState
	if prepared != nil && prepared.file == scratch {
		pre = prepared.pre
		r.LastStats.ScratchPrepared = true
		r.LastStats.Scratch = prepared.elapsed
	} else {
		pendingRetainedMu.Lock()
		scratchStart := time.Now()
		pre = scratchPreBody(scratch, r.options)
		r.LastStats.Scratch = time.Since(scratchStart)
		pendingRetainedMu.Unlock()
	}
	compareStart := time.Now()
	diff := preBodyDifference(r.pre, pre, marks, len(added), len(removed), replaced)
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
		for _, of := range removed {
			cands := liveFns[fnKey{retainedLineKey(of.Pos()), of.Name}]
			if len(cands) != 1 {
				return fail("%d analyzed candidates for removed %s", len(cands), of.Name)
			}
			live := cands[0]
			sym := a.funcDeclSymbols[live]
			if sym == nil || a.globalScope.Symbols[sym.Name] != sym || len(a.ufcsFunctionsByName[sym.Name]) > 1 {
				return fail("removed %s is not a plain global function", of.Name)
			}
			if _, known := scoped[live]; !known {
				return fail("removed %s is not a top-level analyzed function", of.Name)
			}
			removedLive = append(removedLive, live)
			gone[live.Name] = true
		}
		spliced := map[*ast.FuncDecl]bool{}
		for _, sp := range splices {
			spliced[sp.live] = true
		}
		for _, sd := range r.decls {
			fn, ok := sd.Decl.(*ast.FuncDecl)
			if !ok || spliced[fn] || gone[fn.Name] {
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
	bodies := make([][]ast.Stmt, len(splices))
	for i, s := range splices {
		prepared := pre.prepared[retainedLineKey(s.edit.Pos())+"|"+s.edit.Name]
		if prepared == nil {
			return fail("prepared form of %s not found", s.edit.Name)
		}
		bodies[i] = prepared.Body
	}
	// Removals go first: a changed signature re-adds the same name below.
	if len(removedLive) != 0 {
		r.removeLiveFunctions(removedLive)
	}
	// Added functions are declared in the live analysis as the declaration phase
	// declared them in scratch; their results must then match scratch's exactly.
	var addedScoped []scopedDecl
	var addedInto []*ast.NamespaceDecl // live block that receives each added member; nil at top level
	if len(added) != 0 {
		// Usings come from an analyzed declaration in the same block: they are the
		// block's inherited usings plus its own `using` lines.
		usingsOf := map[string][]string{}
		liveBlocks := map[string]*ast.NamespaceDecl{}
		for _, sd := range r.decls {
			if _, seen := usingsOf[sd.Namespace]; !seen {
				usingsOf[sd.Namespace] = sd.Usings
			}
		}
		var collectLive func(decls []ast.Decl)
		collectLive = func(decls []ast.Decl) {
			for _, d := range decls {
				if n, ok := d.(*ast.NamespaceDecl); ok {
					liveBlocks[retainedLineKey(n.Position)+"|"+n.Name] = n
					collectLive(n.Decls)
				}
			}
		}
		collectLive(a.file.Decls)
		for _, nf := range added {
			prepared := pre.prepared[retainedLineKey(nf.Pos())+"|"+nf.Name]
			if prepared == nil {
				return fail("prepared form of added %s not found", nf.Name)
			}
			sd := scopedDecl{Decl: prepared}
			var into *ast.NamespaceDecl
			if block := memberOf[nf]; block != nil && !topLevel[nf] {
				sd.Namespace = namespaceOf[block]
				usings, ok := usingsOf[sd.Namespace]
				if !ok {
					return fail("added %s is in a block with no analyzed declarations", nf.Name)
				}
				sd.Usings = usings
				// The block itself is unchanged and lines only shift after an
				// insertion, so it is found again at its own position.
				if into = liveBlocks[retainedLineKey(block.Position)+"|"+block.Name]; into == nil {
					return fail("block of added %s not found in the analysis", nf.Name)
				}
				switch scratch.DeclVisibility[prepared] {
				case "private":
					sd.Private = true
				case "":
					if file.DeclVisibility[nf] != "" {
						return fail("visibility of added %s not found", nf.Name)
					}
				}
				if v := scratch.DeclVisibility[prepared]; v != "" {
					if a.declVisibility == nil {
						a.declVisibility = map[ast.Decl]string{}
					}
					a.declVisibility[prepared] = v
				}
			} else {
				sd.Usings = usingsOf[""]
			}
			addedScoped = append(addedScoped, sd)
			addedInto = append(addedInto, into)
		}
		// Declaring mutates the live analysis: from here a mismatch poisons it.
		a.collectValueSymbols(addedScoped)
		for _, sd := range addedScoped {
			fn := sd.Decl.(*ast.FuncDecl)
			if t, ok := pre.targets[fn]; ok {
				a.paramStoreTargets[fn] = t
			}
			if idx, ok := pre.regionIndex[fn]; ok {
				a.regionParamCalleeParamIndex[fn.Name] = idx
			}
		}
		a.classifyRegionPolymorphicFunctions(addedScoped)
		for _, sd := range addedScoped {
			fn := sd.Decl.(*ast.FuncDecl)
			want := pre.fingerprint[sd.Namespace+"::"+fn.Name+"#0"]
			if got := a.preBodyFunctionFingerprint(fn); got != want {
				r.a = nil
				return fail("added %s declared differently than a full analysis%s", fn.Name, firstFieldDifference(want, got))
			}
		}
		for i, sd := range addedScoped {
			if addedInto[i] != nil {
				addedInto[i].Decls = append(addedInto[i].Decls, sd.Decl)
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
			}
		}
		r.decls = append(r.decls, addedScoped...)
	}
	// Past this point the edit is accepted; mutate the retained state.
	// Added functions are analyzed first: the edited bodies call them.
	changed := append([]scopedDecl(nil), addedScoped...)
	drop := map[int]bool{}
	for _, fn := range removedLive {
		if w, ok := r.windows[fn]; ok {
			for k := w[0]; k < w[1]; k++ {
				drop[k] = true
			}
		}
	}
	for _, d := range movedSplices {
		retainedShiftDecl(d, marks)
	}
	for i, s := range splices {
		s.live.Body = bodies[i]
		changed = append(changed, scoped[s.live])
		if w, ok := r.windows[s.live]; ok {
			for k := w[0]; k < w[1]; k++ {
				drop[k] = true
			}
		}
	}
	for _, d := range shifted {
		retainedShiftDecl(d, marks)
	}
	r.LastStats.Diff = time.Since(start)
	// Keep the body-phase diagnostics of unchanged functions; the changed ones
	// and everything the whole-program passes emitted are recomputed.
	kept := make([]Diagnostic, 0, r.bodyEnd)
	for k := 0; k < r.bodyEnd && k < len(a.diagnostics); k++ {
		if !drop[k] {
			d := a.diagnostics[k]
			d.Pos = retainedShiftPos(d.Pos, marks)
			kept = append(kept, d)
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
	if len(changed) != 0 || len(removedLive) != 0 {
		// A body edit can change flow through every transitive caller and can
		// split or join call cycles. These results predate the replacement bodies.
		a.invalidateStoreFlowSummaries()
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
	r.perms.beginUpdate(changed, len(added) == 0 && len(removed) == 0, marks)
	a.returnBorrowLateFrames = append([]returnBorrowLateFrame{}, r.lateFrames...)
	a.returnBorrowLateEnabled = true
	bodiesStart := time.Now()
	a.analyzeDecls(changed)
	r.LastStats.Bodies = time.Since(bodiesStart)
	r.rbUpdating = true
	postStart := time.Now()
	r.post(r.decls)
	r.LastStats.Post = time.Since(postStart)
	r.rbUpdating = false
	a.closeSMT()
	// file is the next comparison base. Its spliced bodies now belong to the
	// analyzed AST and get rewritten there, so keep their text from before.
	r.pristine, r.src, r.texts, r.pre = file, src, newTexts, pre
	r.result.Diagnostics = a.diagnostics
	r.result.SMTProfile = a.smtStats
	r.LastStats.Changed = len(changed)
	r.LastStats.Added = len(added)
	r.LastStats.Removed = len(removed)
	r.LastStats.Shifted = len(shifted)
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
	header  string // signature, gensyms normalized
	gensyms string // offset-named identifiers the function mentions
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

func retainedNormalized(text string) string { return retainedGensym.ReplaceAllString(text, "${1}_") }

// retainedSharedGensyms lists the offset-named identifiers a body mentions that
// may name declarations outside it. A spliced body keeps its new names while the
// retained declarations keep the old ones, so these must not change.
func retainedSharedGensyms(text string) string {
	var out []string
	for _, name := range retainedGensym.FindAllString(text, -1) {
		if !retainedLocalGensym.MatchString(name) {
			out = append(out, name)
		}
	}
	return strings.Join(out, " ")
}

func retainedHeader(fn *ast.FuncDecl) string {
	c := *fn
	c.Body = nil
	return unparse.FormatDecl(&c)
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
			if v.Type().Implements(retainedDeclType) {
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
	seen := map[uintptr]bool{}
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
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
		starts = append(starts, d.Pos().Offset)
	}
	sort.Ints(starts)
	out := make([]string, len(decls))
	for i, d := range decls {
		start := d.Pos().Offset
		end := len(src)
		if k := sort.SearchInts(starts, start+1); k < len(starts) {
			end = starts[k]
		}
		if start < 0 || start > end || end > len(src) {
			out[i] = fmt.Sprintf("\x00unordered %d", i) // never equal: forces a fallback or splice
			continue
		}
		out[i] = retainedWithoutLineDirectives(src[start:end])
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
	fa, ok := a.(*ast.FuncDecl)
	if !ok {
		return true
	}
	return fa.Name == b.(*ast.FuncDecl).Name
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
	gone := map[*ast.FuncDecl]bool{}
	for _, fn := range fns {
		gone[fn] = true
		sym := a.funcDeclSymbols[fn]
		name := fn.Name
		if sym != nil {
			// A block member is registered under its qualified name.
			name = sym.Name
		}
		delete(a.globalScope.Symbols, name)
		delete(a.functionTypes, fn.Name)
		delete(a.functionTypes, name)
		delete(a.funcDeclSymbols, fn)
		delete(a.funcDeclUsings, fn)
		delete(a.paramStoreTargets, fn)
		delete(a.regionParamCalleeParamIndex, fn.Name)
		if list := a.ufcsFunctionsByName[name]; len(list) <= 1 {
			delete(a.ufcsFunctionsByName, name)
		}
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
