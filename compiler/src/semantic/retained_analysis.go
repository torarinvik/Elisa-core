package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
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
	oldFunctionNames := nameIndex.oldFunctionNames
	newDeclIndex := nameIndex.newDeclIndex
	oldTop, oldRemovable := retainedRemovableDeclarations(r.pristine)
	paired, err := retainedPairDeclarations(oldDecls, newDecls, nameIndex, topLevel, memberOf, oldRemovable)
	if err != nil {
		return fail("%s", err)
	}
	removed, added := paired.removed, paired.added
	addedConsts, removedConsts := paired.addedConsts, paired.removedConsts
	addedStructs, removedStructs := paired.addedStructs, paired.removedStructs
	insertedBefore, overloadNames := paired.insertedBefore, paired.overloadNames
	usingsAdded, usingsRemoved := paired.usingsAdded, paired.usingsRemoved
	liveOther, liveFns, scoped := retainedIndexLiveDeclarations(a.file, r.decls)
	comparison, err := r.compareRetainedDeclarations(oldDecls, newDecls, src, paired, nameIndex, topLevel, oldTop, memberOf, namespaceOf, liveOther, liveFns, scoped)
	if err != nil {
		return fail("%s", err)
	}
	splices, shadowEdits, movedSplices := comparison.splices, comparison.shadowEdits, comparison.movedSplices
	pairedNew, replaced := comparison.pairedNew, comparison.replaced
	shifted, shiftedOther := comparison.shifted, comparison.shiftedOther
	structPairs, constPairs, constEnumPairs := comparison.structPairs, comparison.constPairs, comparison.constEnumPairs
	enumPairs, hierarchyPairs := comparison.enumPairs, comparison.hierarchyPairs
	newTexts, lineDelta, marks := comparison.newTexts, comparison.lineDelta, comparison.marks
	newSpans := comparison.newSpans
	removed, added = comparison.removed, comparison.added
	// Track the containers that receive declarations for later source-order sorting.
	appendedInto := map[*ast.NamespaceDecl]bool{}
	appendedTop := false
	reportDeclarationImport, relocateProofReport, err := r.prepareRetainedProofUpdate(file, newDecls, paired, &comparison, liveFns, scoped)
	if err != nil {
		return fail("%s", err)
	}
	splices, shadowEdits = comparison.splices, comparison.shadowEdits
	// Bodies are analyzed in the form the declaration phase leaves them in, and
	// that phase's body-dependent results must be what the retained state holds.
	pairing := time.Since(start)
	var pre *preBodyState
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
	fast := r.prepareRetainedFastBodies(a, shadowEdits, scoped, oldDecls, marks, len(splices), bodyEnvironmentUnchanged, fastBodyEnvironmentUnchanged)
	fastBodies := fast.bodies
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
			pre, why = r.freshFunctionPreBody(a, added, file, src, namespaceInfo, r.retainedShiftedPreBodyDiagnostics(pairedNew, marks), retainedGrowth{addedFuncs: len(added)})
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
			pre, subset, why = r.scopedDeclarationPreBody(a, file, src, newDecls, newSpans, addedDecls, edits, r.retainedShiftedPreBodyDiagnostics(pairedNew, marks), retainedGrowth{addedConsts: len(addedConsts), addedStructs: structNames})
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
	pre, scratch, prepareErr := r.selectRetainedScratch(pre, scratch, file, prepared, fast, pairedNew, marks)
	if prepareErr != nil {
		return fail("%s", prepareErr)
	}
	compareStart := time.Now()
	if reportDeclarationImport && pre != nil && pre.proofPrefixError != "" {
		return fail("proof report declaration capture: %s", pre.proofPrefixError)
	}
	// A payload-less enum variant is a const value of the declaration phase.
	growth, globalsIn, growthErr := retainedGrowthForUpdate(r, paired, comparison, oldDecls, newDecls, topLevel, memberOf, namespaceOf)
	if growthErr != nil {
		return fail("%s", growthErr)
	}
	bindings, bindingErr := r.planRetainedBindings(file, oldDecls, newDecls, paired, comparison, added, splices)
	if bindingErr != nil {
		return fail("%s", bindingErr)
	}
	newOf, spanOf, usingChanges := bindings.newOf, bindings.spanOf, bindings.usingChanges
	functionKeysBeforeReplay, reanalyzedBodyKeys := bindings.functionKeys, bindings.reanalyzedBodyKeys
	warningsChanged := false
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
	bodyDeps, bodyDepsErr := retainedReanalyzeNameDependents(r, a, paired, comparison, liveFns, scoped, oldFunctionNames, splices, pairedNew, newTexts, usingChanges)
	if bodyDepsErr != nil {
		return fail("%s", bodyDepsErr)
	}
	removedLive, splices, newTexts := bodyDeps.removedLive, bodyDeps.splices, bodyDeps.newTexts
	structEdits, structOnly, splices, structErr := r.retainedStructUsers(a, structPairs, removedLive, splices, pairedNew, newTexts, spanOf)
	if structErr != nil {
		return fail("%s", structErr)
	}
	valuePlan, valueErr := r.retainedValueDependencies(a, paired, comparison, scratch, newDecls, splices, pairedNew, newTexts, spanOf)
	if valueErr != nil {
		return fail("%s", valueErr)
	}
	constLive, removedConstLive := valuePlan.constLive, valuePlan.removedConstLive
	removedStructLive, splices := valuePlan.removedStructLive, valuePlan.splices
	enumPlan, enumErr := r.retainedEnumDependencies(a, comparison, scratch, newDecls, removedLive, splices, pairedNew, newTexts, spanOf)
	if enumErr != nil {
		return fail("%s", enumErr)
	}
	constEnumEdits, enumEdits := enumPlan.constEnumEdits, enumPlan.enumEdits
	hierarchyEdits, hierarchyMembers, splices := enumPlan.hierarchyEdits, enumPlan.hierarchyMembers, enumPlan.splices
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
	permissionBefore, permissionBeforeTypes, permissionKnownTypes := r.retainedPermissionSnapshot(a, len(added) != 0 || len(removed) != 0)
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
	additions, additionsErr := r.retainedDeclareAdditions(a, file, scratch, pre, paired, added, newDeclIndex, topLevel, memberOf, namespaceOf, valuePlan, appendedInto, appendedTop, plainManaged)
	if additionsErr != nil {
		return fail("%s", additionsErr)
	}
	addedScoped, addedConstScoped, appendedTop := additions.functions, additions.values, additions.appendedTop
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
	changed, replayErr := r.retainedReplayBodiesAndDiagnostics(retainedBodyReplayInput{
		a: a, pre: pre, constLive: constLive, addedConstScoped: addedConstScoped, addedScoped: addedScoped,
		removedLive: removedLive, newOf: newOf, movedSplices: movedSplices, marks: marks, splices: splices,
		plainBodies: plainBodies, plainRebound: plainRebound, resolvedTypeNameRetirement: resolvedTypeNameRetirement,
		functionKeysBeforeReplay: functionKeysBeforeReplay, bodies: bodies, scoped: scoped, shifted: shifted,
		shiftedOther: shiftedOther, plainCoordinates: plainCoordinates, reportDeclarationImport: reportDeclarationImport,
		warningsChanged: warningsChanged, start: start,
	})
	if replayErr != nil {
		return fail("%s", replayErr)
	}
	r.invalidateRetainedBodyCaches(a, changed, removedLive, addedScoped, splices, structOnly, marks, bodyEnvironmentUnchanged)
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
	r.sortRetainedAddedDeclarations(a, newDecls, appendedInto, appendedTop)
	if retainedFastPrepareMode != "" && !bodyEnvironmentUnchanged {
		r.invalidateFastPrepareBaseline("declaration environment changed; fresh retained baseline required")
	}
	declarationsChanged := len(addedConsts)+len(removedConsts)+len(addedStructs)+len(removedStructs)+len(structPairs)+len(constPairs)+len(constEnumPairs)+len(enumPairs)+len(hierarchyPairs)+globalsIn != 0
	r.updateResolvedTypeNameOwners(pre, removedLive, functionKeysBeforeReplay, pairedNew, splices, addedScoped, declarationsChanged)
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
	if err := r.commitRetainedPlainDefaults(plainManaged, plainRemoved, plainBodies, plainRebound); err != nil {
		return fail("%s", err)
	}
	if plainManaged {
		plainCommitted = true
	}
	r.pruneFastPrepareDigests(newDecls)
	r.LastStats.Total = time.Since(start)
	return r.result, nil
}
