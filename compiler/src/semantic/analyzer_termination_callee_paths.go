package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// changedPath is a rebased `changes` frame path: a root name plus the field prefix it covers. An empty
// Fields covers the WHOLE root (every place under it is changed).
type changedPath struct {
	root   string
	fields []string
}

// consumerChangedPaths returns the consumer callee's `changes` frame paths rebased onto the caller's
// argument roots (a callee `changes q.pos` for `advance(p)` becomes root `p`, fields `[pos]`). Only paths
// rooted at a measured root are kept.
func (a *Analyzer) consumerChangedPaths(consumer *ast.CallExpr, roots map[string]bool) []changedPath {
	decl, ok := a.resolveDirectCallFuncDecl(consumer)
	if !ok || decl == nil || len(decl.Changes) == 0 {
		return nil
	}
	rebase := map[string]string{}
	for i, param := range decl.Params {
		if i >= len(consumer.Args) || consumer.Args[i] == nil {
			continue
		}
		arg := stripOptimizationParens(consumer.Args[i])
		if u, isAddr := arg.(*ast.UnaryExpr); isAddr && u.Op == lexer.TOKEN_AMPERSAND {
			arg = stripOptimizationParens(u.Operand)
		}
		if id, ok := arg.(*ast.Ident); ok {
			rebase[param.Name] = id.Name
		}
	}
	var out []changedPath
	for _, cp := range decl.Changes {
		root := cp.Root
		if r, ok := rebase[cp.Root]; ok {
			root = r
		}
		if roots[root] {
			out = append(out, changedPath{root: root, fields: cp.Fields})
		}
	}
	return out
}

// placeIsChanged reports whether a measured field place `root.f1.f2…` is covered by any changed path —
// same root and a covered-field prefix (an empty path Fields covers the whole root).
func placeIsChanged(fe *ast.FieldExpr, changed []changedPath) bool {
	root, fields, ok := fieldPlacePath(fe)
	if !ok {
		return false
	}
	for _, cp := range changed {
		if cp.root != root {
			continue
		}
		if len(cp.fields) == 0 {
			return true // whole root changes
		}
		if isFieldPrefix(cp.fields, fields) {
			return true
		}
	}
	return false
}

// fieldPlacePath decomposes `root.f1.f2…` into its root name and ordered field chain.
func fieldPlacePath(fe *ast.FieldExpr) (string, []string, bool) {
	var fields []string
	cur := ast.Expr(fe)
	for {
		switch n := stripOptimizationParens(cur).(type) {
		case *ast.FieldExpr:
			fields = append([]string{n.Field}, fields...)
			cur = n.Object
		case *ast.Ident:
			return n.Name, fields, true
		default:
			return "", nil, false
		}
	}
}

// isFieldPrefix reports whether `prefix` is a leading sub-chain of `full`.
func isFieldPrefix(prefix, full []string) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i, f := range prefix {
		if full[i] != f {
			return false
		}
	}
	return true
}

// measureHasChangedPlace reports whether the measure mentions at least one CHANGED measured place.
func (a *Analyzer) measureHasChangedPlace(measure ast.Expr, roots map[string]bool, changed []changedPath) bool {
	has := false
	a.walkStaticExpr(measure, func(e ast.Expr) bool {
		if fe, ok := e.(*ast.FieldExpr); ok && fe != nil && a.placeIsMeasured(fe, roots) && placeIsChanged(fe, changed) {
			has = true
			return true
		}
		return false
	})
	return has
}

// placeIsMeasured reports whether a field expr is a scalar place rooted at a measured root.
func (a *Analyzer) placeIsMeasured(fe *ast.FieldExpr, roots map[string]bool) bool {
	id, ok := stripOptimizationParens(fe.Object).(*ast.Ident)
	return ok && roots[id.Name]
}

// rewriteChangedPlacesToEntry clones `expr` and rewrites every CHANGED measured place `root.field` to its
// entry twin (`__entry_root.field`, field type stamped), leaving unchanged places as their single current
// symbol. This yields the entry-state view of a measure/guard/requires.
func (a *Analyzer) rewriteChangedPlacesToEntry(expr ast.Expr, roots map[string]bool, changed []changedPath) ast.Expr {
	return a.rewritePlaces(ast.CloneExpr(expr), roots, changed)
}

// rewriteEnsureOldToEntry clones a consumer `ensure` and rewrites each `old(x)` to the entry-twin view of
// x (the pre-consumer value), leaving BARE places as the current (post-consumer) value.
func (a *Analyzer) rewriteEnsureOldToEntry(ensure ast.Expr, roots map[string]bool, changed []changedPath) ast.Expr {
	clone := ast.CloneExpr(ensure)
	return a.rewriteOldArgs(clone, roots, changed)
}

