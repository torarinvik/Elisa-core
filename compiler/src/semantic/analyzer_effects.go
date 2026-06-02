package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type EffectAlias struct {
	Name         string
	ErrorEffects *ErrorSetType
	Permissions  []ast.PermissionRef
	Families     []string
	Decl         *ast.EffectsDecl
}

func (a *Analyzer) collectEffectAliases(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.EffectsDecl)
		if !ok || decl == nil {
			continue
		}
		qualifiedName := joinQualifiedName(scoped.Namespace, decl.Name)
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			if _, exists := a.effectAliases[qualifiedName]; exists {
				a.errorf(decl.Pos(), "duplicate effects alias %q", qualifiedName)
				return
			}
			resolvedPermissions := a.resolvePermissionRefs(decl.Permissions, true)
			families := permissionFamiliesFromRefs(resolvedPermissions)
			var resolvedErrors *ErrorSetType
			if decl.ErrorEffects != nil {
				errorType := a.resolveType(decl.ErrorEffects)
				var ok bool
				resolvedErrors, ok = errorType.(*ErrorSetType)
				if !ok && !IsInvalidType(errorType) {
					a.errorf(decl.ErrorEffects.Pos(), "effects alias %q error clause must be an error set", qualifiedName)
					return
				}
			}
			if decl.ErrorEffects == nil && len(resolvedPermissions) == 0 {
				a.errorf(decl.Pos(), "effects alias %q must include error[...] and/or can[...]", qualifiedName)
				return
			}
			a.effectAliases[qualifiedName] = &EffectAlias{
				Name:         qualifiedName,
				ErrorEffects: resolvedErrors,
				Permissions:  append([]ast.PermissionRef(nil), resolvedPermissions...),
				Families:     append([]string(nil), families...),
				Decl:         decl,
			}
		})
	}
}

// collectGrantAliases registers `grant Name = ref, ref` declarations. The refs are
// stored raw and validated here (so bad refs are reported once at the declaration);
// expandGrantAliases substitutes them at each `can`/`effects` use site.
func (a *Analyzer) collectGrantAliases(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.GrantAliasDecl)
		if !ok || decl == nil {
			continue
		}
		qualifiedName := joinQualifiedName(scoped.Namespace, decl.Name)
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			if _, exists := a.grantAliases[qualifiedName]; exists {
				a.errorf(decl.Pos(), "duplicate grant alias %q", qualifiedName)
				return
			}
			if _, _, isPerm := a.lookupVisiblePermission(qualifiedName); isPerm {
				a.errorf(decl.Pos(), "grant alias %q conflicts with a permission family of the same name", qualifiedName)
				return
			}
			if len(decl.Refs) == 0 {
				a.errorf(decl.Pos(), "grant alias %q must list at least one permission ref", qualifiedName)
				return
			}
			// Refs are validated at each use site (after all aliases are collected, so
			// one grant alias may reference another without forward-reference errors).
			a.grantAliases[qualifiedName] = append([]ast.PermissionRef(nil), decl.Refs...)
		})
	}
}

func (a *Analyzer) lookupVisibleGrantAlias(name string) ([]ast.PermissionRef, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if refs, ok := a.grantAliases[candidate]; ok {
			return refs, true
		}
	}
	return nil, false
}

// expandGrantAliases replaces any whole-name ref that names a grant alias with the
// alias's refs, iterating so a grant alias may reference another (a small depth cap
// breaks accidental cycles). Refs with a member, or that name no alias, pass through.
func (a *Analyzer) expandGrantAliases(refs []ast.PermissionRef) []ast.PermissionRef {
	if len(a.grantAliases) == 0 || len(refs) == 0 {
		return refs
	}
	out := refs
	for depth := 0; depth < 16; depth++ {
		expandedAny := false
		next := make([]ast.PermissionRef, 0, len(out))
		for _, ref := range out {
			if ref.Member == "" {
				if aliasRefs, ok := a.lookupVisibleGrantAlias(ref.Name); ok {
					next = append(next, aliasRefs...)
					expandedAny = true
					continue
				}
			}
			next = append(next, ref)
		}
		out = next
		if !expandedAny {
			break
		}
	}
	return out
}

func (a *Analyzer) lookupVisibleEffectAlias(name string) (*EffectAlias, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if alias, ok := a.effectAliases[candidate]; ok {
			return alias, candidate, true
		}
	}
	return nil, "", false
}

func cloneErrorSetExpr(expr *ast.ErrorSetExpr) *ast.ErrorSetExpr {
	if expr == nil {
		return nil
	}
	tags := make([]ast.ErrorTagExpr, 0, len(expr.Tags))
	for _, tag := range expr.Tags {
		tags = append(tags, ast.ErrorTagExpr{Position: tag.Position, SetName: tag.SetName, Tag: tag.Tag})
	}
	return &ast.ErrorSetExpr{Position: expr.Position, Tags: tags, HasEllipsis: expr.HasEllipsis}
}

