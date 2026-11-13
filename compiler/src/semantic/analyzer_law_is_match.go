package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/purematch"
)

func pureMatchSyntacticallyEqual(x, y *ast.MatchExpr) bool { return purematch.MatchEqual(x, y) }
func pureLeafEqual(x, y ast.Expr) bool                     { return purematch.LeafEqual(x, y) }
