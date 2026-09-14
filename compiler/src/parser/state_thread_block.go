package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// A field-mutating block that yields its assignment target on every path is an
// in-place state thread. Keep its lexical scopes and cleanup, license only the
// named target, and erase the redundant aggregate result. Explicit capture blocks
// retain their ordinary value/snapshot semantics.
func desugarStateThreadBlock(target, value ast.Expr) (ast.Stmt, bool) {
	id, ok := target.(*ast.Ident)
	if !ok {
		return nil, false
	}
	block, ok := value.(*ast.ExprBlock)
	if !ok || len(block.Captures) != 0 || stateThreadShape(value, id.Name) != 2 {
		return nil, false
	}
	return &ast.ExprStmt{Position: value.Pos(), Expr: eraseStateThreadResult(value, id.Name)}, true
}

// 0: ordinary value; 1: identity; 2: identity with field mutations.
func stateThreadShape(value ast.Expr, name string) int {
	switch n := value.(type) {
	case *ast.Ident:
		if n.Name == name {
			return 1
		}
	case *ast.ParenExpr:
		return stateThreadShape(n.Inner, name)
	case *ast.TernaryExpr:
		if stateThreadBindingCondition(n.Cond) {
			return 0
		}
		left, right := stateThreadShape(n.Value, name), stateThreadShape(n.Alt, name)
		if left == 0 || right == 0 {
			return 0
		}
		if left == 2 || right == 2 {
			return 2
		}
		return 1
	case *ast.ExprBlock:
		shape := stateThreadShape(n.Value, name)
		if shape == 0 {
			return 0
		}
		for _, stmt := range n.Stmts {
			// Other declaration/control-flow forms may introduce a binding
			// that shadows the identity result. Leave them on the value path.
			switch stmt.(type) {
			case *ast.VarDeclStmt, *ast.AssignStmt, *ast.AugAssignStmt, *ast.DeferStmt:
			default:
				return 0
			}
			if decl, ok := stmt.(*ast.VarDeclStmt); ok && decl.Name == name {
				return 0
			}
			var written ast.Expr
			switch n := stmt.(type) {
			case *ast.AssignStmt:
				written = n.Target
			case *ast.AugAssignStmt:
				written = n.Target
			}
			if written != nil {
				switch written.(type) {
				case *ast.Ident, *ast.FieldExpr:
				default:
					return 0
				}
				if _, field := written.(*ast.FieldExpr); field && stateThreadRoot(written, name) {
					shape = 2
				}
			}
		}
		return shape
	}
	return 0
}

func eraseStateThreadResult(value ast.Expr, name string) ast.Expr {
	switch n := value.(type) {
	case *ast.ExprBlock:
		return &ast.ExprBlock{Position: n.Position, Stmts: n.Stmts, Value: eraseStateThreadResult(n.Value, name), Captures: appendMissingNames(append([]string(nil), n.Captures...), []string{name})}
	case *ast.TernaryExpr:
		return &ast.TernaryExpr{Position: n.Position, Cond: n.Cond, Value: eraseStateThreadResult(n.Value, name), Alt: eraseStateThreadResult(n.Alt, name)}
	case *ast.ParenExpr:
		return eraseStateThreadResult(n.Inner, name)
	default:
		return &ast.IntLit{Position: value.Pos(), Value: "0"}
	}
}

func stateThreadBindingCondition(value ast.Expr) bool {
	switch n := value.(type) {
	case *ast.OptionalBindExpr:
		return true
	case *ast.ParenExpr:
		return stateThreadBindingCondition(n.Inner)
	case *ast.UnaryExpr:
		return stateThreadBindingCondition(n.Operand)
	case *ast.BinaryExpr:
		return n.Op == lexer.TOKEN_IS || stateThreadBindingCondition(n.Left) || stateThreadBindingCondition(n.Right)
	}
	return false
}

func stateThreadRoot(value ast.Expr, name string) bool {
	switch n := value.(type) {
	case *ast.Ident:
		return n.Name == name
	case *ast.FieldExpr:
		return stateThreadRoot(n.Object, name)
	}
	return false
}
