package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func HardBoundPushElementCount(call *ast.CallExpr) int {
	if call == nil || len(call.Args) != 1 {
		return 1
	}
	list, ok := call.Args[0].(*ast.ListLitExpr)
	if !ok || list == nil || list.Brace || len(list.Elems) == 0 {
		return 1
	}
	return len(list.Elems)
}

func BoundInScopeAtRegionEntry(bound ast.Expr, paramNames map[string]bool) bool {
	switch b := bound.(type) {
	case *ast.IntLit:
		return true
	case *ast.Ident:
		return paramNames[b.Name]
	case *ast.BinaryExpr:
		switch b.Op {
		case lexer.TOKEN_STAR, lexer.TOKEN_PLUS:
			return BoundInScopeAtRegionEntry(b.Left, paramNames) && BoundInScopeAtRegionEntry(b.Right, paramNames)
		}
	}
	return false
}

// countingLoopBound returns the exclusive end N of a `for _ in 0..<N` loop with a pure ident/int
// bound (the same shape auto-reservation uses), or ok=false for any other loop.
func CountingLoopBound(node ast.Node) (ast.Expr, bool) {
	loop, ok := node.(*ast.ForStmt)
	if !ok || loop.Reverse || loop.Op != lexer.TOKEN_RANGE_LT || loop.Step != nil {
		return nil, false
	}
	if start, ok := loop.Start.(*ast.IntLit); !ok || start.Value != "0" {
		return nil, false
	}
	switch loop.End.(type) {
	case *ast.Ident, *ast.IntLit:
		return loop.End, true
	}
	return nil, false
}
