package regionframesyntax

import (
	"elisacore/src/ast"
	"slices"
)

// AssignmentPath returns the identifier root and field/index path in source order.
// An empty path component denotes an index, which region freshness cannot prove safe.
func AssignmentPath(target ast.Expr, unwrap func(ast.Expr) ast.Expr) (*ast.Ident, []string) {
	var path []string
	current := unwrap(target)
	for {
		switch node := current.(type) {
		case *ast.FieldExpr:
			path = append(path, node.Field)
			current = unwrap(node.Object)
		case *ast.IndexExpr:
			path = append(path, "")
			current = unwrap(node.Object)
		default:
			slices.Reverse(path)
			root, _ := current.(*ast.Ident)
			return root, path
		}
	}
}
