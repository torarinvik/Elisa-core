package unparse

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (f *formatter) writeControlGuard(level int, n *ast.IfStmt) bool {
	if n.FromSource || len(n.Then) != 1 || len(n.Else) != 0 || len(n.Elifs) != 0 || n.Then[0].Pos().Line != n.Position.Line {
		return false
	}
	var prefix string
	switch stmt := n.Then[0].(type) {
	case *ast.ReturnStmt:
		prefix = "return"
		if stmt.Value != nil {
			prefix += " " + formatExpr(stmt.Value)
		}
	case *ast.BreakStmt:
		prefix = "break"
	case *ast.ContinueStmt:
		prefix = "continue"
	default:
		return false
	}
	if strings.Contains(prefix, "\n") {
		return false
	}
	if binary, ok := n.Cond.(*ast.BinaryExpr); ok && binary.Op == lexer.TOKEN_OR {
		f.writeLine(level, prefix+" if:")
		f.writeGuardConditions(level+1, n.Cond)
	} else {
		f.writeLine(level, prefix+" if "+formatExpr(n.Cond))
	}
	return true
}

func (f *formatter) writeGuardConditions(level int, condition ast.Expr) {
	if binary, ok := condition.(*ast.BinaryExpr); ok && binary.Op == lexer.TOKEN_OR {
		f.writeGuardConditions(level, binary.Left)
		f.writeGuardConditions(level, binary.Right)
		return
	}
	f.writePrefixedMultiline(level, "", formatExpr(condition))
}
