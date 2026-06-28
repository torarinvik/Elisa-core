package ast

// PrepassCallShape returns the callee name and positional arguments as pre-analysis
// passes should see them. UFCS receiver calls are normalized to the same receiver-first
// shape as their eventual semantic rewrite: recv.f(x) => f(recv, x).
//
// This deliberately does not prove that a FieldExpr is a valid UFCS call. These early
// scans are conservative triggers; the semantic analyzer still resolves member-vs-UFCS
// precedence and reports any invalid call.
func PrepassCallShape(expr Expr) (string, []Expr, bool) {
	switch e := unwrapPrepassParen(expr).(type) {
	case *CallExpr:
		if e == nil {
			return "", nil, false
		}
		name, receiver, ok := prepassCalleeShape(e.Func)
		if !ok || name == "" {
			return "", nil, false
		}
		if receiver == nil {
			return name, e.Args, true
		}
		args := make([]Expr, 0, len(e.Args)+1)
		args = append(args, receiver)
		args = append(args, e.Args...)
		return name, args, true
	case *StructLitExpr:
		if e != nil && !e.Brace && e.Name != "" {
			return e.Name, e.Args, true
		}
	}
	return "", nil, false
}

func prepassCalleeShape(callee Expr) (name string, receiver Expr, ok bool) {
	for {
		switch c := unwrapPrepassParen(callee).(type) {
		case *SpecializeExpr:
			if c == nil {
				return "", nil, false
			}
			callee = c.Operand
		case *IndexExpr:
			if c == nil {
				return "", nil, false
			}
			callee = c.Object
		case *Ident:
			if c == nil || c.Name == "" {
				return "", nil, false
			}
			return c.Name, nil, true
		case *FieldExpr:
			if c == nil || c.Object == nil || c.Field == "" {
				return "", nil, false
			}
			return c.Field, c.Object, true
		default:
			return "", nil, false
		}
	}
}

func unwrapPrepassParen(expr Expr) Expr {
	for {
		paren, ok := expr.(*ParenExpr)
		if !ok || paren == nil {
			return expr
		}
		expr = paren.Inner
	}
}
