package semantic

import "elisacore/src/ast"

// checkUntypedShadow rejects `name = value` in a nested block when an enclosing block of the same
// function already binds `name` as a declared local or a parameter. That line DECLARES a second
// local that shadows the first, so the outer variable silently keeps its old value; code written
// with C or Python habits almost always meant `name <- value`. An explicit type (`name: T = value`)
// still shadows on purpose.
func (a *Analyzer) checkUntypedShadow(n *ast.VarDeclStmt) {
	if a.reportWrappedBodyParamRedeclaration(n) {
		return
	}
	if n == nil || n.Type != nil || n.Mutable || n.Ghost || n.Name == "_" || a.currentScope == nil {
		return
	}
	if _, here := a.currentScope.Symbols[n.Name]; here {
		return
	}
	for scope := a.currentScope.Parent; scope != nil; scope = scope.Parent {
		outer, ok := scope.Symbols[n.Name]
		if !ok {
			continue
		}
		if kind := untypedShadowOuterKind(outer); kind != "" {
			a.errorf(n.Pos(), "%s", UntypedShadowMessage(n.Name, kind))
		}
		return
	}
}

// untypedShadowOuterKind names the binding an untyped `=` would shadow, or "" when it is not a
// source-declared local or a function parameter (loop and pattern binders, globals, captures).
func untypedShadowOuterKind(outer *Symbol) string {
	switch outer.Node.(type) {
	case *ast.VarDeclStmt:
		if outer.Kind == SymbolLocal {
			return "local"
		}
	case *ast.FuncDecl:
		if outer.Kind == SymbolParam {
			return "parameter"
		}
	}
	return ""
}
