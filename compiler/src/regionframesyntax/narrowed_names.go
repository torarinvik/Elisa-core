package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/astprunedscan"
)

func peelMutableTypeExpr(typ ast.TypeExpr) ast.TypeExpr {
	for {
		mutable, ok := typ.(*ast.MutableType)
		if !ok || mutable == nil {
			return typ
		}
		typ = mutable.Elem
	}
}

// NarrowedNames returns NAME plus every local that narrows it to the same referent: an
// optional borrow parameter (`edges: mutable darray[T]&?`) is grown only through the name a
// refinement binds — `if edges is e: e.push(x)` or `e = get edges else return` — or a
// typed reference alias (`e: mutable darray[T]& = edges`). Growth of such
// a name is growth of the parameter's container, so it needs the caller's region exactly as
// `edges.push(x)` on a plain borrow does.
func NarrowedNames(stmts []ast.Stmt, name string) map[string]bool {
	names := map[string]bool{name: true}
	refersToName := func(value ast.Expr) bool {
		id, ok := UnwrapRegionValue(value).(*ast.Ident)
		return ok && id != nil && id.Name == name
	}
	astprunedscan.WalkPruned(stmts, func(node any) bool {
		switch n := node.(type) {
		case *ast.OptionalBindExpr:
			if n != nil && refersToName(n.Value) {
				names[n.Name] = true
			}
		case *ast.VarDeclStmt:
			if n != nil {
				if get, ok := n.Value.(*ast.GetExpr); ok && get != nil && refersToName(get.Value) {
					names[n.Name] = true
				}
				// A typed reference alias (`e: mutable darray[T]& = edges`) names the same
				// container. Only a written reference type counts: an untyped `copy = edges`
				// of a by-value container is a separate container.
				if _, isRef := peelMutableTypeExpr(n.Type).(*ast.RefType); isRef && refersToName(n.Value) {
					names[n.Name] = true
				}
			}
		}
		return true
	})
	return names
}
