package semantic

import "elisacore/src/ast"

func (a *Analyzer) analyzeMatchExprArmBodyWithAffineSnapshot(body []ast.Stmt, scope *Scope) (Type, affineFlowSnapshot) {
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
	result := a.analyzeMatchExprArmBody(body, scope)
	// Entry clones belong to this path; transfer them before restoring the parent.
	snapshot := affineFlowSnapshot{Affine: a.currentAffineValues, BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.snapshotPrivateSpecializedValueTypes(), ValueBindings: a.currentValueBindings, AliasCarriers: a.cloneAliasCarriers(), AliasCarrierFieldOverrides: a.cloneAliasCarrierFieldOverrides()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	return result, snapshot
}
