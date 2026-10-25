package semantic

// Header/default validation may perform all expression checks, including local
// purity/effect checks, without becoming runtime code in the declared function's
// body. Give it temporary collectors and restore the exact enclosing collectors.
// Actual call-site cloned defaults and body struct defaults do not enter here:
// their effects/guards must remain visible to the caller's body contracts.
func (a *Analyzer) isolateFunctionBodyObservations() func() {
	permissions := a.currentFunctionUsedPermissions
	refs := a.currentFunctionUsedPermissionRefs
	guards := a.currentFunctionGuardedIndexes
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentFunctionGuardedIndexes = nil
	return func() {
		a.currentFunctionUsedPermissions = permissions
		a.currentFunctionUsedPermissionRefs = refs
		a.currentFunctionGuardedIndexes = guards
	}
}
