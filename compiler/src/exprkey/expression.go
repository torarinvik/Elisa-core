package exprkey

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strings"
)

func CallArg(call *ast.CallExpr, index int) (ast.Expr, bool) {
	if call == nil || index < 0 || index >= len(call.Args) {
		return nil, false
	}
	return call.Args[index], true
}

func FieldMatches(expr ast.Expr, object ast.Expr, field string) bool {
	fieldExpr, ok := StripParens(expr).(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Safe || fieldExpr.Field != field ||
		!identitySupported(fieldExpr.Object, 0) || !identitySupported(object, 0) {
		return false
	}
	left, right := String(fieldExpr.Object), String(object)
	return left != "" && right != "" && left == right
}

// Field matching justifies optimization facts, so a partially rendered or
// effectful receiver must never serve as evidence of expression identity.
func identitySupported(expr ast.Expr, depth int) bool {
	if expr == nil || depth >= 64 {
		return false
	}
	switch n := expr.(type) {
	case *ast.Ident:
		return n != nil && n.Name != ""
	case *ast.IntLit:
		return n != nil && n.Value != ""
	case *ast.StringLit:
		return n != nil
	case *ast.BoolLit:
		return n != nil
	case *ast.ParenExpr:
		return n != nil && identitySupported(n.Inner, depth+1)
	case *ast.FieldExpr:
		return n != nil && !n.Safe && n.Field != "" && identitySupported(n.Object, depth+1)
	case *ast.IndexExpr:
		if n == nil || n.Fallback != nil || n.AsOverlayCall != nil || n.AsBuiltinDictGet != nil || n.AsSpecialize != nil {
			return false
		}
		return identitySupported(n.Object, depth+1) && identitySupported(n.Index, depth+1) &&
			(n.Index2 == nil || identitySupported(n.Index2, depth+1))
	default:
		return false
	}
}

func IsZero(expr ast.Expr) bool {
	intLit, ok := StripParens(expr).(*ast.IntLit)
	if !ok {
		return false
	}
	return intLit.Value == "0"
}

func StripParens(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.Inner
	}
}

func String(expr ast.Expr) string {
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
		return fmt.Sprintf("(%s %s %s)", String(n.Left), lexer.TokenName(n.Op), String(n.Right))
	case *ast.UnaryExpr:
		return fmt.Sprintf("(%s %s)", lexer.TokenName(n.Op), String(n.Operand))
	case *ast.MoveExpr:
		return fmt.Sprintf("move %s", String(n.Operand))
	case *ast.CallExpr:
		args := make([]string, 0, len(n.Args))
		for i, arg := range n.Args {
			if name := n.ArgName(i); name != "" {
				args = append(args, name+": "+String(arg))
				continue
			}
			args = append(args, String(arg))
		}
		return fmt.Sprintf("%s(%s)", String(n.Func), joinStrings(args))
	case *ast.FieldExpr:
		return fmt.Sprintf("%s.%s", String(n.Object), n.Field)
	case *ast.IndexExpr:
		index := String(n.Index)
		if n.Index2 != nil {
			index += ", " + String(n.Index2)
		}
		line := fmt.Sprintf("%s[%s]", String(n.Object), index)
		if n.Fallback != nil {
			line += " else " + String(n.Fallback)
		}
		return line
	case *ast.SliceExpr:
		return fmt.Sprintf("%s[%s:%s]", String(n.Object), String(n.Start), String(n.End))
	case *ast.ListLitExpr:
		parts := make([]string, 0, len(n.Elems))
		for i, elem := range n.Elems {
			part := String(elem)
			if i < len(n.Spreads) && n.Spreads[i] {
				part = "..." + part
			}
			if i < len(n.Keys) {
				part = String(n.Keys[i]) + ": " + part
			}
			parts = append(parts, part)
		}
		line := fmt.Sprintf("[%s]", joinStrings(parts))
		if n.Brace {
			line = fmt.Sprintf("{%s}", joinStrings(parts))
		}
		if n.Owner != nil {
			line += " in " + String(n.Owner)
		}
		return line
	case *ast.CastExpr:
		return fmt.Sprintf("%s.cast", String(n.Operand))
	case *ast.SizeofExpr:
		return "size_of(...)"
	case *ast.AlignofExpr:
		return "align_of(...)"
	case *ast.OffsetofExpr:
		return "offset_of(...)"
	case *ast.TernaryExpr:
		return fmt.Sprintf("(%s if %s else %s)", String(n.Value), String(n.Cond), String(n.Alt))
	case *ast.AddrOfExpr:
		return fmt.Sprintf("&%s", String(n.Operand))
	case *ast.SpecializeExpr:
		return String(n.Operand) + ".specialize"
	case *ast.StructLitExpr:
		parts := make([]string, 0, len(n.Args))
		for i, arg := range n.Args {
			part := String(arg)
			if name := n.ArgName(i); name != "" {
				part = fmt.Sprintf("%s: %s", name, part)
			}
			parts = append(parts, part)
		}
		if n.Brace {
			return fmt.Sprintf("%s{%s}", n.Name, joinStrings(parts))
		}
		return fmt.Sprintf("%s(%s)", n.Name, joinStrings(parts))
	case *ast.RecordUpdateExpr:
		parts := make([]string, 0, len(n.Args))
		for i, arg := range n.Args {
			part := String(arg)
			if name := n.ArgName(i); name != "" {
				part = fmt.Sprintf("%s = %s", name, part)
			}
			parts = append(parts, part)
		}
		return fmt.Sprintf("%s{%s}", String(n.Base), joinStrings(parts))
	case *ast.ParenExpr:
		return fmt.Sprintf("(%s)", String(n.Inner))
	case *ast.AllocExpr:
		if n.Owner == nil {
			return fmt.Sprintf("new %s", String(n.Value))
		}
		return fmt.Sprintf("new[%s] %s", String(n.Owner), String(n.Value))
	case *ast.CanExpr:
		return String(n.Expr)
	case *ast.TryExpr:
		if n.Fallback == nil {
			return fmt.Sprintf("try %s", String(n.Value))
		}
		return fmt.Sprintf("(try %s else %s)", String(n.Value), String(n.Fallback))
	case *ast.UnwrapElseExpr:
		return fmt.Sprintf("(%s else %s)", String(n.Value), String(n.Fallback))
	case *ast.GetExpr:
		return fmt.Sprintf("(%s else %s)", String(n.Value), String(n.Fallback))
	case *ast.OptionalBindExpr:
		return fmt.Sprintf("(let %s = %s)", n.Name, String(n.Value))
	default:
		return ""
	}
}

func joinStrings(parts []string) string {
	return strings.Join(parts, ", ")
}
