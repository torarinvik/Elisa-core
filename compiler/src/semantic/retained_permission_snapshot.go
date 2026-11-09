package semantic

// retainedPermissionSnapshot preserves the old equation numbering across declaration edits.
func (r *RetainedAnalysis) retainedPermissionSnapshot(a *Analyzer, changed bool) ([]scopedDecl, []*FuncType, map[*FuncType]bool) {
	// Preserve the old permission-equation numbering before changing the set.
	var permissionBefore []scopedDecl
	var permissionBeforeTypes []*FuncType
	var permissionKnownTypes map[*FuncType]bool
	if changed {
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
	return permissionBefore, permissionBeforeTypes, permissionKnownTypes
}
