package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
)

func derivedStateSelfFieldPath(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "self" {
			return nil, true
		}
		return nil, false
	case *ast.FieldExpr:
		path, ok := derivedStateSelfFieldPath(n.Object)
		if !ok {
			return nil, false
		}
		return append(path, n.Field), true
	default:
		return nil, false
	}
}
func cloneDerivedStateExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.Ident:
		return &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.IntLit:
		return &ast.IntLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix, IsHex: n.IsHex}
	case *ast.FloatLit:
		return &ast.FloatLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix}
	case *ast.StringLit:
		return &ast.StringLit{Position: n.Position, Value: n.Value}
	case *ast.CharLit:
		return &ast.CharLit{Position: n.Position, Value: n.Value}
	case *ast.BoolLit:
		return &ast.BoolLit{Position: n.Position, Value: n.Value}
	case *ast.NullLit:
		return &ast.NullLit{Position: n.Position}
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: cloneDerivedStateExpr(n.Inner)}
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: n.Position, Object: cloneDerivedStateExpr(n.Object), Field: n.Field}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: cloneDerivedStateExpr(n.Operand)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: cloneDerivedStateExpr(n.Left), Right: cloneDerivedStateExpr(n.Right)}
	default:
		return expr
	}
}
func allocOwnerPos(expr *ast.AllocExpr) lexer.Pos {
	if expr != nil && expr.Owner != nil {
		return expr.Owner.Pos()
	}
	if expr != nil {
		return expr.Pos()
	}
	return lexer.Pos{}
}
func (a *Analyzer) analyzeStructLiteralArgs(expr *ast.StructLitExpr, base *StructType, bindings map[string]Type) {
	if base == nil || base.Decl == nil {
		if expr.Spread != nil {
			a.analyzeExpr(expr.Spread)
		}
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return
	}
	if expr.Spread != nil && !expr.Brace {
		a.errorf(expr.Pos(), "struct literal spread is only valid in brace struct literals")
	}
	if expr.NamedArgCount() != 0 {
		if expr.NamedArgCount() != len(expr.Args) {
			a.errorf(expr.Pos(), "struct literal %q cannot mix positional and named fields", expr.Name)
			for i := range expr.Args {
				expr.Args[i], _ = a.analyzeCallLikeValueExpr(expr.Args[i], nil)
			}
			return
		}
		ordered := make([]ast.Expr, len(base.Decl.Fields))
		fieldIndexes := make(map[string]int, len(base.Decl.Fields))
		for i, fieldDecl := range base.Decl.Fields {
			fieldIndexes[fieldDecl.Name] = i
		}
		seen := map[int]lexer.Pos{}
		ok := true
		for i := range expr.Args {
			name := expr.ArgName(i)
			index, exists := fieldIndexes[name]
			if !exists {
				a.errorf(expr.Args[i].Pos(), "struct literal %q has no field %q", expr.Name, name)
				expr.Args[i], _ = a.analyzeCallLikeValueExpr(expr.Args[i], nil)
				ok = false
				continue
			}
			fieldDecl := base.Decl.Fields[index]
			field, exists := base.Fields[fieldDecl.Name]
			if !exists {
				expr.Args[i], _ = a.analyzeCallLikeValueExpr(expr.Args[i], nil)
				ok = false
				continue
			}
			expected := field.Type
			if len(bindings) > 0 {
				expected = a.substituteType(expected, bindings, nil, nil, nil)
			}
			arg, actual := a.analyzeCallLikeValueExpr(expr.Args[i], expected)
			expr.Args[i] = arg
			if !AssignableTo(expected, actual) {
				a.errorf(expr.Args[i].Pos(), "struct literal field %q expects %s, got %s", fieldDecl.Name, expected, actual)
			}
			a.consumeAffineValueExpr(expr.Args[i], expected, "move into struct literal field "+strconv.Quote(fieldDecl.Name))
			if prev, exists := seen[index]; exists {
				a.errorf(expr.Args[i].Pos(), "struct literal %q field %q is specified more than once (first at %s:%d:%d)", expr.Name, fieldDecl.Name, prev.File, prev.Line, prev.Col)
				ok = false
				continue
			}
			seen[index] = expr.Args[i].Pos()
			ordered[index] = expr.Args[i]
		}
		for i, fieldDecl := range base.Decl.Fields {
			if _, exists := seen[i]; exists {
				continue
			}
			if expr.Spread != nil {
				continue
			}
			a.errorf(expr.Pos(), "struct literal %q is missing field %q", expr.Name, fieldDecl.Name)
			ok = false
		}
		if ok {
			expr.ResolvedArgsValid = true
			expr.ResolvedArgs = ordered
		}
		return
	}
	if expr.Spread != nil {
		if len(expr.Args) == 0 {
			expr.ResolvedArgsValid = true
			expr.ResolvedArgs = make([]ast.Expr, len(base.Decl.Fields))
			return
		}
		a.errorf(expr.Pos(), "struct literal spread requires named field overrides")
	}
	expr.ResolvedArgsValid = true
	expr.ResolvedArgs = expr.Args
	if len(expr.Args) != len(base.Decl.Fields) {
		a.errorf(expr.Pos(), "struct literal %q expects %d arguments, got %d", expr.Name, len(base.Decl.Fields), len(expr.Args))
	}
	limit := len(expr.Args)
	if len(base.Decl.Fields) < limit {
		limit = len(base.Decl.Fields)
	}
	for i := 0; i < limit; i++ {
		fieldDecl := base.Decl.Fields[i]
		field, ok := base.Fields[fieldDecl.Name]
		if !ok {
			a.analyzeExpr(expr.Args[i])
			continue
		}
		expected := field.Type
		if len(bindings) > 0 {
			expected = a.substituteType(expected, bindings, nil, nil, nil)
		}
		var actual Type
		expr.Args[i], actual = a.analyzeCallLikeValueExpr(expr.Args[i], expected)
		if !AssignableTo(expected, actual) {
			a.errorf(expr.Args[i].Pos(), "struct literal field %q expects %s, got %s", fieldDecl.Name, expected, actual)
		}
		a.consumeAffineValueExpr(expr.Args[i], expected, "move into struct literal field "+strconv.Quote(fieldDecl.Name))
	}
	for i := limit; i < len(expr.Args); i++ {
		a.analyzeExpr(expr.Args[i])
	}
}
