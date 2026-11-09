package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

func (a *Analyzer) analyzeStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		a.analyzeVariableDeclaration(n)
	case *ast.LetDestructureStmt:
		a.analyzeLetDestructureStmt(n)
	case *ast.TupleBindStmt:
		// docs/120 §8 arg-manifest candidacy: `t1, … <- call(…, t1, …)` where every target is
		// a mutable binding passed to the call. If so, analyze the RHS with NO expected tuple
		// (a void mutator would otherwise be corrupted into a type error by the expected
		// tuple), then erase when it is void — the call mutates the targets in place and the
		// `<-` is the manifest. A non-void RHS (a real `a, b <- swap(a, b)` tuple destructure)
		// falls through to the normal path using the already-analyzed type.
		manifestCandidate := false
		if !n.Declare {
			if call, ok := n.Value.(*ast.CallExpr); ok && a.namesAreMutableArgRoots(n.Names, call) {
				manifestCandidate = true
			}
		}

		var expectedTuple Type
		targetTypes := make([]Type, len(n.Names))
		if !n.Declare && !manifestCandidate {
			expectedFields := make([]TupleField, 0, len(n.Names))
			for i, binding := range n.Names {
				if binding.Name == "_" {
					expectedFields = append(expectedFields, TupleField{Name: binding.Name})
					continue
				}
				target := &ast.Ident{Position: binding.Position, Name: binding.Name}
				targetTypes[i] = a.assignmentTargetType(target)
				expectedFields = append(expectedFields, TupleField{Name: binding.Name, Type: targetTypes[i]})
			}
			expectedTuple = &TupleType{Fields: expectedFields}
		}
		savedReassign := a.reassignTargets
		a.reassignTargets = tupleBindTargetSet(n.Names)
		valueType := a.analyzeValueExpr(n.Value, expectedTuple)
		a.reassignTargets = savedReassign
		if manifestCandidate && isVoidType(valueType) {
			n.ArgManifest = true // codegen emits only the call; nothing to destructure
			return
		}
		if !n.Declare && manifestCandidate {
			// A non-void candidate is a real destructure; recover the target types the
			// normal path expects (they were skipped above to keep the RHS uncoerced).
			for i, binding := range n.Names {
				if binding.Name != "_" {
					targetTypes[i] = a.assignmentTargetType(&ast.Ident{Position: binding.Position, Name: binding.Name})
				}
			}
		}
		fields, ok := a.resolvedStructFields(valueType)
		if !ok {
			a.errorf(n.Pos(), "tuple destructuring requires a tuple value, got %s", valueType)
			return
		}
		if _, isTuple := StripAggregateStateType(valueType).(*TupleType); !isTuple {
			a.errorf(n.Pos(), "tuple destructuring requires a tuple value, got %s", valueType)
			return
		}
		if len(n.Names) != len(fields) {
			a.errorf(n.Pos(), "tuple destructuring expects %d bindings, got %d", len(fields), len(n.Names))
		}
		limit := len(n.Names)
		if len(fields) < limit {
			limit = len(fields)
		}
		for i := 0; i < limit; i++ {
			binding := n.Names[i]
			fieldExpr := &ast.FieldExpr{Position: binding.Position, Object: n.Value, Field: fields[i].Name}
			a.recordAnalyzedExprType(fieldExpr, fields[i].Type)
			if binding.Name == "_" {
				if n.Declare {
					a.consumeAffineValueExpr(fieldExpr, fields[i].Type, "discard tuple element")
				}
				continue
			}
			if n.Declare {
				a.checkVoidTupleBinding(binding.Position, binding.Name, fields[i].Type)
				sym := &Symbol{Name: binding.Name, Kind: SymbolLocal, Type: fields[i].Type, Node: n, Mutable: false}
				a.defineLocal(sym, binding.Position)
				a.recordValueBinding(sym, fieldExpr)
				a.recordFunctionValueBinding(sym, fieldExpr)
				a.recordImmutableSymbolOptimizationFacts(sym, fieldExpr)
				a.recordBorrowedOwnerRefBinding(sym, fieldExpr)
				a.recordRegionRefBinding(sym, fieldExpr)
				a.recordStorageViewBinding(sym, fieldExpr)
				continue
			}
			target := &ast.Ident{Position: binding.Position, Name: binding.Name}
			targetType := targetTypes[i]
			if targetType == nil {
				targetType = a.assignmentTargetType(target)
			}
			a.clearPackedVariantViewExpr(target)
			if !AssignableTo(targetType, fields[i].Type) {
				a.errorf(binding.Position, "cannot assign %s to %s", fields[i].Type, targetType)
				a.reportShapeMismatchNotes(binding.Position, targetType, fields[i].Type)
			}
			a.recordAssignmentRefinement(target, targetType, fields[i].Type)
			a.recordRegionRefAssignment(target, fieldExpr)
			a.recordStorageViewAssignment(target, fieldExpr)
			a.recordSpecializedValueTypeTarget(target, fields[i].Type)
			a.recordNamedStateAssignmentTarget(target, fieldExpr, fields[i].Type)
			a.clearAffineValueTarget(target)
			a.trackAffineValueTarget(target, targetType)
			a.markCreatedProtocolTarget(target, fieldExpr, targetType)
			a.recordBorrowedOwnerRefTarget(target, targetType, fieldExpr)
			a.recordFunctionValueTarget(target, fieldExpr)
			if AssignableTo(targetType, fields[i].Type) {
				a.bindActivePackedStoreType(targetType)
			}
			a.consumeAffineValueExpr(fieldExpr, targetType, "assignment")
		}
	case *ast.MoveBindStmt:
		a.analyzeMoveBindStmt(n)
	case *ast.DeferStmt:
		a.analyzeDeferStmt(n)
	case *ast.ScopeStmt:
		a.analyzeScopeStmt(n)
	case *ast.RegionStmt:
		if n.UserAuto {
			a.deprecatedf(n.Pos(), "`in auto:` is deprecated; remove the block and rely on region inference, or use `region NAME(size):` for an explicit scope")
		}
		if len(n.Body) != 0 || n.OwnerName != "" {
			a.analyzeScopedArenaStmt(n)
		} else {
			a.analyzeRegionDecl(n)
		}
	case *ast.MarkStmt:
		a.analyzeMarkStmt(n)
	case *ast.CheckpointStmt:
		a.analyzeCheckpointStmt(n)
	case *ast.GroupedCheckpointStmt:
		a.analyzeGroupedCheckpointStmt(n)
	case *ast.RestoreStmt:
		a.analyzeRestoreStmt(n)
	case *ast.RestoreCheckpointStmt:
		a.analyzeRestoreCheckpointStmt(n)
	case *ast.ResetStmt:
		a.analyzeResetStmt(n)
	case *ast.DestroyStmt:
		sym, state := a.lookupRegionState(n.Name)
		if sym == nil {
			a.errorf(n.Pos(), "undefined region %q", n.Name)
			return
		}
		if state.Destroyed {
			a.errorf(n.Pos(), "region %q has already been destroyed", n.Name)
			return
		}
		before := state.Generation
		state.Destroyed = true
		a.setCurrentRegionState(sym, state)
		a.invalidateRegionRefs(sym, func(regionDependencyState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
		a.invalidateRegionMarks(sym, func(regionMarkState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
		a.recordRegionInvalidateTransform(n.Pos(), n.Name, "", "destroy region", before, -1)
		// destroy discharges the region owner's must-consume obligation.
		a.recordAffineConsumption(affineValueKey{Root: sym}, "destroy")
	case *ast.LeakStmt:
		sym, state := a.lookupRegionState(n.Name)
		if sym == nil {
			a.errorf(n.Pos(), "undefined region %q", n.Name)
			return
		}
		if state.Destroyed {
			a.errorf(n.Pos(), "region %q has already been destroyed", n.Name)
			return
		}
		// leak discharges the must-consume obligation WITHOUT freeing the region
		// (its refs stay valid). It is an explicit, audited memory-safety opt-out
		// gated by Unsafe.Leak.
		a.recordFunctionPermissionRefs(unsafeLeakRefs(n.Position))
		a.recordAffineConsumption(affineValueKey{Root: sym}, "leak")
	case *ast.PromoteStmt:
		a.analyzePromoteStmt(n)
	case *ast.AdoptStmt:
		a.analyzeAdoptStmt(n)
	case *ast.AssignStmt, *ast.AugAssignStmt, *ast.AsRefAssignStmt:
		a.analyzeAssignmentStmt(stmt)
	case *ast.ReturnStmt:
		a.analyzeReturnStatement(n, nil)
	case *ast.BreakStmt:
		if a.loopDepth == 0 {
			a.errorf(n.Pos(), "break is only valid inside a loop")
		} else if a.breakLeavesValueBlock() {
			a.errorf(n.Pos(), "`break` may not jump out of a value block (docs/119 E5); a block ends only in its tail value — decide before the block, or make the loop a loop expression")
		}
		a.noteLoopJumpAffineState(true)
		a.reportLoopBodyLeaksOnJump()
	case *ast.ContinueStmt:
		if a.loopDepth == 0 {
			a.errorf(n.Pos(), "continue is only valid inside a loop")
		} else if a.breakLeavesValueBlock() {
			a.errorf(n.Pos(), "`continue` may not jump out of a value block (docs/119 E5); a block ends only in its tail value — decide before the block, or make the loop a loop expression")
		}
		a.noteLoopJumpAffineState(false)
		a.reportLoopBodyLeaksOnJump()
	case *ast.IfStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) && !IsInvalidType(condType) {
			a.errorf(n.Pos(), "if condition must be bool, got %s", condType)
		}
		// The post-if affine state is the meet of the branch outcomes, NOT the
		// entry state unioned with them. Seeding with entry would keep a value
		// "live" even when every arm consumes it (a false "must be consumed"),
		// because mergeAffineValueStates only ever adds liveness. The skip path
		// (condition false with no else) is already supplied by the empty-else
		// snapshot that is analyzed and merged below, so entry survives the join
		// only when it actually should.
		entryAffine := a.cloneAffineValueStates()
		entryRegions := a.cloneRegionStates()
		var mergedAffine map[affineValueKey]affineValueState
		regionBranches := make([]map[*Symbol]regionState, 0, len(n.Elifs)+2)
		constantCondition, conditionIsConstant := a.evalConstBoolExpr(n.Cond)
		mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
		mergedStorageViewDeps := a.cloneStorageViewDeps()
		var mergedAliasCarriers map[string][]string
		var mergedAliasCarrierFieldOverrides map[string]map[string][]string
		functionValueBranches := make([]map[*Symbol]*FuncType, 0, len(n.Elifs)+2)
		specializedValueTypeBranches := make([]map[*Symbol]Type, 0, len(n.Elifs)+2)
		entryRangeFacts := a.visibleRangeFacts()
		rangeBranches := make([]map[string]numRange, 0, len(n.Elifs)+2)
		var factBranches [][]smtFact
		var survivorBranches []smtFactFrame
		entryFacts := a.snapshotSMTFactFrame()
		thenSnapshot := a.analyzeBlockWithConditionAffineClone(n.Then, a.currentScope, n.Cond, true)
		thenSurvivors := a.restoreSMTFactFrame(entryFacts)
		if (!conditionIsConstant || constantCondition) && !blockDefinitelyExits(n.Then) {
			factBranches = append(factBranches, thenSnapshot.SMTFacts)
			survivorBranches = append(survivorBranches, thenSurvivors)
			mergedAffine = mergeAffineValueStates(mergedAffine, thenSnapshot.Affine)
			regionBranches = append(regionBranches, thenSnapshot.Regions)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, thenSnapshot.BorrowedOwnerRefs)
			mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, thenSnapshot.StorageViewDeps)
			mergedAliasCarrierFieldOverrides = mergeAliasCarrierFieldOverrides(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, thenSnapshot.AliasCarriers, thenSnapshot.AliasCarrierFieldOverrides)
			mergedAliasCarriers = mergeAliasCarriers(mergedAliasCarriers, thenSnapshot.AliasCarriers)
			functionValueBranches = append(functionValueBranches, thenSnapshot.FunctionValues)
			specializedValueTypeBranches = append(specializedValueTypeBranches, thenSnapshot.SpecializedValueTypes)
			rangeBranches = append(rangeBranches, thenSnapshot.RangeFacts)
		}
		for _, elif := range n.Elifs {
			elifType := a.analyzeExpr(elif.Cond)
			if !IsBoolType(elifType) && !IsInvalidType(elifType) {
				a.errorf(elif.Position, "elif condition must be bool, got %s", elifType)
			}
			elifSnapshot := a.analyzeBlockWithConditionAffineClone(elif.Body, a.currentScope, elif.Cond, true)
			elifSurvivors := a.restoreSMTFactFrame(entryFacts)
			if (!conditionIsConstant || !constantCondition) && !blockDefinitelyExits(elif.Body) {
				factBranches = append(factBranches, elifSnapshot.SMTFacts)
				survivorBranches = append(survivorBranches, elifSurvivors)
				mergedAffine = mergeAffineValueStates(mergedAffine, elifSnapshot.Affine)
				regionBranches = append(regionBranches, elifSnapshot.Regions)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elifSnapshot.BorrowedOwnerRefs)
				mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, elifSnapshot.StorageViewDeps)
				mergedAliasCarrierFieldOverrides = mergeAliasCarrierFieldOverrides(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, elifSnapshot.AliasCarriers, elifSnapshot.AliasCarrierFieldOverrides)
				mergedAliasCarriers = mergeAliasCarriers(mergedAliasCarriers, elifSnapshot.AliasCarriers)
				functionValueBranches = append(functionValueBranches, elifSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elifSnapshot.SpecializedValueTypes)
				rangeBranches = append(rangeBranches, elifSnapshot.RangeFacts)
			}
		}
		if len(n.Elifs) == 0 {
			elseSnapshot := a.analyzeBlockWithConditionAffineClone(n.Else, a.currentScope, n.Cond, false)
			elseSurvivors := a.restoreSMTFactFrame(entryFacts)
			if (!conditionIsConstant || !constantCondition) && !blockDefinitelyExits(n.Else) {
				factBranches = append(factBranches, elseSnapshot.SMTFacts)
				survivorBranches = append(survivorBranches, elseSurvivors)
				mergedAffine = mergeAffineValueStates(mergedAffine, elseSnapshot.Affine)
				regionBranches = append(regionBranches, elseSnapshot.Regions)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elseSnapshot.BorrowedOwnerRefs)
				mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, elseSnapshot.StorageViewDeps)
				mergedAliasCarrierFieldOverrides = mergeAliasCarrierFieldOverrides(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, elseSnapshot.AliasCarriers, elseSnapshot.AliasCarrierFieldOverrides)
				mergedAliasCarriers = mergeAliasCarriers(mergedAliasCarriers, elseSnapshot.AliasCarriers)
				functionValueBranches = append(functionValueBranches, elseSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elseSnapshot.SpecializedValueTypes)
				rangeBranches = append(rangeBranches, elseSnapshot.RangeFacts)
			}
		} else {
			elseSnapshot := a.analyzeBranchBlockWithAffineClone(n.Else, NewScope(a.currentScope))
			elseSurvivors := a.restoreSMTFactFrame(entryFacts)
			if (!conditionIsConstant || !constantCondition) && !blockDefinitelyExits(n.Else) {
				factBranches = append(factBranches, elseSnapshot.SMTFacts)
				survivorBranches = append(survivorBranches, elseSurvivors)
				mergedAffine = mergeAffineValueStates(mergedAffine, elseSnapshot.Affine)
				regionBranches = append(regionBranches, elseSnapshot.Regions)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elseSnapshot.BorrowedOwnerRefs)
				mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, elseSnapshot.StorageViewDeps)
				mergedAliasCarrierFieldOverrides = mergeAliasCarrierFieldOverrides(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, elseSnapshot.AliasCarriers, elseSnapshot.AliasCarrierFieldOverrides)
				mergedAliasCarriers = mergeAliasCarriers(mergedAliasCarriers, elseSnapshot.AliasCarriers)
				functionValueBranches = append(functionValueBranches, elseSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elseSnapshot.SpecializedValueTypes)
				rangeBranches = append(rangeBranches, elseSnapshot.RangeFacts)
			}
		}
		if len(n.Else) == 0 {
			if !conditionIsConstant || !constantCondition {
				regionBranches = append(regionBranches, entryRegions)
				functionValueBranches = append(functionValueBranches, a.currentFunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, a.currentSpecializedValueTypes)
				rangeBranches = append(rangeBranches, entryRangeFacts)
			}
		}
		if mergedAffine == nil {
			// Every taken branch diverged and an else covered the remaining
			// case: code after the if is unreachable. Preserve entry so the
			// flow state stays non-nil for downstream analysis.
			mergedAffine = entryAffine
		}
		joinedRegions, inconsistentRegions := joinRegionStateBranches(entryRegions, regionBranches)
		for _, sym := range inconsistentRegions {
			a.errorf(n.Pos(), "region %q is destroyed on some control-flow paths and remains live on others; consume it consistently before the paths join", sym.Name)
		}
		a.currentRegions = joinedRegions
		a.currentRegionsBorrowed = false
		a.currentAffineValues = mergedAffine
		a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
		a.currentStorageViewDeps = mergedStorageViewDeps
		a.currentAliasCarriers = mergedAliasCarriers
		a.currentAliasCarrierFieldOverrides = mergedAliasCarrierFieldOverrides
		if mergedFunctionValues, ok := a.intersectFunctionValueFlows(functionValueBranches...); ok {
			a.currentFunctionValues = mergedFunctionValues
		}
		if mergedSpecializedValueTypes, ok := a.intersectSpecializedValueTypeFlows(specializedValueTypeBranches...); ok {
			a.currentSpecializedValueTypes = mergedSpecializedValueTypes
		}
		a.mergePostIfRangeFacts(entryRangeFacts, rangeBranches)
		// With a constant condition only the branches that can run are survivors (the others were never
		// added above), so their end facts may be joined exactly as for a computed condition. This is
		// what carries facts out of the synthesized `if true:` scope around a captured loop header.
		a.joinIfSMTFacts(entryFacts, survivorBranches, factBranches, true)
		a.applyPostIfFallthroughRefinement(n)
		if len(n.Elifs) == 0 && len(n.Else) == 0 && blockDefinitelyExits(n.Then) {
			a.applyIndexBoundsFactsForCondition(n.Cond, false)
		}
	case *ast.MatchStmt:
		a.analyzeMatchStmt(n)
	case *ast.ExpectPatternStmt:
		a.analyzeExpectPatternStmt(n)
	case *ast.InStoreStmt:
		a.analyzeInStoreStmt(n)
	case *ast.CanStmt:
		a.analyzeCanStmt(n)
	case *ast.PoolStmt:
		a.analyzePoolStmt(n)
	case *ast.LockStmt:
		a.analyzeLockStmt(n)
	case *ast.WhileStmt:
		// Loop-capture manifest (docs/120 §9/§10): `while cond |parser|:` licenses
		// mutating calls on the captured bindings across the whole loop (cond + body).
		// analyzeStmt handles one statement per call, so the defer pops at case end.
		if len(n.Captures) > 0 {
			captured := make(map[string]bool, len(n.Captures))
			for _, c := range n.Captures {
				captured[c] = true
			}
			a.loopCaptureAllowed = append(a.loopCaptureAllowed, captured)
			defer func() { a.loopCaptureAllowed = a.loopCaptureAllowed[:len(a.loopCaptureAllowed)-1] }()
		}
		bodyPermissionRefStart := len(a.currentFunctionUsedPermissionRefs)
		progressObligationIndex := a.recordProgressLoopObligation(n)
		// The condition runs again before every iteration: its view uses belong to the back-edge check.
		a.pushStorageViewLoopUseFrame(a.currentScope, n.Body)
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) && !IsInvalidType(condType) {
			a.errorf(n.Pos(), "while condition must be bool, got %s", condType)
		}
		mergedAffine := a.cloneAffineValueStates()
		mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
		mergedFunctionValues := a.cloneFunctionValueBindings()
		mergedSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
		derivedEntry := a.captureDerivedLoopEntry(a.currentScope)
		mergedStorageViewDeps := a.cloneStorageViewDeps()
		// Verify leading `invariant` clauses inductively (establishment + preservation) BEFORE the body
		// is analyzed — the body's own mutations (`i <- i + 1`) invalidate the entry facts upward, so
		// establishment must read the pristine pre-loop scope. Exit facts are seeded after the body.
		provenInvariants, loopExitFactsSound := a.proveLoopInvariants(n)
		// Verify a leading `decreases` measure proves the loop terminates (strict decrease + bounded
		// below on every iteration), on the same pristine pre-loop scope.
		a.checkLoopTermination(n, provenInvariants)
		// The count-up exit fact `i <= bound` is sound only if `i <= bound` at ENTRY; evaluate on the
		// pristine pre-loop scope, since the body's `i <- i + 1` mutates i's tracked value below.
		countUpExitSound := a.countUpExitFactSound(n)
		a.havocLoopWrittenFacts(n.Body, n.Cond)
		entryAffine := a.cloneAffineValueStates()
		outerScope := a.currentScope
		a.pushLoopAffineFrame()
		a.loopDepth++
		// Push the proven outer-loop invariants onto the stack so that nested inner-loop invariant
		// proofs can use them as additional hypotheses (for variables the inner loop doesn't mutate).
		outerBase := len(a.activeOuterLoopInvariants)
		a.activeOuterLoopInvariants = append(a.activeOuterLoopInvariants, provenInvariants...)
		bodySnapshot := a.analyzeLoopBodyKeepingWrites(func() affineFlowSnapshot {
			return a.analyzeBlockWithConditionAffineClone(n.Body, a.currentScope, n.Cond, true)
		})
		a.activeOuterLoopInvariants = a.activeOuterLoopInvariants[:outerBase]
		a.loopDepth--
		a.checkStorageViewLoopBackEdge(bodySnapshot.StorageViewDeps, blockDefinitelyExits(n.Body))
		if cond, literal := n.Cond.(*ast.BoolLit); !literal || cond.Value {
			mergedFunctionValues = a.mergeLoopJumpFunctionValues(mergedFunctionValues)
			mergedSpecializedValueTypes = a.mergeLoopJumpSpecializedTypes(mergedSpecializedValueTypes)
		}
		continuedAffine := a.finishLoopAffineFrame(entryAffine, bodySnapshot.Affine, blockDefinitelyExits(n.Body), outerScope, n.Pos())
		a.finishProgressLoopObligation(progressObligationIndex, a.currentFunctionUsedPermissionRefs[bodyPermissionRefStart:])
		if !blockDefinitelyExits(n.Body) {
			mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
			// A literal-false body is checked, but cannot change the exit typestate.
			if cond, literal := n.Cond.(*ast.BoolLit); !literal || cond.Value {
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
			}
			mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, bodySnapshot.StorageViewDeps)
		}
		mergedAffine = mergeAffineValueStates(mergedAffine, continuedAffine)
		a.currentAffineValues = mergedAffine
		a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
		a.currentFunctionValues = mergedFunctionValues
		a.currentSpecializedValueTypes = mergedSpecializedValueTypes
		if exit := a.checkDerivedLoopTransfer(n.Body, n.Cond, nil, derivedEntry, n.Pos()); exit != nil {
			for root, typ := range exit {
				a.bindTrackedValueType(root, typ)
			}
		}
		a.currentStorageViewDeps = mergedStorageViewDeps
		// Export `inv ∧ ¬cond` as after-loop facts when every invariant was proven inductive above.
		if loopExitFactsSound {
			a.seedLoopExitFacts(n, provenInvariants)
		}
		if countUpExitSound {
			a.applyCountUpWhileExitFacts(n)
		}
	case *ast.ForStmt:
		a.analyzeForStmt(n)
	case *ast.IterForStmt:
		a.analyzeIterForStmt(n)
	case *ast.ParallelForStmt:
		a.analyzeParallelForStmt(n)
	case *ast.PassStmt:
		return
	case *ast.SignalStmt:
		refs := a.resolvePermissionRefs(n.Permissions, true)
		a.recordFunctionPermissionRefs(refs)
		return
	case *ast.PanicStmt:
		a.analyzeExpr(n.Message)
	case *ast.ExprStmt:
		if cond, ok := a.assertedCondition(n.Expr); ok {
			// An `assert` is a contract: ghost vars are readable here (it is debug-checked / erased
			// in release, never observable by real values).
			a.ghostReadAllowed++
			a.ghostReadSeen = false
			defer func() { a.ghostReadAllowed-- }()
			effectStart := len(a.currentFunctionUsedPermissionRefs)
			condType := a.analyzeCondExpr(cond)
			if effectStart <= len(a.currentFunctionUsedPermissionRefs) {
				a.checkAssertConditionPurity(n.Pos(), cond, a.currentFunctionUsedPermissionRefs[effectStart:])
			}
			if a.ghostReadSeen {
				a.markGhostContract(n.Expr)
			}
			if !IsBoolType(condType) && !IsInvalidType(condType) {
				a.errorf(n.Pos(), "assert condition must be bool, got %s", condType)
			}
			if call, ok := n.Expr.(*ast.CallExpr); ok {
				a.recordBuiltinHelperFuncType(call, callIdentName(call), condType)
			}
			// docs/98 — proof holes: under strict proofs a plain `assert` is held to the same
			// prove-it-or-fail bar as `assert … by:`. Discharge BEFORE the cond is recorded as a
			// downstream fact (else it trivially entails itself); on failure emit the constructive
			// goal / known-facts / suggested-missing-fact report.
			// docs/98/101 — route a plain `assert COND` through the SAME static discharge ladder
			// `assert … by:` uses (facts/intervals → linear → SMT). On success a proof is recorded; on
			// decline the assert REMAINS a runtime check (no hard error). The constructive proof-hole hint
			// (opt-in) is then offered only when the goal was NOT discharged here.
			proven := a.dischargePlainAssert(n.Pos(), cond)
			a.checkStrictAssertProofHole(n.Pos(), cond, proven)
			a.applyConditionRefinements(a.currentScope, cond, true)
			a.applyIndexBoundsFactsForCondition(cond, true)
			a.recordSMTAssertFact(cond)
			return
		}
		exprType := a.analyzeExpr(n.Expr)
		// An error union must be handled (`try`/`match`/`catch`) or propagated
		// (`return`/`raise`); it cannot be silently dropped as a bare statement, or its
		// error — and any owned payload it carries — is swallowed. `try`/`raise` already
		// reduce to the ok type, so only a raw dropped call surfaces here.
		if isUnhandledErrorUnionType(exprType) {
			a.errorf(n.Pos(), "error union result must be handled with `try`, `match`, or `catch`, or propagated with `return`; it cannot be silently dropped")
		}
		a.checkDiscardedValue(n, exprType)
	case *ast.StaticIfStmt:
		for _, stmt := range a.activeStmtBranch(n) {
			a.analyzeStmt(stmt)
		}
	case *ast.StaticErrorStmt:
		if a.currentFuncDecl != nil && a.currentFuncDecl.Static {
			a.analyzeExpr(n.Message)
			return
		}
		if msg, ok := a.evalConstStringExpr(n.Message); ok {
			a.errorf(n.Pos(), "static error: %s", msg)
		} else {
			a.errorf(n.Pos(), "static error triggered")
		}
	case *ast.MachineCoverageStmt:
		a.checkMachineCoverage(n)
	case *ast.ContractStmt:
		// In-body `invariant` is analyzed + checked in place; a `requires`/`ensure` reaching here
		// (not lifted) was not at the function start, which is the only place they're honoured.
		if n.Kind == ast.ContractDecreases {
			return
		}
		if n.Kind != ast.ContractInvariant {
			a.errorf(n.Pos(), "`requires`/`ensure` contracts must be the first statements of the function body")
		} else if n.Cond != nil {
			a.ghostReadAllowed++
			a.ghostReadSeen = false
			defer func() { a.ghostReadAllowed-- }()
			condType := a.analyzeCondExpr(n.Cond)
			if a.ghostReadSeen {
				a.markGhostContract(n.Cond)
			}
			if condType != nil && !IsBoolType(condType) {
				a.errorf(n.Cond.Pos(), "invariant must be bool, got %s", condType)
			}
			// Prove BEFORE assuming. Applying the condition's refinements first let every invariant
			// over an immutable value prove itself (`invariant y > 5` refined `y` to [6, ∞) and then
			// "proved" `y > 5`), and an unproven invariant still fed later proofs through those
			// refinements although it is "not used as a release proof fact".
			if a.isInductiveLoopInvariant(n) || a.canAssumeLoopInvariantInPlace(n) {
				a.applyConditionRefinements(a.currentScope, n.Cond, true)
				a.applyIndexBoundsFactsForCondition(n.Cond, true)
				a.recordSMTAssertFact(n.Cond)
			}
		}
	case *ast.AssertByStmt:
		a.analyzeAssertBy(n)
	case *ast.ProofBlockStmt:
		a.analyzeProofBlock(n)
	case *ast.AssertHoleStmt:
		a.emitAssertHole(n.Pos())
	case *ast.ProofUseStmt:
		a.errorf(n.Pos(), "`use` citations are only allowed inside an `assert … by:` proof block")
	case *ast.StaticAssertStmt:
		a.analyzeStaticAssert(n.Pos(), n.Cond, n.Message)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			a.analyzeStaticAssert(item.Position, item.Cond, item.Message)
		}
	case *ast.StaticBlockStmt:
		a.analyzeStaticOnlyStmts(n.Body)
	case *ast.DiscardStmt:
		valueType := a.analyzeExpr(n.Value)
		// `_ = f()` discards the value; for an error union that swallows the error (and
		// any owned payload). The correct discard is `_ = try f() else …` (handle first,
		// then drop the ok value).
		if isUnhandledErrorUnionType(valueType) {
			a.errorf(n.Pos(), "error union result must be handled with `try`, `match`, or `catch`; discarding it with `_ =` swallows the error")
		}
		a.consumeAffineValueExpr(n.Value, valueType, "discard")
	}
}
