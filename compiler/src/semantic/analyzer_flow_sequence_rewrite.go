package semantic

import (
	"strings"

	"elisacore/src/ast"
)

func sequenceRewriteTargetTypeExpr(rootExpr ast.TypeExpr) (ast.TypeExpr, bool) {
	generic, ok := rootExpr.(*ast.GenericType)
	if !ok || generic == nil || generic.Name != "sequence" || len(generic.Args) != 1 {
		return nil, false
	}
	return generic.Args[0], true
}

func sequenceRewriteCarrierElemType(t Type) (Type, bool) {
	switch tt := StripAggregateStateType(t).(type) {
	case *DArrayType:
		return tt.Elem, true
	case *DArrayViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "dview" {
			return nil, false
		}
		return tt.Elem, true
	default:
		return nil, false
	}
}

func sequenceRewriteArmBindName(arm ast.VisitArm) (string, bool) {
	if arm.Wildcard {
		return "", true
	}
	if arm.TargetName != "" && !strings.Contains(arm.TargetName, ".") && arm.BindName == "" && arm.ChildResultsName == "" && len(arm.ChildBindings) == 0 {
		return arm.TargetName, true
	}
	return "", false
}

type sequenceRewriteArmInfo struct {
	BindType      Type
	BindName      string
	AlwaysMatches bool
}

func (a *Analyzer) resolveSequenceRewriteArmInfo(elemType Type, arm ast.VisitArm) (sequenceRewriteArmInfo, bool) {
	if arm.Wildcard {
		return sequenceRewriteArmInfo{AlwaysMatches: true}, true
	}
	if bindName, ok := sequenceRewriteArmBindName(arm); ok {
		return sequenceRewriteArmInfo{BindType: elemType, BindName: bindName, AlwaysMatches: true}, true
	}
	if arm.ChildResultsName != "" || len(arm.ChildBindings) != 0 {
		a.errorf(arm.Position, "sequence rewrite tree-target arms do not support child result bindings")
		return sequenceRewriteArmInfo{}, false
	}
	if _, _, ok := resolveTreeVisitSourceType(elemType); !ok {
		a.errorf(arm.Position, "sequence rewrite arms currently support only `_`, a bare element binding name, or an exact tree target with an optional bind name")
		return sequenceRewriteArmInfo{}, false
	}
	root, ok := a.resolveVisitRootInfo(elemType, nil, arm.Position)
	if !ok {
		return sequenceRewriteArmInfo{}, false
	}
	armInfo, ok := a.resolveVisitArmInfo(root, arm)
	if !ok {
		return sequenceRewriteArmInfo{}, false
	}
	return sequenceRewriteArmInfo{BindType: armInfo.BindType, BindName: arm.BindName, AlwaysMatches: root.Kind == treeVisitRootKindExact}, true
}

func (a *Analyzer) analyzeSequenceRewriteArmBody(body []ast.Stmt, scope *Scope) {
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	a.currentScope = scope
	a.currentRegions = a.cloneRegionStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	defer func() {
		a.currentScope = savedScope
		a.currentRegions = savedRegions
		a.currentPackedVariantViews = savedPackedVariantViews
		a.currentPackedStores = savedPackedStores
		a.currentPackedStoreResolutions = savedPackedStoreResolutions
	}()
	for _, stmt := range body {
		a.analyzeStmt(stmt)
		if stmtDefinitelyExits(stmt) {
			return
		}
	}
}

func (a *Analyzer) analyzeSequenceRewriteArmBodyWithAffineSnapshot(body []ast.Stmt, scope *Scope) affineFlowSnapshot {
	savedAffine := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	a.currentAffineValues = a.cloneAffineValueStates()
	a.currentBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
	a.currentFunctionValues = a.cloneFunctionValueBindings()
	a.currentSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
	a.currentValueBindings = a.cloneValueBindings()
	a.analyzeSequenceRewriteArmBody(body, scope)
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	return snapshot
}

func (a *Analyzer) analyzeSequenceRewriteExpr(expr *ast.FoldExpr) Type {
	return a.analyzeSequenceRewriteExprWithExpected(expr, nil)
}

