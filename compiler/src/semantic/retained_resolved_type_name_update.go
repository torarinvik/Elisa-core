package semantic

import "elisacore/src/ast"

// updateResolvedTypeNameOwners carries stable type-name ownership across the body replay.
func (r *RetainedAnalysis) updateResolvedTypeNameOwners(pre *preBodyState, removedLive []*ast.FuncDecl, functionKeysBeforeReplay map[*ast.FuncDecl]string, pairedNew map[*ast.FuncDecl]*ast.FuncDecl, splices []retainedSplice, addedScoped []scopedDecl, declarationsChanged bool) {
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
		if declarationsChanged {
			typeNameOwnersValid = false
		}
	}
	r.resolvedTypeNameSourceOwners = nextTypeNameOwners
	r.resolvedTypeNameSourceOwnersValid = typeNameOwnersValid
}
