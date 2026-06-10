package semantic

import (
	"elisacore/src/ast"
)

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
