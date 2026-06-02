package semantic

import (
	"sort"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) collectPermissionDecls(decls []scopedDecl) {
	for _, scoped := range decls {
		var (
			name     string
			members  []string
			includes []string
			permDecl *ast.PermissionDecl
			pos      lexer.Pos
		)
		switch decl := scoped.Decl.(type) {
		case *ast.PermissionDecl:
			if decl == nil {
				continue
			}
			if decl.DeprecatedSyntax != "" {
				if decl.DeprecatedReplacement != "" {
					a.deprecatedf(decl.Position, "`%s` is deprecated; use `%s`", decl.DeprecatedSyntax, decl.DeprecatedReplacement)
				} else {
					a.deprecatedf(decl.Position, "`%s` is deprecated", decl.DeprecatedSyntax)
				}
			}
			name = decl.Name
			members = decl.Members
			includes = decl.Includes
			permDecl = decl
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
		resolvedIncludes := make([]string, 0, len(includes))
		for _, inc := range includes {
			resolvedIncludes = append(resolvedIncludes, joinQualifiedName(scoped.Namespace, inc))
		}
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
		a.permissions[qualifiedName] = &PermissionSet{Name: qualifiedName, Members: resolvedMembers, MemberSet: memberSet, Includes: resolvedIncludes, Decl: permDecl}
	}
}

// validatePermissionSubsumption checks every `includes` edge resolves to a known
// permission family and rejects include cycles, so the subsumption relation `≥`
// is a well-founded DAG before it is used in grant checking.
func (a *Analyzer) validatePermissionSubsumption() {
	names := make([]string, 0, len(a.permissions))
	for name, ps := range a.permissions {
		if len(ps.Includes) != 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	posOf := func(ps *PermissionSet) lexer.Pos {
		if ps != nil && ps.Decl != nil {
			return ps.Decl.Pos()
		}
		return lexer.Pos{}
	}

	for _, name := range names {
		ps := a.permissions[name]
		for _, inc := range ps.Includes {
			if a.permissions[inc] == nil {
				a.errorf(posOf(ps), "permission %q includes unknown family %q", name, inc)
			}
		}
	}

	// Cycle detection over the `includes` graph (DFS with a recursion stack).
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(a.permissions))
	var visit func(string) bool
	visit = func(node string) bool {
		ps := a.permissions[node]
		if ps == nil {
			return false
		}
		color[node] = gray
		for _, inc := range ps.Includes {
			switch color[inc] {
			case gray:
				a.errorf(posOf(a.permissions[node]), "permission %q has a cyclic `includes` chain through %q", node, inc)
				color[node] = black
				return true
			case white:
				if visit(inc) {
					color[node] = black
					return true
				}
			}
		}
		color[node] = black
		return false
	}
	allNames := make([]string, 0, len(a.permissions))
	for name := range a.permissions {
		allNames = append(allNames, name)
	}
	sort.Strings(allNames)
	for _, name := range allNames {
		if color[name] == white {
			visit(name)
		}
	}
}