// rewriteOldArgs walks an expression and replaces each `old(x)` call with the entry-twin rewrite of x;
// non-old sub-expressions are left as their current view (recursing to find nested `old(...)`).
func (a *Analyzer) rewriteOldArgs(expr ast.Expr, roots map[string]bool, changed []changedPath) ast.Expr {
	switch n := expr.(type) {
	case *ast.CallExpr:
		if ast.IsOldCall(n) && len(n.Args) == 1 {
			return a.rewritePlaces(n.Args[0], roots, changed)
		}
	case *ast.BinaryExpr:
		n.Left = a.rewriteOldArgs(n.Left, roots, changed)
		n.Right = a.rewriteOldArgs(n.Right, roots, changed)
		return n
	case *ast.UnaryExpr:
		n.Operand = a.rewriteOldArgs(n.Operand, roots, changed)
		return n
	case *ast.ParenExpr:
		n.Inner = a.rewriteOldArgs(n.Inner, roots, changed)
		return n
	case *ast.CastExpr:
		n.Operand = a.rewriteOldArgs(n.Operand, roots, changed)
		return n
	}
	return expr
}

// rewritePlaces rewrites CHANGED measured places to their entry twin in place (the argument is already a
// clone).
func (a *Analyzer) rewritePlaces(expr ast.Expr, roots map[string]bool, changed []changedPath) ast.Expr {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		if a.placeIsMeasured(n, roots) && placeIsChanged(n, changed) {
			return a.entryTwin(n)
		}
		return n
	case *ast.BinaryExpr:
		n.Left = a.rewritePlaces(n.Left, roots, changed)
		n.Right = a.rewritePlaces(n.Right, roots, changed)
		return n
	case *ast.UnaryExpr:
		n.Operand = a.rewritePlaces(n.Operand, roots, changed)
		return n
	case *ast.ParenExpr:
		n.Inner = a.rewritePlaces(n.Inner, roots, changed)
		return n
	case *ast.CastExpr:
		n.Operand = a.rewritePlaces(n.Operand, roots, changed)
		return n
	}
	return expr
}

// entryTwin builds the entry-value twin of a measured place `root.field` — the SAME field chain rooted at
// a fresh `__entry_<root>` ident, with the field type stamped so the SMT translator can size the symbol
// without a scope lookup on the synthetic root.
func (a *Analyzer) entryTwin(fe *ast.FieldExpr) ast.Expr {
	ft := a.fieldTypeOfPlace(fe)
	obj := renameFieldRoot(fe.Object)
	twin := &ast.FieldExpr{Position: fe.Position, Object: obj, Field: fe.Field, Safe: fe.Safe}
	if ft != nil {
		a.exprTypes[twin] = ft
	}
	return twin
}

// renameFieldRoot returns a copy of a field-object expression with the bottom identifier renamed to its
// entry-twin name (`__entry_<name>`).
func renameFieldRoot(obj ast.Expr) ast.Expr {
	switch n := stripOptimizationParens(obj).(type) {
	case *ast.Ident:
		return &ast.Ident{Position: n.Position, Name: "__entry_" + n.Name}
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: n.Position, Object: renameFieldRoot(n.Object), Field: n.Field, Safe: n.Safe}
	}
	return obj
}

// fieldTypeOfPlace resolves a measured place's scalar field type (analyzed exprTypes, else the struct's
// declared field type for a synthetic/cloned node).
func (a *Analyzer) fieldTypeOfPlace(fe *ast.FieldExpr) Type {
	if t := a.exprType(fe); t != nil {
		return t
	}
	if t, ok := a.fieldReadResolvedType(fe); ok {
		return t
	}
	return nil
}

// calleeRequiresRebased returns the consumer callee's `requires` clauses rebased onto the caller's
// argument roots (entry-time facts guaranteed to hold at the call site).
func (a *Analyzer) calleeRequiresRebased(consumer *ast.CallExpr) []ast.Expr {
	decl, ok := a.resolveDirectCallFuncDecl(consumer)
	if !ok || decl == nil || len(decl.Requires) == 0 {
		return nil
	}
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(consumer.Args) || consumer.Args[i] == nil {
			continue
		}
		arg := stripOptimizationParens(consumer.Args[i])
		if u, isAddr := arg.(*ast.UnaryExpr); isAddr && u.Op == lexer.TOKEN_AMPERSAND {
			arg = stripOptimizationParens(u.Operand)
		}
		subst[param.Name] = arg
	}
	var out []ast.Expr
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		if rebased, ok := substituteLemmaEnsure(req, subst); ok {
			out = append(out, rebased)
		}
	}
	return out
}

// stmtContainsCall reports whether the target call node appears anywhere within a statement.
func (a *Analyzer) stmtContainsCall(stmt ast.Stmt, target *ast.CallExpr) bool {
	found := false
	a.walkStaticStmt(stmt, func(e ast.Expr) bool {
		if c, ok := e.(*ast.CallExpr); ok && c == target {
			found = true
			return true
		}
		return false
	})
	return found
}
