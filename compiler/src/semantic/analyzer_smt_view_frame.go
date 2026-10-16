package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// sviewFrameWriteFree reports whether decl's body can never write memory, so the bytes behind an
// immutable sview parameter are the same at every point of the function. Then the parameter can be
// modeled as ONE byte array for the whole body (contents and length fixed), which is what lets a
// call-initialized local `s: usize = f(text, 0)` and a contract call `f(text, 0)` share a canonical
// call symbol (call congruence keys a view by its array term).
//
// The gate is syntactic over the whole body (statements after the proof point included):
//   - locals: immutable bindings of write-free values; MUTABLE bindings only when the declared
//     type is a scalar (integer/bool) or sview — rebinding such a slot writes no shared memory;
//   - assignments `x <- v` / `x += v` only to those mutable scalar/view locals;
//   - if/elif/else, while (condition and body write-free), return, and `can Abort.Panic:`;
//   - expressions: identifiers, literals, operators without overloads, ternaries, non-optional
//     field reads, view slices and indexing, plain integer casts, tuples, and calls to callees
//     classified write-free (classifierCalleeWriteFree / functionPureEquationShape);
//   - the function has no mutable parameters and uses no permission beyond Abort.Panic.
//
// Anything else declines (the caller keeps the conservative per-read modeling).
func (a *Analyzer) sviewFrameWriteFree(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || decl.IsLemma || len(decl.Body) == 0 {
		return false
	}
	if cached, ok := a.sviewFrameWriteFreeCache[decl]; ok {
		return cached
	}
	result := a.computeSViewFrameWriteFree(decl)
	if a.sviewFrameWriteFreeCache == nil {
		a.sviewFrameWriteFreeCache = map[*ast.FuncDecl]bool{}
	}
	a.sviewFrameWriteFreeCache[decl] = result
	return result
}

func (a *Analyzer) computeSViewFrameWriteFree(decl *ast.FuncDecl) bool {
	for _, param := range decl.Params {
		if param.Mutable || a.paramIsMutable(param) {
			return false
		}
	}
	panicOnly := func(permissions []ast.PermissionRef) bool {
		for _, permission := range permissions {
			if permission.Name != "Abort" || permission.Member != "Panic" || len(permission.TypeArgs) != 0 || len(permission.Via) != 0 {
				return false
			}
		}
		return true
	}
	if !panicOnly(decl.Permissions) {
		return false
	}
	if decl == a.currentFuncDecl && !panicOnly(a.currentFunctionUsedPermissionRefs) {
		return false
	}
	g := &sviewFrameGate{a: a, rebindable: map[string]bool{}}
	return g.stmts(decl.Body)
}

type sviewFrameGate struct {
	a *Analyzer
	// rebindable names the mutable scalar/view locals declared so far; only they may be assigned.
	rebindable map[string]bool
}

func sviewFrameRebindableType(te ast.TypeExpr) bool {
	named, ok := te.(*ast.NamedType)
	if !ok || named == nil || named.Region != "" {
		return false
	}
	switch named.Name {
	case "bool", "sview", "char":
		return true
	}
	return isIntegerConversionTargetName(named.Name)
}

func (g *sviewFrameGate) stmts(body []ast.Stmt) bool {
	for _, stmt := range body {
		if !g.stmt(stmt) {
			return false
		}
	}
	return true
}

func (g *sviewFrameGate) stmt(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return n.Value == nil || g.expr(n.Value, 0)
	case *ast.VarDeclStmt:
		if n == nil || n.Owner != nil || n.Value == nil || n.Ghost || !g.expr(n.Value, 0) {
			return false
		}
		if n.Mutable {
			if !sviewFrameRebindableType(n.Type) {
				return false
			}
			g.rebindable[n.Name] = true
		} else {
			// An immutable binding shadows any same-named rebindable local.
			delete(g.rebindable, n.Name)
		}
		return true
	case *ast.AssignStmt:
		if n == nil || n.Optional || n.AsOverlayCall != nil {
			return false
		}
		id, ok := n.Target.(*ast.Ident)
		return ok && id != nil && g.rebindable[id.Name] && g.expr(n.Value, 0)
	case *ast.AugAssignStmt:
		if n == nil || n.CollectionAppend != nil {
			return false
		}
		id, ok := n.Target.(*ast.Ident)
		return ok && id != nil && g.rebindable[id.Name] && g.expr(n.Value, 0)
	case *ast.IfStmt:
		if n == nil || !g.expr(n.Cond, 0) || !g.scoped(n.Then) || !g.scoped(n.Else) {
			return false
		}
		for _, clause := range n.Elifs {
			if !g.expr(clause.Cond, 0) || !g.scoped(clause.Body) {
				return false
			}
		}
		return true
	case *ast.WhileStmt:
		return n != nil && g.expr(n.Cond, 0) && g.scoped(n.Body)
	case *ast.ContractStmt:
		// Loop `invariant` / `decreases` clauses only read (they are checked in place).
		if n == nil {
			return false
		}
		switch n.Kind {
		case ast.ContractInvariant, ast.ContractDecreases:
			return n.Cond != nil && g.expr(n.Cond, 0)
		case ast.ContractDecreasesWild:
			return true
		}
		return false
	case *ast.AssertByStmt:
		// The proof block is verification-only; the condition is checked in place.
		return n != nil && g.expr(n.Cond, 0)
	case *ast.CanStmt:
		if n == nil || n.HandlerName != "" || len(n.HandlerArgs) != 0 || n.SuppressPermissionInference || n.As != "" || len(n.Permissions) == 0 {
			return false
		}
		for _, permission := range n.Permissions {
			if permission.Name != "Abort" || permission.Member != "Panic" || len(permission.TypeArgs) != 0 || len(permission.Via) != 0 {
				return false
			}
		}
		return g.scoped(n.Body)
	}
	return false
}

