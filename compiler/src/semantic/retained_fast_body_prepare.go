package semantic

import "elisacore/src/ast"

type retainedFastBodies struct {
	bodies        [][]ast.Stmt
	rows          map[string]map[ast.TypeExpr]string
	contributions map[string]string
	owners        map[ast.TypeExpr]resolvedTypeNameSourceOwner
}

// prepareRetainedFastBodies admits reuse only after movement and ownership checks.
func (r *RetainedAnalysis) prepareRetainedFastBodies(a *Analyzer, shadowEdits [][3]*ast.FuncDecl, scoped map[*ast.FuncDecl]scopedDecl, oldDecls []ast.Decl, marks map[string][][2]int, spliceCount int, bodyEnvironmentUnchanged, fastBodyEnvironmentUnchanged bool) retainedFastBodies {
	var fastBodies [][]ast.Stmt
	var fastResolvedTypeNameRows map[string]map[ast.TypeExpr]string
	var fastResolvedTypeNameContributions map[string]string
	var fastResolvedTypeNameSourceOwners map[ast.TypeExpr]resolvedTypeNameSourceOwner
	// Zero edits is a comment or blank-line change: the same certificate holds,
	// with only the line movement left to authorize.
	if retainedFastPrepareMode == "on" && len(shadowEdits) == spliceCount && fastBodyEnvironmentUnchanged {
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
	return retainedFastBodies{fastBodies, fastResolvedTypeNameRows, fastResolvedTypeNameContributions, fastResolvedTypeNameSourceOwners}
}