func (a *Analyzer) analyzeSequenceRewriteExprWithExpected(expr *ast.FoldExpr, expected Type) Type {
	valueType := a.analyzeExpr(expr.Value)
	elemType, ok := sequenceRewriteCarrierElemType(valueType)
	if !ok || elemType == nil {
		a.errorf(expr.Pos(), "sequence rewrite expects a darray or dview source, got %s", valueType)
		for _, arm := range expr.Arms {
			a.analyzeSequenceRewriteArmBody(arm.Body, NewScope(a.currentScope))
		}
		return invalidType
	}
	targetTypeExpr, ok := sequenceRewriteTargetTypeExpr(expr.Root)
	if !ok {
		a.errorf(expr.Pos(), "sequence rewrite expects `as sequence[T]`")
		for _, arm := range expr.Arms {
			a.analyzeSequenceRewriteArmBody(arm.Body, NewScope(a.currentScope))
		}
		return invalidType
	}
	outputElemType := a.resolveType(targetTypeExpr)
	if outputElemType == nil || IsInvalidType(outputElemType) {
		for _, arm := range expr.Arms {
			a.analyzeSequenceRewriteArmBody(arm.Body, NewScope(a.currentScope))
		}
		return invalidType
	}
	resultType, _ := a.stampContainerRegion(&DArrayType{Elem: outputElemType, Shape: &WildcardShape{}, SurfaceName: "darray"}).(*DArrayType)
	if expectedDArray, ok := contextualDArrayLiteralType(expected); ok && AssignableTo(expectedDArray.Elem, outputElemType) {
		resultType = expectedDArray
	}
	if a.currentTreeAllocOwner.Kind == treeAllocOwnerNone && !a.regionAvailableForContainer(resultType) {
		a.errorf(expr.Pos(), "sequence rewrite requires an active in <owner>: scope")
	}
	baselineAffine := a.cloneAffineValueStates()
	baselineBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	baselineFunctionValues := a.cloneFunctionValueBindings()
	baselineSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
	mergedAffine := baselineAffine
	mergedBorrowedOwnerRefs := baselineBorrowedOwnerRefs
	mergedFunctionValues := baselineFunctionValues
	mergedSpecializedValueTypes := baselineSpecializedValueTypes
	hasUnconditional := false
	for _, arm := range expr.Arms {
		if hasUnconditional {
			a.errorf(arm.Position, "sequence rewrite arm is unreachable because an earlier unguarded arm already matches")
		}
		armInfo, armOK := a.resolveSequenceRewriteArmInfo(elemType, arm)
		scope := NewScope(a.currentScope)
		if armOK && armInfo.BindName != "" && armInfo.BindType != nil {
			a.defineLocalInScope(scope, &Symbol{Name: armInfo.BindName, Kind: SymbolLocal, Type: armInfo.BindType, Mutable: false}, arm.Position)
		}
		bodyScope := scope
		guardFallthrough := affineFlowSnapshot{}
		if arm.Guard != nil {
			guardType, guardSnapshot := a.analyzeExprInAffineScope(arm.Guard, scope)
			if !IsBoolType(guardType) {
				a.errorf(arm.Guard.Pos(), "sequence rewrite arm guard must be bool, got %s", guardType)
			}
			guardFallthrough = guardSnapshot
			bodyScope = a.refinedScopeForCondition(scope, arm.Guard, true)
		}
		savedSeq := a.currentSequenceRewrite
		a.currentSequenceRewrite = &sequenceRewriteContext{ElemType: elemType, OutputElem: outputElemType}
		bodySnapshot := a.analyzeSequenceRewriteArmBodyWithAffineSnapshot(arm.Body, bodyScope)
		a.currentSequenceRewrite = savedSeq
		if arm.Guard != nil {
			bodySnapshot.Affine = mergeAffineValueStates(bodySnapshot.Affine, guardFallthrough.Affine)
			bodySnapshot.BorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(bodySnapshot.BorrowedOwnerRefs, guardFallthrough.BorrowedOwnerRefs)
			bodySnapshot.FunctionValues = a.mergeFunctionValueBindings(bodySnapshot.FunctionValues, guardFallthrough.FunctionValues)
			bodySnapshot.SpecializedValueTypes = a.mergeSpecializedValueTypeBindings(bodySnapshot.SpecializedValueTypes, guardFallthrough.SpecializedValueTypes)
		}
		if !armInfo.AlwaysMatches {
			bodySnapshot.Affine = mergeAffineValueStates(bodySnapshot.Affine, baselineAffine)
			bodySnapshot.BorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(bodySnapshot.BorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			bodySnapshot.FunctionValues = a.mergeFunctionValueBindings(bodySnapshot.FunctionValues, baselineFunctionValues)
			bodySnapshot.SpecializedValueTypes = a.mergeSpecializedValueTypeBindings(bodySnapshot.SpecializedValueTypes, baselineSpecializedValueTypes)
		} else if arm.Guard == nil {
			hasUnconditional = true
		}
		mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
		mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
		mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.exprTypes[expr] = resultType
	return resultType
}
