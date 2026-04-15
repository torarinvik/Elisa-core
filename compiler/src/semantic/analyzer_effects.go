package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
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
