package parser

import (
	"elisacore/src/ast"
)

func rewriteWhereTupleBinderExpr(expr ast.Expr, tupleName string, binders []string) ast.Expr {
	if expr == nil {
		return nil
	}
	fieldForBinder := func(name string) (string, bool) {
		for i, binder := range binders {
			if binder != name {
				continue
			}
			switch i {
			case 0:
				return "index", true
			case 1:
				return "value", true
			default:
				return "", false
			}
		}
		return "", false
	}
	var rewrite func(ast.Expr) ast.Expr
	rewrite = func(value ast.Expr) ast.Expr {
		switch n := value.(type) {
		case *ast.Ident:
			if field, ok := fieldForBinder(n.Name); ok {
				return &ast.FieldExpr{Position: n.Position, Object: &ast.Ident{Position: n.Position, Name: tupleName}, Field: field}
			}
			return value
		case *ast.ParenExpr:
			n.Inner = rewrite(n.Inner)
		case *ast.BinaryExpr:
			n.Left = rewrite(n.Left)
			n.Right = rewrite(n.Right)
		case *ast.UnaryExpr:
			n.Operand = rewrite(n.Operand)
		case *ast.CallExpr:
			n.Func = rewrite(n.Func)
			for i, arg := range n.Args {
				n.Args[i] = rewrite(arg)
			}
		case *ast.FieldExpr:
			n.Object = rewrite(n.Object)
		case *ast.IndexExpr:
			n.Object = rewrite(n.Object)
			n.Index = rewrite(n.Index)
			n.Fallback = rewrite(n.Fallback)
		case *ast.SliceExpr:
			n.Object = rewrite(n.Object)
			n.Start = rewrite(n.Start)
			n.End = rewrite(n.End)
		case *ast.TernaryExpr:
			n.Value = rewrite(n.Value)
			n.Cond = rewrite(n.Cond)
			n.Alt = rewrite(n.Alt)
		case *ast.CastExpr:
			n.Operand = rewrite(n.Operand)
		case *ast.TupleExpr:
			for i, elem := range n.Elems {
				n.Elems[i] = rewrite(elem)
			}
		case *ast.ListLitExpr:
			for i, elem := range n.Elems {
				n.Elems[i] = rewrite(elem)
			}
		case *ast.StructLitExpr:
			for i, arg := range n.Args {
				n.Args[i] = rewrite(arg)
			}
		case *ast.RecordUpdateExpr:
			n.Base = rewrite(n.Base)
			for i, arg := range n.Args {
				n.Args[i] = rewrite(arg)
			}
		}
		return value
	}
	return rewrite(expr)
}