// scoped checks a nested block; its declarations go out of scope at the end.
func (g *sviewFrameGate) scoped(body []ast.Stmt) bool {
	saved := make(map[string]bool, len(g.rebindable))
	for k, v := range g.rebindable {
		saved[k] = v
	}
	ok := g.stmts(body)
	g.rebindable = saved
	return ok
}

func (g *sviewFrameGate) expr(e ast.Expr, depth int) bool {
	if e == nil || depth > 64 {
		return false
	}
	switch n := e.(type) {
	case *ast.Ident, *ast.StringLit, *ast.BoolLit, *ast.IntLit, *ast.CharLit:
		return true
	case *ast.ParenExpr:
		return g.expr(n.Inner, depth+1)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && g.expr(n.Left, depth+1) && g.expr(n.Right, depth+1)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && (n.Op == lexer.TOKEN_NOT || n.Op == lexer.TOKEN_MINUS) && g.expr(n.Operand, depth+1)
	case *ast.TernaryExpr:
		return g.expr(n.Cond, depth+1) && g.expr(n.Value, depth+1) && g.expr(n.Alt, depth+1)
	case *ast.FieldExpr:
		return !n.Safe && g.expr(n.Object, depth+1)
	case *ast.SliceExpr:
		return n.Start != nil && n.End != nil && g.expr(n.Object, depth+1) && g.expr(n.Start, depth+1) && g.expr(n.End, depth+1)
	case *ast.IndexExpr:
		return n.Index2 == nil && n.Fallback == nil && n.AsOverlayCall == nil && g.expr(n.Object, depth+1) && g.expr(n.Index, depth+1)
	case *ast.CastExpr:
		return isPureNumericValueCast(n) && g.expr(n.Operand, depth+1)
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if !g.expr(elem, depth+1) {
				return false
			}
		}
		return len(n.Elems) != 0
	case *ast.QuantifierExpr:
		return (n.In == nil || g.expr(n.In, depth+1)) && g.expr(n.Body, depth+1)
	case *ast.MembershipRangeExpr:
		return g.expr(n.Start, depth+1) && g.expr(n.End, depth+1)
	case *ast.MatchExpr:
		if n.Store != nil || !g.expr(n.Value, depth+1) {
			return false
		}
		for _, arm := range n.Arms {
			if !sviewFramePattern(arm.Pattern, 0) {
				return false
			}
			if arm.Guard != nil && !g.expr(arm.Guard, depth+1) {
				return false
			}
			if len(arm.Body) != 1 {
				return false
			}
			tail, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !g.expr(tail.Expr, depth+1) {
				return false
			}
		}
		return true
	case *ast.CallExpr:
		callee, ok := g.a.classifierFrameCallDecl(n)
		if !ok || callee == nil || !(g.a.functionPureEquationShape(callee) || g.a.classifierCalleeWriteFree(callee)) {
			return false
		}
		for _, arg := range n.Args {
			if !g.expr(arg, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// sviewFramePattern accepts patterns whose test evaluates nothing: wildcards, string/int/bool/char
// literals (never pinned: a pinned pattern evaluates its expression), literal ranges, and
// or-patterns of those.
func sviewFramePattern(pattern ast.MatchPattern, depth int) bool {
	if depth > 16 {
		return false
	}
	if pureClassifierPattern(pattern) {
		return true
	}
	switch n := pattern.(type) {
	case *ast.MatchLiteralPattern:
		if n == nil || n.Pinned {
			return false
		}
		_, ok := stripOptimizationParens(n.Value).(*ast.CharLit)
		return ok
	case *ast.MatchRangePattern:
		if n == nil {
			return false
		}
		literal := func(e ast.Expr) bool {
			switch stripOptimizationParens(e).(type) {
			case *ast.IntLit, *ast.CharLit:
				return true
			}
			return false
		}
		return literal(n.Lo) && literal(n.Hi)
	case *ast.MatchOrPattern:
		if n == nil || len(n.Options) == 0 {
			return false
		}
		for _, option := range n.Options {
			if !sviewFramePattern(option, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}
