package ownerinventory

import "elisacore/src/ast"

func ScanDeclarations(decls []ast.Decl, depth int, visit func() bool, add func(*ast.FuncDecl) bool) bool {
	if depth > 128 {
		return false
	}
	for _, decl := range decls {
		switch node := decl.(type) {
		case *ast.NamespaceDecl:
			if node == nil || !visit() || !ScanDeclarations(node.Decls, depth+1, visit, add) {
				return false
			}
		case *ast.StaticIfDecl:
			if node == nil || !visit() || !ScanDeclarations(node.Then, depth+1, visit, add) {
				return false
			}
			for _, clause := range node.Elifs {
				if !visit() || !ScanDeclarations(clause.Body, depth+1, visit, add) {
					return false
				}
			}
			if !ScanDeclarations(node.Else, depth+1, visit, add) {
				return false
			}
		case *ast.ImplDecl:
			if node == nil || !visit() {
				return false
			}
			for _, member := range node.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					if !add(fn) {
						return false
					}
				} else if !visit() {
					return false
				}
			}
		case *ast.InterfaceDecl:
			if node == nil || !visit() {
				return false
			}
			for _, member := range node.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					if !add(fn) {
						return false
					}
				} else if !visit() {
					return false
				}
			}
		case *ast.FuncDecl:
			if node == nil {
				if !visit() {
					return false
				}
			} else if !add(node) {
				return false
			}
		default:
			if !visit() {
				return false
			}
		}
	}
	return true
}
