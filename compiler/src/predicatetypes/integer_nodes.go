package predicatetypes

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"sync"
)

var integerSeenPool = sync.Pool{New: func() any { return make(map[ast.Expr]bool, 256) }}

// IntegerExpressionNodes validates the replay-safe integer expression subset.
func IntegerExpressionNodes(root ast.Expr, parameters map[string]bool, sourceBytes, byteBudget int, parameterSchemaValid bool, remainingNodes *int, budgetExhausted *bool) ([]ast.Expr, int, bool) {

	if root == nil || sourceBytes > byteBudget {
		return nil, sourceBytes, false
	}
	var nodes []ast.Expr
	seen := integerSeenPool.Get().(map[ast.Expr]bool)
	defer func() { clear(seen); integerSeenPool.Put(seen) }()
	var visit func(ast.Expr, int) bool
	visit = func(expr ast.Expr, depth int) bool {
		if expr == nil || depth > 128 || len(nodes) >= 4096 {
			return false
		}
		if seen[expr] {
			return false
		}
		if remainingNodes != nil {
			if *remainingNodes <= 0 {
				if budgetExhausted != nil {
					*budgetExhausted = true
				}
				return false
			}
			(*remainingNodes)--
		}
		seen[expr] = true
		nodes = append(nodes, expr)
		switch node := expr.(type) {
		case *ast.Ident:
			if !parameterSchemaValid || !parameters[node.Name] || len(node.Name) > byteBudget-sourceBytes {
				return false
			}
			sourceBytes += len(node.Name)
			return true
		case *ast.IntLit:
			if len(node.Value) > byteBudget-sourceBytes {
				return false
			}
			sourceBytes += len(node.Value)
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Value = ""
			copy.IsHex = false
			return node.Suffix == "" && reflect.ValueOf(copy).IsZero()
		case *ast.ParenExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Inner = nil
			return reflect.ValueOf(copy).IsZero() && visit(node.Inner, depth+1)
		case *ast.UnaryExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Op = 0
			copy.Operand = nil
			return node.Op == lexer.TOKEN_MINUS && reflect.ValueOf(copy).IsZero() && visit(node.Operand, depth+1)
		case *ast.BinaryExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Op = 0
			copy.Left = nil
			copy.Right = nil
			if !reflect.ValueOf(copy).IsZero() || (node.Op != lexer.TOKEN_PLUS && node.Op != lexer.TOKEN_MINUS && node.Op != lexer.TOKEN_STAR) {
				return false
			}
			return visit(node.Left, depth+1) && visit(node.Right, depth+1)
		default:
			return false
		}
	}
	if !visit(root, 0) {
		return nil, sourceBytes, false
	}
	return nodes, sourceBytes, true

}
