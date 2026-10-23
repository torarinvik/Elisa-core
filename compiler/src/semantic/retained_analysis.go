package semantic

import (
	"strconv"
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
	rbSameFlow  map[*ast.FuncDecl]bool                   // changed functions whose return-borrow summary this update keeps
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
	StructUsers               int // functions re-analyzed for an edited struct (with the edited ones)
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
	oldNames, newNames := map[string]bool{}, map[string]bool{}
	oldValueNames := map[string]bool{} // consts, globals and functions: names an added const must not take
	oldTypeNames := map[string]bool{}  // structs and enums: names an added struct must not take
	oldFuncNameCount := map[string]int{}
	for _, d := range oldDecls {
		if f, ok := d.(*ast.FuncDecl); ok {
			oldFuncNameCount[f.Name]++
		}
		switch n := d.(type) {
		case *ast.StructDecl:
			oldTypeNames[n.Name] = true
		case *ast.EnumDecl:
			oldTypeNames[n.Name] = true
		case *ast.FuncDecl:
			oldNames[n.Name] = true
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
			newFuncNameCount[f.Name]++
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
	var removed []*ast.FuncDecl
	var pairs [][2]int
	var added []*ast.FuncDecl
	var addedConsts []ast.Decl           // added typed consts and globals with fresh names
	var removedConsts []ast.Decl         // consts and globals whose name is gone from the edited program
	var addedStructs []*ast.StructDecl   // added plain structs with fresh names
	var removedStructs []*ast.StructDecl // plain structs whose name is gone from the edited program
	insertedBefore := map[int]bool{}     // pair index preceded by an added declaration
	overloadNames := map[string]bool{}   // names that gained an overload
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
		// A removed top-level function: its name is gone from the edited source.
		if i < len(oldDecls) {
			if of, ok := oldDecls[i].(*ast.FuncDecl); ok && oldTop[of] && !newNames[of.Name] && retainedPlainFunction(of) {
				removed = append(removed, of)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
			// A removed overload: the name survives in another same-named function, which is
			// re-declared with the rest of its overload set below.
			if of, ok := oldDecls[i].(*ast.FuncDecl); ok && newNames[of.Name] && retainedPlainFunction(of) &&
				oldFuncNameCount[of.Name] > newFuncNameCount[of.Name] &&
				(j == len(newDecls) || !retainedSameDeclKind(of, newDecls[j])) {
				overloadNames[of.Name] = true
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
		if ok && oldNames[nf.Name] && newFuncNameCount[nf.Name] > oldFuncNameCount[nf.Name] &&
			(topLevel[nf] || memberOf[nf] != nil) && retainedPlainFunction(nf) {
			// An overload of an existing name: registration order decides which one owns the
			// bare name, so every same-named function is re-declared with it below.
			overloadNames[nf.Name] = true
			added = append(added, nf)
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		if !ok || !(topLevel[nf] || memberOf[nf] != nil) || oldNames[nf.Name] || !retainedPlainFunction(nf) {
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
	var structPairs [][2]*ast.StructDecl  // edited plain structs: (previous, edited)
	var constPairs [][2]*ast.ConstDecl    // typed consts with a new value: (previous, edited)
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
			if !sameText && retainedTrimTrailing(oldSpans[i]) == retainedTrimTrailing(newSpans[j]) {
				continue
			}
			if !sameText {
				if osd, ok := od.(*ast.StructDecl); ok {
					if nsd, ok := nd.(*ast.StructDecl); ok && osd.Name == nsd.Name && retainedPlainStruct(osd) && retainedPlainStruct(nsd) {
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
		forceReplace := overloadNames[ofn.Name] || overloadNames[nfn.Name]
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
			if !sameScope || !retainedPlainFunction(ofn) || !retainedPlainFunction(nfn) {
				return fail("signature of %s changed", ofn.Name)
			}
			removed = append(removed, ofn)
			added = append(added, nfn)
			replaced[namespace+"::"+ofn.Name+"#0"] = true
			if forceReplace {
				// Every occurrence of an overloaded name is re-declared.
				for k := 1; k < newFuncNameCount[ofn.Name]; k++ {
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
	warningsChanged := false
	diff := preBodyDifference(r.pre, pre, marks, growth, replaced, &warningsChanged)
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
			// An overload set is removed whole, its survivors re-declared with it.
			if sym == nil || a.globalScope.Symbols[sym.Name] != sym || (len(a.ufcsFunctionsByName[sym.Name]) > 1 && !overloadNames[of.Name]) {
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
	// A live declaration's paired form in the edited program and both source texts. Live
	// declarations sit at their pre-edit positions, as the pristine ones do.
	var newOfOld map[string]int
	keyOf := func(d ast.Decl) string { return fmt.Sprintf("%s|%T", retainedLineKey(d.Pos()), d) }
	lookup := func(d ast.Decl) (int, int, bool) {
		if newOfOld == nil {
			newOfOld = make(map[string]int, len(pairs))
			for _, p := range pairs {
				newOfOld[keyOf(oldDecls[p[0]])] = p[0]<<32 | p[1]
			}
		}
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
	// An added or removed `using`: its scope's declarations take their new usings, and the
	// functions there that mention a member of the used module are re-analyzed.
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
	// An edited const value: every function that mentions it is re-analyzed. A declaration or
	// signature that names it was resolved in the declaration phase, which this path keeps.
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
		for j, d := range newDecls {
			if edited[d] {
				continue
			}
			if fn, ok := d.(*ast.FuncDecl); ok {
				if pat.MatchString(retainedText(fn).header) {
					return fail("the signature of %s names an added or edited const or struct", fn.Name)
				}
			} else if _, ns := d.(*ast.NamespaceDecl); !ns && pat.MatchString(newSpans[j]) {
				return fail("declaration at %s names an added or edited const or struct", retainedLineKey(d.Pos()))
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
			if !retainedMentionsAny(fn, names) && (len(addedStructs)+len(removedStructs) == 0 || !pat.MatchString(spanOf[pairedNew[fn]])) &&
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
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
			}
		}
		r.decls = append(r.decls, addedScoped...)
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
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
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
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
			}
		}
		a.collectNamedTypes(addedStructScoped)
		a.populateStructFields(addedStructScoped)
		a.clearRetainedTypeMemos()
		r.decls = append(r.decls, addedStructScoped...)
	}
	// Past this point the edit is accepted; mutate the retained state.
	r.applyUsingChanges(usingChanges, scoped)
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
	r.perms.beginUpdate(changed, len(added) == 0 && len(removed) == 0, marks)
	a.returnBorrowLateFrames = append([]returnBorrowLateFrame{}, r.lateFrames...)
	a.returnBorrowLateEnabled = true
	bodiesStart := time.Now()
	a.retainedProofsScoped = true
	// A re-analyzed body is the same declaration node with a new body: the first analysis's
	// mark would skip the cyclic-summary convergence a fresh analysis runs before it, leaving
	// a recursive call to read the summary this update invalidated.
	for _, sd := range changed {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			delete(a.ambientFillAnalyzed, fn)
		}
	}
	r.perms.withInitialRows(a, r.decls, func() { a.analyzeDecls(changed) })
	a.retainedProofsScoped = false
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

var retainedLocalGensymRef = regexp.MustCompile(`\b__(auto|quantifier)_([0-9]+)\b`)

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
