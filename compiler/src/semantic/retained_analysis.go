package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"
)

// UpdatePrepared is Update with an optional prepared scratch phase; it is used
// only when it was prepared from scratch itself.
func (r *RetainedAnalysis) UpdatePrepared(file, scratch *ast.File, src []byte, prepared *RetainedScratch) (*Result, error) {
	if err := r.prepareIndexProofOwnership(); err != nil {
		return nil, err
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
	a.resetSMTForRetainedUpdate()
	oldDecls, newDecls := retainedWalkDecls(r.pristine), retainedWalkDecls(file)
	// Index declaration scopes before pairing edits with the retained graph.
	topLevel, memberOf, namespaceOf := retainedDeclarationScopes(file)
	nameIndex := retainedIndexDeclarationNames(r.pristine, file, oldDecls, newDecls)
	oldFunctionNames, newFunctionNames := nameIndex.oldFunctionNames, nameIndex.newFunctionNames
	newFuncNameCount := nameIndex.newFuncNameCount
	newDeclIndex := nameIndex.newDeclIndex
	oldTop, oldRemovable := retainedRemovableDeclarations(r.pristine)
	paired, err := retainedPairDeclarations(oldDecls, newDecls, nameIndex, topLevel, memberOf, oldRemovable)
	if err != nil {
		return fail("%s", err)
	}
	removed, pairs, added := paired.removed, paired.pairs, paired.added
	addedConsts, removedConsts := paired.addedConsts, paired.removedConsts
	addedStructs, removedStructs := paired.addedStructs, paired.removedStructs
	insertedBefore, overloadNames := paired.insertedBefore, paired.overloadNames
	usingsAdded, usingsRemoved := paired.usingsAdded, paired.usingsRemoved
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
	pairedOld := map[*ast.FuncDecl]*ast.FuncDecl{}
	replaced := map[string]bool{} // fingerprint keys of functions whose signature changed
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
		pairedOld[live] = ofn
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
		proofBodies, why := r.proofReportBodyReanalysisClosure(changedOwners, removedOwners)
		if why != "" {
			return fail("%s", why)
		}
		if len(proofBodies) != 0 {
			// Traverse independently parsed declarations in source order, using
			// ONLY the already-established exact live/fresh pairing. No source
			// position, function spelling or fact-table key selects an owner.
			liveOf := map[*ast.FuncDecl]*ast.FuncDecl{}
			for live, fresh := range pairedNew {
				liveOf[fresh] = live
			}
			for _, decl := range newDecls {
				fresh, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				live := liveOf[fresh]
				if !proofBodies[live] {
					continue
				}
				old := pairedOld[live]
				if old == nil {
					return fail("proof report dependent body has no exact baseline parse")
				}
				if _, known := scoped[live]; !known {
					return fail("proof report dependent body is not independently analyzable")
				}
				splices = append(splices, splice{live, fresh})
				shadowEdits = append(shadowEdits, [3]*ast.FuncDecl{live, old, fresh})
				newTexts[fresh] = retainedText(fresh)
				changedOwners = append(changedOwners, live)
				delete(proofBodies, live)
			}
			if len(proofBodies) != 0 {
				return fail("proof report dependent body has no exact fresh source counterpart")
			}
		}
		reportDeclarationImport = declarationChange
		reportLocationsChanged := len(marks) != 0
		for live, fresh := range pairedNew {
			if fresh.Pos().Offset != live.Pos().Offset {
				reportLocationsChanged = true
			}
		}
		if reportLocationsChanged && len(r.a.proofReport) != 0 && r.DeferProofReportCoordinates {
			r.proofCoordinatesDeferred = true
		} else if reportLocationsChanged && len(r.a.proofReport) != 0 {
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
	var fastResolvedTypeNameContributions map[string]string
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
	// The function-name usage index holds identifier names only. Line movement
	// cannot change them, so a location-only update replaces just the owners it
	// touched instead of rescanning the whole program.
	funcNameUsageIncremental := usingsAdded+usingsRemoved == 0 && len(removed)+len(replaced) == 0 // owners added or replaced are reconciled below
	// Zero edits is a comment or blank-line change: the same certificate holds,
	// with only the line movement left to authorize.
	if retainedFastPrepareMode == "on" && len(shadowEdits) == len(splices) && fastBodyEnvironmentUnchanged {
		var why string
		if !bodyEnvironmentUnchanged {
			why = r.fastLineShiftAuthority(a, marks)
		}
		if why == "" {
			fastBodies, fastResolvedTypeNameRows, fastResolvedTypeNameContributions, why = r.fastPathBodies(a, shadowEdits, scoped, oldDecls)
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
			fastResolvedTypeNameContributions = nil
			fastResolvedTypeNameSourceOwners = nil
			r.LastStats.FastDeclined = why
		}
	}
	shiftedPreBodyDiagnostics := func() []Diagnostic {
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
		return diags
	}
	// A pure addition of fresh closed functions: see retained_fresh_function.go.
	if retainedFastPrepareMode == "on" && len(added) != 0 && len(splices)+len(shadowEdits)+len(removed)+len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs) == 0 &&
		len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs)+len(replaced)+len(overloadNames)+usingsAdded+usingsRemoved == 0 &&
		r.fastPrepareInvalidated == "" && (prepared == nil || prepared.lazy != nil) {
		why := r.fastLineShiftAuthority(a, marks)
		if why == "" {
			namespaceInfo := func(d ast.Decl) (string, []string, string) {
				if topLevel[d] {
					return "", nil, ""
				}
				block := memberOf[d]
				if block == nil || block.Private {
					return "", nil, "added function is not in a plain block"
				}
				return namespaceOf[block], nil, ""
			}
			pre, why = r.freshFunctionPreBody(a, added, file, src, namespaceInfo, shiftedPreBodyDiagnostics(), retainedGrowth{addedFuncs: len(added)})
		}
		if why != "" {
			pre = nil
			r.LastStats.FastDeclined = why
		} else {
			reportDeclarationImport = false
			r.LastStats.FastPrepared, r.LastStats.FastAdded = true, true
		}
	}
	// Fresh consts and structs, and const value edits: see scopedDeclarationPreBody.
	valueEditsOnly := len(constPairs) != 0 && len(addedConsts)+len(addedStructs) == 0
	freshDeclsOnly := len(constPairs) == 0 && len(addedConsts)+len(addedStructs) != 0
	allConstDecls := true
	for _, d := range addedConsts {
		if _, isConst := d.(*ast.ConstDecl); !isConst {
			allConstDecls = false
		}
	}
	if pre == nil && retainedFastPrepareMode == "on" && (valueEditsOnly || freshDeclsOnly) && allConstDecls && len(added)+len(splices)+len(shadowEdits)+len(removed)+len(removedConsts)+len(removedStructs) == 0 &&
		len(structPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs)+len(replaced)+len(overloadNames)+usingsAdded+usingsRemoved == 0 &&
		r.fastPrepareInvalidated == "" && (prepared == nil || prepared.lazy != nil) {
		why := r.fastLineShiftAuthority(a, marks)
		if why == "" {
			var addedDecls, edits []ast.Decl
			for _, d := range addedConsts {
				addedDecls = append(addedDecls, d)
			}
			var structNames []string
			for _, st := range addedStructs {
				addedDecls = append(addedDecls, st)
				namespace := ""
				if block := memberOf[st]; block != nil && !topLevel[st] {
					namespace = namespaceOf[block]
				}
				structNames = append(structNames, joinQualifiedName(namespace, st.Name))
			}
			for _, e := range constPairs {
				edits = append(edits, e[1])
			}
			var subset *ast.File
			pre, subset, why = r.scopedDeclarationPreBody(a, file, src, newDecls, newSpans, addedDecls, edits, shiftedPreBodyDiagnostics(), retainedGrowth{addedConsts: len(addedConsts), addedStructs: structNames})
			if why == "" {
				scratch = subset
				reportDeclarationImport = false
				r.LastStats.FastPrepared, r.LastStats.FastAdded = true, true
			}
		}
		if why != "" {
			pre = nil
			r.LastStats.FastDeclined = why
		}
	}
	if pre != nil {
		// Prepared above: nothing to wait for.
	} else if fastBodies != nil {
		// The edit leaves every declaration-level result as the retained baseline holds it.
		// The new prefix is independently relocated; mutating/aliasing the old
		// baseline would invalidate preBodyDifference and later disk revisions.
		resolvedTypeNames := r.pre.resolvedTypeNames
		if len(fastResolvedTypeNameContributions) != 0 {
			resolvedTypeNames = cloneResolvedTypeNameContributions(resolvedTypeNames)
			for key, body := range fastResolvedTypeNameContributions {
				contribution := resolvedTypeNames.functions[key]
				contribution.body = body
				resolvedTypeNames.functions[key] = contribution
			}
		}
		diags := shiftedPreBodyDiagnostics()
		pre = &preBodyState{classification: r.pre.classification, fingerprint: r.pre.fingerprint, global: r.pre.global, resolvedTypeNames: resolvedTypeNames, resolvedTypeNameSourceOwners: fastResolvedTypeNameSourceOwners, resolvedTypeNameRows: fastResolvedTypeNameRows, diags: diags, plainHeaders: r.pre.plainHeaders, plainGlobal: r.pre.plainGlobal, plainInputs: r.pre.plainInputs, plainIdentities: r.pre.plainIdentities, hasProofReport: r.pre.hasProofReport}
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
		for _, fn := range removedLive {
			// Removed, or re-declared with a new signature: splicing the new body under
			// the old header would check it against the old parameter list.
			spliced[fn] = true
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
			if !contractUsers[fn.Name] && !mentionsCurrent(current, names) && (len(addedStructs)+len(removedStructs) == 0 || !retainedNameTextMatch(spanOf[pairedNew[fn]], names, pat)) &&
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
			visibility := pre.addedVisibility[prepared]
			if scratch != nil && pre.addedPrepared == nil {
				visibility = scratch.DeclVisibility[prepared]
			}
			switch visibility {
			case "private":
				sd.Private = true
			case "":
				if file.DeclVisibility[nd] != "" {
					return sd, nil, fmt.Sprintf("visibility of added %s not found", name)
				}
			}
			if visibility != "" {
				if a.declVisibility == nil {
					a.declVisibility = map[ast.Decl]string{}
				}
				a.declVisibility[prepared] = visibility
			}
		}
		return sd, into, ""
	}
	if len(added) != 0 {
		// Declared in source order, as the declaration phase registers them: the first of
		// an overloaded name owns its bare symbol.
		sort.SliceStable(added, func(x, y int) bool { return newDeclIndex[added[x]] < newDeclIndex[added[y]] })
		for _, nf := range added {
			key := retainedLineKey(nf.Pos()) + "|" + nf.Name
			prepared := pre.prepared[key]
			if prepared == nil {
				prepared = pre.addedPrepared[key]
			}
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
		// Alias closures also depend on resolved callees and their types, even
		// when the caller's syntax index itself remains valid.
		a.invalidateStoreScanParamAliases()
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
	if funcNameUsageIncremental {
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
		r.reconcileFuncNameUsageOwners(r.decls)
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
	r.funcNameUsageRebuild = !funcNameUsageIncremental
	r.funcNameUsageChangedOwners = nil
	if funcNameUsageIncremental {
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
	if r.proofCoordinatesDeferred {
		r.result.ProofReport = nil
	}
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
	r.pruneFastPrepareDigests(newDecls)
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
