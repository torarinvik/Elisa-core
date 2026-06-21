package ast

// CloneStmt returns a deep copy of a statement with fresh node identities, following the same
// "return nil = not clonable, caller bails" contract as CloneExpr. See CloneStmtSubst for the
// substituting variant; this is the plain (no-substitution) form.
func CloneStmt(stmt Stmt) Stmt {
	return CloneStmtSubst(stmt, nil)
}

// CloneBlock deep-copies a statement slice, returning ok=false if any statement is not clonable.
func CloneBlock(stmts []Stmt) ([]Stmt, bool) {
	return CloneBlockSubst(stmts, nil)
}

// CloneStmtSubst is CloneStmt with simultaneous capture-free identifier substitution (see
// CloneExprSubst): any *Ident whose name is a key in subst is replaced by a fresh clone of the bound
// expression. It covers the statement shapes that appear in protocol DEFAULT METHOD bodies (return /
// if / while / expr / let / assign / match-free control flow); any statement outside that set yields
// nil so the caller can fall back to requiring an explicit method implementation.
//
// It exists so a single user-written default-method body can be instantiated independently into each
// conforming type's impl, with the protocol's `Self` rewritten to that type — sharing one node
// pointer across impls would let later annotation/affine passes observe one node in several scopes.
func CloneStmtSubst(stmt Stmt, subst map[string]Expr) Stmt {
	switch n := stmt.(type) {
	case nil:
		return nil
	case *ReturnStmt:
		value := CloneExprSubst(n.Value, subst)
		if n.Value != nil && value == nil {
			return nil
		}
		return &ReturnStmt{Position: n.Position, Value: value}
	case *ExprStmt:
		expr := CloneExprSubst(n.Expr, subst)
		if n.Expr != nil && expr == nil {
			return nil
		}
		return &ExprStmt{Position: n.Position, Expr: expr}
	case *BreakStmt:
		return &BreakStmt{Position: n.Position}
	case *ContinueStmt:
		return &ContinueStmt{Position: n.Position}
	case *PassStmt:
		return &PassStmt{Position: n.Position}
	case *VarDeclStmt:
		value := CloneExprSubst(n.Value, subst)
		owner := CloneExprSubst(n.Owner, subst)
		if (n.Value != nil && value == nil) || (n.Owner != nil && owner == nil) {
			return nil
		}
		return &VarDeclStmt{Position: n.Position, Name: n.Name, Mutable: n.Mutable, Type: n.Type, Value: value, Owner: owner, Ghost: n.Ghost}
	case *AssignStmt:
		target := CloneExprSubst(n.Target, subst)
		value := CloneExprSubst(n.Value, subst)
		if (n.Target != nil && target == nil) || (n.Value != nil && value == nil) {
			return nil
		}
		return &AssignStmt{Position: n.Position, Target: target, Value: value, Optional: n.Optional, FastMath: n.FastMath}
	case *AugAssignStmt:
		target := CloneExprSubst(n.Target, subst)
		value := CloneExprSubst(n.Value, subst)
		if (n.Target != nil && target == nil) || (n.Value != nil && value == nil) {
			return nil
		}
		return &AugAssignStmt{Position: n.Position, Op: n.Op, Target: target, Value: value}
	case *IfStmt:
		cond := CloneExprSubst(n.Cond, subst)
		if n.Cond != nil && cond == nil {
			return nil
		}
		then, ok := CloneBlockSubst(n.Then, subst)
		if !ok {
			return nil
		}
		els, ok := CloneBlockSubst(n.Else, subst)
		if !ok {
			return nil
		}
		var elifs []ElifClause
		if len(n.Elifs) != 0 {
			elifs = make([]ElifClause, 0, len(n.Elifs))
			for _, e := range n.Elifs {
				ec := CloneExprSubst(e.Cond, subst)
				if e.Cond != nil && ec == nil {
					return nil
				}
				body, ok := CloneBlockSubst(e.Body, subst)
				if !ok {
					return nil
				}
				elifs = append(elifs, ElifClause{Position: e.Position, Hint: e.Hint, Cond: ec, Body: body})
			}
		}
		return &IfStmt{Position: n.Position, Hint: n.Hint, Cond: cond, Then: then, Elifs: elifs, Else: els}
	case *WhileStmt:
		cond := CloneExprSubst(n.Cond, subst)
		if n.Cond != nil && cond == nil {
			return nil
		}
		body, ok := CloneBlockSubst(n.Body, subst)
		if !ok {
			return nil
		}
		return &WhileStmt{Position: n.Position, Hint: n.Hint, Cond: cond, Body: body}
	default:
		return nil
	}
}

// CloneBlockSubst deep-copies a statement slice with identifier substitution, returning ok=false if
// any statement is not clonable. An empty input is a clonable empty block.
func CloneBlockSubst(stmts []Stmt, subst map[string]Expr) ([]Stmt, bool) {
	if len(stmts) == 0 {
		return nil, true
	}
	out := make([]Stmt, 0, len(stmts))
	for _, s := range stmts {
		c := CloneStmtSubst(s, subst)
		if s != nil && c == nil {
			return nil, false
		}
		out = append(out, c)
	}
	return out, true
}
