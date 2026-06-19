package semantic

import (
	"elisacore/src/ast"
)

// collectCapabilityAliases registers `alias Name = ref, ref` declarations. The refs are
// stored raw and validated here (so bad refs are reported once at the declaration);
// expandCapabilityAliases substitutes them at each `can` use site.
func (a *Analyzer) collectCapabilityAliases(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.AliasDecl)
		if !ok || decl == nil {
			continue
		}
		qualifiedName := joinQualifiedName(scoped.Namespace, decl.Name)
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			if _, exists := a.capabilityAliases[qualifiedName]; exists {
				a.errorf(decl.Pos(), "duplicate alias %q", qualifiedName)
				return
			}
			if _, _, isPerm := a.lookupVisiblePermission(qualifiedName); isPerm {
				a.errorf(decl.Pos(), "alias %q conflicts with a permission family of the same name", qualifiedName)
				return
			}
			if len(decl.Refs) == 0 {
				a.errorf(decl.Pos(), "alias %q must list at least one permission ref", qualifiedName)
				return
			}
			// Refs are validated at each use site (after all aliases are collected, so
			// one alias may reference another without forward-reference errors).
			a.capabilityAliases[qualifiedName] = append([]ast.PermissionRef(nil), decl.Refs...)
		})
	}
}

func (a *Analyzer) lookupVisibleCapabilityAlias(name string) ([]ast.PermissionRef, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if refs, ok := a.capabilityAliases[candidate]; ok {
			return refs, true
		}
	}
	return nil, false
}

// expandCapabilityAliases replaces any whole-name ref that names an alias with the
// alias's refs, iterating so an alias may reference another (a small depth cap
// breaks accidental cycles). Refs with a member, or that name no alias, pass through.
func (a *Analyzer) expandCapabilityAliases(refs []ast.PermissionRef) []ast.PermissionRef {
	if len(a.capabilityAliases) == 0 || len(refs) == 0 {
		return refs
	}
	out := refs
	for depth := 0; depth < 16; depth++ {
		expandedAny := false
		next := make([]ast.PermissionRef, 0, len(out))
		for _, ref := range out {
			if ref.Member == "" {
				if aliasRefs, ok := a.lookupVisibleCapabilityAlias(ref.Name); ok {
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
