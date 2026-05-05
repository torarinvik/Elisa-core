package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) collectPermissionDecls(decls []scopedDecl) {
	for _, scoped := range decls {
		var (
			name    string
			members []string
			pos     lexer.Pos
		)
		switch decl := scoped.Decl.(type) {
		case *ast.PermissionDecl:
			if decl == nil {
				continue
			}
			name = decl.Name
			members = decl.Members
			pos = decl.Pos()
		case *ast.EffectDecl:
			if decl == nil {
				continue
			}
			name = decl.Name
			members = decl.Members
			pos = decl.Pos()
		default:
			continue
		}
		qualifiedName := joinQualifiedName(scoped.Namespace, name)
		resolvedMembers := make([]string, 0, len(members))
		memberSet := make(map[string]bool, len(members))
		for _, member := range members {
			if memberSet[member] {
				a.errorf(pos, "duplicate permission member %q in %q", member, name)
				continue
			}
			memberSet[member] = true
			resolvedMembers = append(resolvedMembers, member)
		}
		if existing, exists := a.permissions[qualifiedName]; exists {
			if existing.Builtin && permissionMembersMatch(existing, resolvedMembers) {
				continue
			}
			if existing.Builtin {
				a.errorf(pos, "permission %q conflicts with the builtin members %q", qualifiedName, existing.Members)
				continue
			}
			a.errorf(pos, "duplicate permission %q", qualifiedName)
			continue
		}
		a.permissions[qualifiedName] = &PermissionSet{Name: qualifiedName, Members: resolvedMembers, MemberSet: memberSet}
	}
}