func mergeErrorSetExpr(left, right *ast.ErrorSetExpr) *ast.ErrorSetExpr {
	if left == nil {
		return cloneErrorSetExpr(right)
	}
	merged := cloneErrorSetExpr(left)
	if right == nil {
		return merged
	}
	for _, tag := range right.Tags {
		merged.Tags = append(merged.Tags, ast.ErrorTagExpr{Position: tag.Position, SetName: tag.SetName, Tag: tag.Tag})
	}
	merged.HasEllipsis = merged.HasEllipsis || right.HasEllipsis
	return merged
}

func (a *Analyzer) expandEffectAlias(ret ast.TypeExpr, permissionRefs []ast.PermissionRef, effectAliasName string, effectAliasPos lexer.Pos) (ast.TypeExpr, []ast.PermissionRef) {
	if effectAliasName == "" {
		return ret, permissionRefs
	}
	alias, _, ok := a.lookupVisibleEffectAlias(effectAliasName)
	if !ok {
		a.errorf(effectAliasPos, "unknown effects alias %q", effectAliasName)
		return ret, permissionRefs
	}
	if _, hasExplicitError := ret.(*ast.ErrorUnionTypeExpr); hasExplicitError {
		a.errorf(effectAliasPos, "effects alias %q cannot be combined with an explicit error[...] clause", effectAliasName)
	}
	if len(permissionRefs) != 0 {
		a.errorf(effectAliasPos, "effects alias %q cannot be combined with an explicit can[...] clause", effectAliasName)
	}
	mergedPermissions := mergePermissionRefs(permissionRefs, alias.Permissions)
	if alias.Decl == nil || alias.Decl.ErrorEffects == nil {
		return ret, mergedPermissions
	}
	valueExpr := ret
	if valueExpr == nil {
		valueExpr = &ast.NamedType{Position: effectAliasPos, Name: "void"}
	}
	return &ast.ErrorUnionTypeExpr{
		Position: valueExpr.Pos(),
		Value:    valueExpr,
		Errors:   cloneErrorSetExpr(alias.Decl.ErrorEffects),
	}, mergedPermissions
}

func (a *Analyzer) expandSignatureEffects(ret ast.TypeExpr, permissionRefs []ast.PermissionRef, effectAliasName string, effectAliasPos lexer.Pos, effects []ast.SignatureEffectItem) (ast.TypeExpr, []ast.PermissionRef) {
	if len(effects) == 0 {
		return a.expandEffectAlias(ret, permissionRefs, effectAliasName, effectAliasPos)
	}
	if effectAliasName != "" {
		legacyAlias := ast.SignatureEffectItem{Position: effectAliasPos, Alias: effectAliasName}
		effects = append([]ast.SignatureEffectItem{legacyAlias}, effects...)
	}
	_, hadExplicitReturnErrors := ret.(*ast.ErrorUnionTypeExpr)
	addErrorEffects := func(pos lexer.Pos, label string, errorEffects *ast.ErrorSetExpr) {
		if errorEffects == nil {
			return
		}
		if hadExplicitReturnErrors {
			a.errorf(pos, "effects[%s] cannot be combined with an explicit error[...] clause", label)
			return
		}
		if union, ok := ret.(*ast.ErrorUnionTypeExpr); ok {
			if existing, ok := union.Errors.(*ast.ErrorSetExpr); ok {
				union.Errors = mergeErrorSetExpr(existing, errorEffects)
			}
			return
		}
		valueExpr := ret
		if valueExpr == nil {
			valueExpr = &ast.NamedType{Position: pos, Name: "void"}
		}
		ret = &ast.ErrorUnionTypeExpr{Position: valueExpr.Pos(), Value: valueExpr, Errors: cloneErrorSetExpr(errorEffects)}
	}
	for _, item := range effects {
		if item.ErrorEffects != nil {
			addErrorEffects(item.Position, "error", item.ErrorEffects)
			continue
		}
		if item.Permission != nil {
			permissionRefs = mergePermissionRefs(permissionRefs, []ast.PermissionRef{*item.Permission})
			continue
		}
		if item.Alias == "" {
			continue
		}
		if alias, _, ok := a.lookupVisibleEffectAlias(item.Alias); ok {
			permissionRefs = mergePermissionRefs(permissionRefs, alias.Permissions)
			if alias.Decl != nil {
				addErrorEffects(item.Position, item.Alias, alias.Decl.ErrorEffects)
			}
			continue
		}
		permissionRefs = mergePermissionRefs(permissionRefs, []ast.PermissionRef{{Position: item.Position, Name: item.Alias}})
	}
	return ret, permissionRefs
}
