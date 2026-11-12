package parserregionscan

import "elisacore/src/ast"

func exprBlockTailNeedsAutoRegion(value ast.Expr) bool {
	var scan Scan
	return scan.exprBlockTailNeedsAutoRegion(value)
}

func (scan *Scan) exprBlockTailNeedsAutoRegion(value ast.Expr) bool {
	return scan.exprTailNeedsAutoRegion(value, false)
}

func exprTailNeedsAutoRegion(value ast.Expr, nested bool) bool {
	var scan Scan
	return scan.exprTailNeedsAutoRegion(value, nested)
}

func (scan *Scan) exprTailNeedsAutoRegion(value ast.Expr, nested bool) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	switch tail := value.(type) {
	case *ast.ListLitExpr:
		return nested && tail != nil && tail.Owner == nil
	case *ast.ListComprehensionExpr:
		return tail != nil && tail.Owner == nil
	case *ast.QueryExpr:
		return tail != nil && tail.Kind == ast.QueryExprEach && tail.Owner == nil
	case *ast.StructLitExpr:
		return scan.structLiteralAllocatesContainerArg(tail)
	case *ast.AllocExpr:
		return tail != nil && (tail.AutoRegion || tail.Owner == nil)
	case *ast.TupleExpr:
		if tail != nil {
			for _, elem := range tail.Elems {
				if scan.exprTailNeedsAutoRegion(elem, true) {
					return true
				}
			}
		}
	case *ast.TernaryExpr:
		return tail != nil && (scan.exprTailNeedsAutoRegion(tail.Value, true) || scan.exprTailNeedsAutoRegion(tail.Alt, true))
	case *ast.MatchExpr:
		if tail != nil {
			for _, arm := range tail.Arms {
				if scan.exprTailNeedsAutoRegion(matchArmValueTail(arm.Body), true) {
					return true
				}
			}
		}
	case *ast.ExprBlock:
		if tail != nil {
			return scan.exprBlockBodyNeedsAutoRegion(tail) || scan.exprTailNeedsAutoRegion(tail.Value, nested)
		}
	}
	return false
}

func matchArmValueTail(body []ast.Stmt) ast.Expr {
	if len(body) == 0 {
		return nil
	}
	stmt, ok := body[len(body)-1].(*ast.ExprStmt)
	if !ok || stmt == nil {
		return nil
	}
	return stmt.Expr
}

// structLiteralAllocatesContainerArg reports whether value is a struct construction
// (`Bag([], [])`) with an allocating container literal in one of its immediate arguments.
// The body-level reflection scan visits nested struct literals independently, avoiding a
// recursive rescan of the same constructor subtree for every enclosing literal.
func structLiteralAllocatesContainerArg(value ast.Expr) bool {
	var scan Scan
	return scan.structLiteralAllocatesContainerArg(value)
}

func (scan *Scan) structLiteralAllocatesContainerArg(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	lit, ok := value.(*ast.StructLitExpr)
	if !ok || lit == nil {
		return false
	}
	for _, arg := range lit.Args {
		if scan.isAllocatingLiteral(arg) {
			return true
		}
	}
	return false
}

// isAllocatingLiteral is a syntax-level approximation for whether a value is or
// contains a container literal or builder that may allocate. Tuple fields carry
// their element values, so a tuple used as a struct field can allocate through one
// of its elements.
func isAllocatingLiteral(value ast.Expr) bool {
	var scan Scan
	return scan.isAllocatingLiteral(value)
}

func (scan *Scan) isAllocatingLiteral(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	switch literal := value.(type) {
	case *ast.ListLitExpr, *ast.ListComprehensionExpr:
		return true
	case *ast.TupleExpr:
		if literal == nil {
			return false
		}
		for _, elem := range literal.Elems {
			if scan.isAllocatingLiteral(elem) {
				return true
			}
		}
	case *ast.TernaryExpr:
		return literal != nil && (scan.isAllocatingLiteral(literal.Value) || scan.isAllocatingLiteral(literal.Alt))
	case *ast.MatchExpr:
		if literal != nil {
			for _, arm := range literal.Arms {
				if scan.isAllocatingLiteral(matchArmValueTail(arm.Body)) {
					return true
				}
			}
		}
	case *ast.ExprBlock:
		return literal != nil && (scan.exprBlockBodyNeedsAutoRegion(literal) || scan.isAllocatingLiteral(literal.Value))
	}
	return InferredDArrayBuilder(value)
}

func (s *Scan) ExprTailNeedsAutoRegion(expr ast.Expr, nested bool) bool {
	return s.exprTailNeedsAutoRegion(expr, nested)
}
func IsAllocatingLiteral(expr ast.Expr) bool { return isAllocatingLiteral(expr) }
