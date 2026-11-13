package predicatetypes

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"strconv"
)

func UnaryMinusIsIntrinsic(expr ast.Expr, parameterTypes map[string]string) bool {
	if expr == nil || reflect.ValueOf(expr).Kind() == reflect.Ptr && reflect.ValueOf(expr).IsNil() {
		return false
	}
	switch node := expr.(type) {
	case *ast.Ident:
		return numericName(parameterTypes[node.Name])
	case *ast.IntLit, *ast.CharLit:
		return true
	case *ast.ParenExpr:
		return UnaryMinusIsIntrinsic(node.Inner, parameterTypes)
	case *ast.UnaryExpr:
		return node.Op == lexer.TOKEN_MINUS && UnaryMinusIsIntrinsic(node.Operand, parameterTypes)
	default:
		return false
	}
}

func ComparisonOperator(op lexer.TokenKind) bool {
	switch op {
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		return true
	default:
		return false
	}
}

// Comparison analysis contextually types an unsuffixed integer literal from
// the other operand before attempting overload dispatch. The receiver is the
// left operand, so a literal on the left may dispatch on the right operand's
// integral type rather than on `int`.
func ComparisonReceiverTypeName(expr *ast.BinaryExpr, parameterTypes map[string]string) string {
	if expr == nil {
		return ""
	}
	left := exprBuiltinTypeName(expr.Left, parameterTypes)
	if !unsuffixedInteger(expr.Left) {
		return left
	}
	right := exprBuiltinTypeName(expr.Right, parameterTypes)
	if integralName(right) {
		return right
	}
	return left
}

func exprBuiltinTypeName(expr ast.Expr, parameterTypes map[string]string) string {
	if expr == nil || reflect.ValueOf(expr).Kind() == reflect.Ptr && reflect.ValueOf(expr).IsNil() {
		return ""
	}
	switch node := expr.(type) {
	case *ast.Ident:
		return parameterTypes[node.Name]
	case *ast.IntLit:
		if node.Suffix == "" {
			return "int"
		}
	case *ast.CharLit:
		return "char"
	case *ast.BoolLit:
		return "bool"
	case *ast.ParenExpr:
		return exprBuiltinTypeName(node.Inner, parameterTypes)
	case *ast.UnaryExpr:
		if node.Op == lexer.TOKEN_NOT {
			return "bool"
		}
		if node.Op == lexer.TOKEN_MINUS && UnaryMinusIsIntrinsic(node.Operand, parameterTypes) {
			return exprBuiltinTypeName(node.Operand, parameterTypes)
		}
	case *ast.BinaryExpr:
		if node.Op == lexer.TOKEN_AND || node.Op == lexer.TOKEN_OR || node.Op == lexer.TOKEN_IN || ComparisonOperator(node.Op) {
			return "bool"
		}
	}
	return ""
}

func integralName(name string) bool {
	if len(name) > 1 && (name[0] == 'i' || name[0] == 'u') {
		width, err := strconv.Atoi(name[1:])
		if err == nil && width >= 1 && width <= 64 {
			return true
		}
	}
	switch name {
	case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
		return true
	default:
		return false
	}
}
func numericName(name string) bool { return integralName(name) || name == "f32" || name == "f64" }
func unsuffixedInteger(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.IntLit:
		return n.Suffix == ""
	case *ast.ParenExpr:
		return unsuffixedInteger(n.Inner)
	case *ast.UnaryExpr:
		return n.Op == lexer.TOKEN_MINUS && unsuffixedInteger(n.Operand)
	default:
		return false
	}
}

func ReturnExpressionNodes(value ast.Expr, parameters map[string]bool, parameterTypes map[string]string, bytes, byteBudget int, parameterSchemaValid bool, remainingNodes *int, budgetExhausted *bool) ([]ast.Expr, bool) {
	var nodes []ast.Expr
	seen := map[ast.Expr]bool{}
	var activeRange *ast.MembershipRangeExpr
	var visit func(ast.Expr, int) bool
	visit = func(expr ast.Expr, depth int) bool {
		if expr == nil || (reflect.ValueOf(expr).Kind() == reflect.Ptr && reflect.ValueOf(expr).IsNil()) || depth > 128 || len(nodes) >= 4096 || seen[expr] || bytes > byteBudget {
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
			if !parameterSchemaValid || !parameters[node.Name] {
				return false
			}
			bytes += len(node.Name)
		case *ast.IntLit:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Value = ""
			copy.IsHex = false
			if node.Suffix != "" || !reflect.ValueOf(copy).IsZero() {
				return false
			}
			bytes += len(node.Value)
		case *ast.CharLit:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Value = ""
			if !reflect.ValueOf(copy).IsZero() {
				return false
			}
			bytes += len(node.Value)
		case *ast.BoolLit:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Value = false
			if !reflect.ValueOf(copy).IsZero() {
				return false
			}
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
			// Unary minus on a syntactically numeric canonical scalar is intrinsic.
			// Other receivers may lower to __neg__ and mutate the AST, which a
			// type-only replay cannot reconstruct.
			admissible := node.Op == lexer.TOKEN_NOT || node.Op == lexer.TOKEN_MINUS && UnaryMinusIsIntrinsic(node.Operand, parameterTypes)
			return admissible && reflect.ValueOf(copy).IsZero() && visit(node.Operand, depth+1)
		case *ast.MembershipRangeExpr:
			if node != activeRange {
				return false
			}
			activeRange = nil
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Start, copy.End, copy.Op = nil, nil, 0
			if !reflect.ValueOf(copy).IsZero() {
				return false
			}
			switch node.Op {
			case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_GT, lexer.TOKEN_RANGE_LE:
			default:
				return false
			}
			return visit(node.Start, depth+1) && visit(node.End, depth+1)
		case *ast.BinaryExpr:
			copy := *node
			copy.Position = lexer.Pos{}
			copy.Op = 0
			copy.Left = nil
			copy.Right = nil
			if !reflect.ValueOf(copy).IsZero() {
				return false
			}
			if node.Op == lexer.TOKEN_IN {
				rangeNode, _, ok := MembershipRange(node.Right)
				if !ok || !visit(node.Left, depth+1) {
					return false
				}
				prior := activeRange
				activeRange = rangeNode
				accepted := visit(rangeNode, depth+1)
				activeRange = prior
				return accepted
			}
			switch node.Op {
			case lexer.TOKEN_AND, lexer.TOKEN_OR, lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
			default:
				return false
			}
			return visit(node.Left, depth+1) && visit(node.Right, depth+1)
		default:
			return false
		}
		return bytes <= byteBudget
	}
	if !visit(value, 0) {
		return nil, false
	}
	return nodes, true
}
