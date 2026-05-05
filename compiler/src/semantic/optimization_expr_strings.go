package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

func optimizationCallArg(call *ast.CallExpr, index int) (ast.Expr, bool) {
	if call == nil || index < 0 || index >= len(call.Args) {
		return nil, false
	}
	return call.Args[index], true
}

func optimizationFieldMatches(expr ast.Expr, object ast.Expr, field string) bool {
	fieldExpr, ok := stripOptimizationParens(expr).(*ast.FieldExpr)
	if !ok || fieldExpr.Field != field {
		return false
	}
	return optimizationExprString(fieldExpr.Object) == optimizationExprString(object)
}

func isZeroOptimizationExpr(expr ast.Expr) bool {
	intLit, ok := stripOptimizationParens(expr).(*ast.IntLit)
	if !ok {
		return false
	}
	return intLit.Value == "0"
}

func stripOptimizationParens(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.Inner
	}
}

func optimizationExprString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.IntLit:
		value := n.Value
		if n.Suffix != "" {
			value += n.Suffix
		}
		return value
	case *ast.StringLit:
		return fmt.Sprintf("%q", n.Value)
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.NullLit:
		return "null"
	case *ast.ZeroedLit:
		return "zeroed"
	case *ast.BinaryExpr:
		return fmt.Sprintf("(%s %s %s)", optimizationExprString(n.Left), lexer.TokenName(n.Op), optimizationExprString(n.Right))
	case *ast.UnaryExpr:
		return fmt.Sprintf("(%s %s)", lexer.TokenName(n.Op), optimizationExprString(n.Operand))
	case *ast.MoveExpr:
		return fmt.Sprintf("move %s", optimizationExprString(n.Operand))
	case *ast.CallExpr:
		args := make([]string, 0, len(n.Args))
		for i, arg := range n.Args {
			if name := n.ArgName(i); name != "" {
				args = append(args, name+": "+optimizationExprString(arg))
				continue
			}
			args = append(args, optimizationExprString(arg))
		}
		return fmt.Sprintf("%s(%s)", optimizationExprString(n.Func), joinOptimizationStrings(args))
	case *ast.FieldExpr:
		return fmt.Sprintf("%s.%s", optimizationExprString(n.Object), n.Field)
	case *ast.IndexExpr:
		line := fmt.Sprintf("%s[%s]", optimizationExprString(n.Object), optimizationExprString(n.Index))
		if n.Fallback != nil {
			line += " else " + optimizationExprString(n.Fallback)
		}
		return line
	case *ast.SliceExpr:
		return fmt.Sprintf("%s[%s:%s]", optimizationExprString(n.Object), optimizationExprString(n.Start), optimizationExprString(n.End))
	case *ast.ListLitExpr:
		parts := make([]string, 0, len(n.Elems))
		for _, elem := range n.Elems {
			parts = append(parts, optimizationExprString(elem))
		}
		return fmt.Sprintf("[%s]", joinOptimizationStrings(parts))
	case *ast.CastExpr:
		return fmt.Sprintf("%s.cast", optimizationExprString(n.Operand))
	case *ast.SizeofExpr:
		return "sizeof(...)"
	case *ast.TernaryExpr:
		return fmt.Sprintf("(%s if %s else %s)", optimizationExprString(n.Value), optimizationExprString(n.Cond), optimizationExprString(n.Alt))
	case *ast.AddrOfExpr:
		return fmt.Sprintf("&%s", optimizationExprString(n.Operand))
	case *ast.SpecializeExpr:
		return optimizationExprString(n.Operand) + ".specialize"
	case *ast.StructLitExpr:
		parts := make([]string, 0, len(n.Args))
		for i, arg := range n.Args {
			part := optimizationExprString(arg)
			if name := n.ArgName(i); name != "" {
				part = fmt.Sprintf("%s: %s", name, part)
			}
			parts = append(parts, part)
		}
		if n.Brace {
			return fmt.Sprintf("%s{%s}", n.Name, joinOptimizationStrings(parts))
		}
		return fmt.Sprintf("%s(%s)", n.Name, joinOptimizationStrings(parts))
	case *ast.RecordUpdateExpr:
		parts := make([]string, 0, len(n.Args))
		for i, arg := range n.Args {
			part := optimizationExprString(arg)
			if name := n.ArgName(i); name != "" {
				part = fmt.Sprintf("%s = %s", name, part)
			}
			parts = append(parts, part)
		}
		return fmt.Sprintf("%s{%s}", optimizationExprString(n.Base), joinOptimizationStrings(parts))
	case *ast.ParenExpr:
		return fmt.Sprintf("(%s)", optimizationExprString(n.Inner))
	case *ast.AllocExpr:
		if n.Owner == nil {
			return fmt.Sprintf("new %s", optimizationExprString(n.Value))
		}
		return fmt.Sprintf("new[%s] %s", optimizationExprString(n.Owner), optimizationExprString(n.Value))
	case *ast.CanExpr:
		return optimizationExprString(n.Expr)
	case *ast.TryExpr:
		if n.Fallback == nil {
			return fmt.Sprintf("try %s", optimizationExprString(n.Value))
		}
		return fmt.Sprintf("(try %s else %s)", optimizationExprString(n.Value), optimizationExprString(n.Fallback))
	case *ast.UnwrapElseExpr:
		return fmt.Sprintf("(%s else %s)", optimizationExprString(n.Value), optimizationExprString(n.Fallback))
	case *ast.OptionalBindExpr:
		return fmt.Sprintf("(let %s = %s)", n.Name, optimizationExprString(n.Value))
	default:
		return ""
	}
}

func joinOptimizationStrings(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += ", " + parts[i]
	}
	return result
}
