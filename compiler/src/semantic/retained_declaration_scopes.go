package semantic

import "elisacore/src/ast"

// retainedDeclarationScopes records the lexical container of each current declaration.
func retainedDeclarationScopes(file *ast.File) (map[ast.Decl]bool, map[ast.Decl]*ast.NamespaceDecl, map[*ast.NamespaceDecl]string) {
	topLevel := make(map[ast.Decl]bool, len(file.Decls))
	for _, d := range file.Decls {
		topLevel[d] = true
	}
	memberOf := map[ast.Decl]*ast.NamespaceDecl{}
	namespaceOf := map[*ast.NamespaceDecl]string{}
	var collectMembers func([]ast.Decl, string)
	collectMembers = func(decls []ast.Decl, namespace string) {
		for _, d := range decls {
			if n, ok := d.(*ast.NamespaceDecl); ok && !n.Const {
				full := joinQualifiedName(namespace, n.Name)
				namespaceOf[n] = full
				for _, member := range n.Decls {
					memberOf[member] = n
				}
				collectMembers(n.Decls, full)
			}
		}
	}
	collectMembers(file.Decls, "")
	return topLevel, memberOf, namespaceOf
}

// retainedRemovableDeclarations includes direct members of ordinary namespace blocks.
func retainedRemovableDeclarations(file *ast.File) (map[ast.Decl]bool, map[ast.Decl]bool) {
	top := make(map[ast.Decl]bool, len(file.Decls))
	for _, d := range file.Decls {
		top[d] = true
	}
	removable := map[ast.Decl]bool{}
	var collect func([]ast.Decl)
	collect = func(decls []ast.Decl) {
		for _, d := range decls {
			removable[d] = true
			if ns, ok := d.(*ast.NamespaceDecl); ok && !ns.Const {
				collect(ns.Decls)
			}
		}
	}
	collect(file.Decls)
	return top, removable
}
