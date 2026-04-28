package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) analyzeVisitExpr(expr *ast.VisitExpr) Type {
	valueType := a.analyzeExpr(expr.Value)
	root, ok := a.resolveVisitRootInfo(valueType, expr.Root, expr.Pos())
	if !ok {
		for _, arm := range expr.Arms {
			a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
		}
		return invalidType
	}
	covered := map[string]bool{}
	priorKeys := map[string]bool{}
	hasWildcard := false
	resultType := Type(nil)
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	for _, arm := range expr.Arms {
		armInfo, armOK := a.resolveVisitArmInfo(root, arm)
		guarded := arm.Guard != nil
		if armInfo.Arm.Wildcard {
			if hasWildcard {
				a.errorf(arm.Position, "visit wildcard arm is unreachable because an earlier wildcard already matches")
			}
			if !guarded {
				hasWildcard = true
			}
		} else if armOK {
			if hasWildcard {
				a.errorf(arm.Position, "visit arm %q is unreachable because an earlier wildcard already matches", arm.TargetName)
			}
			if priorKeys[armInfo.Key] {
				a.errorf(arm.Position, "visit arm %q is unreachable because an earlier arm already matches it", arm.TargetName)
			}
			if !guarded {
				priorKeys[armInfo.Key] = true
				covered[armInfo.Key] = true
			}
		}
		scope := NewScope(a.currentScope)
		armType, armSnapshot, armCanFallthrough := a.analyzeVisitArmBody(armInfo, nil, scope, false, "", false, nil)
		if armCanFallthrough {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		if resultType == nil {
			resultType = armType
			continue
		}
		merged := MergeTypes(resultType, armType)
		if IsInvalidType(merged) {
			a.errorf(arm.Position, "visit expression arms are incompatible: %s and %s", resultType, armType)
			resultType = invalidType
			continue
		}
		resultType = merged
	}
	if !hasWildcard {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else if len(visitDomainKeys(root)) != len(covered) {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.reportNonExhaustiveVisit(expr.Pos(), root, covered, hasWildcard, "visit")
	if resultType == nil {
		return neverType
	}
	return resultType
}

func treeVisitRootBindType(root treeVisitRootInfo) Type {
	switch root.Kind {
	case treeVisitRootKindCategory:
		return root.Category
	case treeVisitRootKindExact:
		return root.Exact
	case treeVisitRootKindFamily:
		if root.Family != nil {
			return root.Family.NodeType
		}
	}
	return nil
}

func treeExactStructuralChildTypes(exact Type) []Type {
	family, ok := TreeFamilyForMemberType(exact)
	if !ok || family == nil {
		return nil
	}
	var decls []ast.FieldDecl
	switch tt := StripAggregateStateType(exact).(type) {
	case *TreeBlockType:
		decls = TreeBlockFieldDeclsWithCommon(tt)
	case *TreeStructType:
		decls = TreeStructFieldDeclsWithCommon(tt)
	default:
		return nil
	}
	out := make([]Type, 0, len(decls))
	for _, fieldDecl := range decls {
		field, ok := TreeExactFieldInfo(exact, fieldDecl.Name)
		if !ok {
			continue
		}
		relation := TreeFieldStructuralRelation(family, field.Type)
		if itemType, ok := TreeStructuralChildItemType(field.Type, relation); ok && itemType != nil {
			out = append(out, itemType)
		}
	}
	return out
}

func treeVisitRootStructuralChildTypes(root treeVisitRootInfo) []Type {
	switch root.Kind {
	case treeVisitRootKindCategory:
		if root.Category == nil {
			return nil
		}
		out := make([]Type, 0)
		for _, variant := range root.Category.Variants {
			for i, payloadType := range variant.Payload {
				relation := variant.PayloadRelation(i)
				if itemType, ok := TreeStructuralChildItemType(payloadType, relation); ok && itemType != nil {
					out = append(out, itemType)
				}
			}
		}
		return out
	case treeVisitRootKindExact:
		return treeExactStructuralChildTypes(root.Exact)
	case treeVisitRootKindFamily:
		if root.Family == nil {
			return nil
		}
		out := make([]Type, 0)
		for _, member := range TreeFamilyExactMembersInTagOrder(root.Family) {
			switch tt := member.(type) {
			case *TreeVariantViewType:
				if tt == nil || tt.Variant == nil {
					continue
				}
				for i, payloadType := range tt.Variant.Payload {
					relation := tt.Variant.PayloadRelation(i)
					if itemType, ok := TreeStructuralChildItemType(payloadType, relation); ok && itemType != nil {
						out = append(out, itemType)
					}
				}
			default:
				out = append(out, treeExactStructuralChildTypes(member)...)
			}
		}
		return out
	default:
		return nil
	}
}

func (a *Analyzer) validateFoldRecursionRoot(pos lexer.Pos, root treeVisitRootInfo, keyword string) {
	if a == nil || root.Kind == treeVisitRootKindFamily {
		return
	}
	if keyword == "" {
		keyword = "fold"
	}
	rootType := treeVisitRootBindType(root)
	if rootType == nil {
		return
	}
	for _, childType := range treeVisitRootStructuralChildTypes(root) {
		if childType == nil {
			continue
		}
		if !AssignableTo(rootType, childType) {
			familyLabel := "<tree>"
			if root.Family != nil && root.Family.NodeType != nil {
				familyLabel = root.Family.NodeType.String()
			}
			a.errorf(pos, "%s over %s requires an explicit `as %s` root because structural children include %s", keyword, rootType, familyLabel, childType)
			return
		}
	}
}

func (a *Analyzer) recordFoldExprInfo(expr *ast.FoldExpr) {
	if a == nil || expr == nil || a.currentScope == nil {
		return
	}
	collector := newDeferCaptureCollector(a, a.currentScope)
	for _, arm := range expr.Arms {
		armLocals := map[string]bool{}
		appendVisitArmLocals(armLocals, arm)
		collector.collectExpr(arm.Guard, cloneParallelForLocals(armLocals))
		for _, stmt := range arm.Body {
			collector.collectStmt(stmt, cloneParallelForLocals(armLocals))
		}
	}
	a.foldInfo[expr] = &FoldInfo{Captures: append([]string(nil), collector.captureOrder...)}
}
