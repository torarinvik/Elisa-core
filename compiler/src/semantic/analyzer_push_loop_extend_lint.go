package semantic

import "elisacore/src/ast"

// The push-loop-extend lint nudges the iterate-and-push idiom toward declarative resize-and-fill
// (docs/70, [[extend-comprehension-fusion]]). A loop `for x in src: dst.push(f(x))` (optionally
// guarded by a single `if cond`) grows `dst` one element at a time — a per-iteration capacity
// check plus conditional realloc that LLVM cannot hoist, so the fill stays scalar. The equivalent
// `dst.extend([f(x) for x in src])` fuses (backend) into a single presize + indexed-store fill
// (filter-free, which auto-vectorizes) or a one-shot upper-bound reserve + conditional push
// (filtered) — no intermediate darray, no repeated reallocation. Same result, strictly better
// codegen, and it makes the pretty path the fast path. A warning by default (keeps prototyping
// fluid); a hard error under -Wperf so shipped code can ban the slow shape.
func (a *Analyzer) checkPushLoopExtendable(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.findPushLoopExtendable(fn.Body)
}

func (a *Analyzer) findPushLoopExtendable(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.IterForStmt:
			a.flagPushLoopExtendable(s)
			a.findPushLoopExtendable(s.Body)
		case *ast.ForStmt:
			a.findPushLoopExtendable(s.Body)
		case *ast.WhileStmt:
			a.findPushLoopExtendable(s.Body)
		case *ast.IfStmt:
			a.findPushLoopExtendable(s.Then)
			a.findPushLoopExtendable(s.Else)
		case *ast.ScopeStmt:
			a.findPushLoopExtendable(s.Body)
		case *ast.CanStmt:
			a.findPushLoopExtendable(s.Body)
		case *ast.RegionStmt:
			a.findPushLoopExtendable(s.Body)
		case *ast.InStoreStmt:
			a.findPushLoopExtendable(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.findPushLoopExtendable(arm.Body)
			}
		}
	}
}

func (a *Analyzer) flagPushLoopExtendable(loop *ast.IterForStmt) {
	// Only the plain forward value-binding iteration `for x in src:` maps cleanly onto a
	// comprehension source. A move-drain, reverse, or pattern/where-filtered iterator changes the
	// element semantics the comprehension form cannot mirror, so leave those alone.
	if loop.MovedSource || loop.Reverse || loop.PatternFilter != nil || loop.WhereFilter != nil || loop.Filter != nil {
		return
	}
	name, ok := loop.Pattern.(*ast.MoveBindNamePattern)
	if !ok || name == nil || name.Name == "" || name.Name == "_" {
		return
	}
	// The source must be a plain named collection: it becomes the comprehension source verbatim and
	// is re-read by `.extend`, so it has to be a side-effect-free re-evaluable spelling.
	srcIdent, ok := loop.Source.(*ast.Ident)
	if !ok || srcIdent == nil {
		return
	}
	dst, value, cond, ok := a.pushLoopBodyShape(loop.Body)
	if !ok {
		return
	}
	// Self-extend (`for x in xs: xs.push(...)`) is a distinct, subtler shape (the comprehension
	// snapshots the source length); don't nudge it — the plain push loop over the same container is
	// its own hazard the iterator-invalidation checker owns.
	if dst.Name == srcIdent.Name {
		return
	}
	if !isDArrayTypeMaybeRef(a.exprTypes[dst]) {
		return
	}
	// The value must actually use the loop variable — otherwise this is a constant/side-channel fill,
	// not a map, and the comprehension rewrite would be misleading.
	if !exprReferencesName(value, name.Name) {
		return
	}
	suggestion := "[" + optimizationExprString(value) + " for " + name.Name + " in " + srcIdent.Name
	if cond != nil {
		suggestion += " if " + optimizationExprString(cond)
	}
	suggestion += "]"
	a.perfLint(loop.Pos(), "loop pushes onto %q one element per iteration; prefer the declarative fill `%s.extend(%s)`, which fuses into a single presize-and-fill (no repeated reallocation, and the filter-free form auto-vectorizes). If the accumulation is genuinely incremental (early exit, interleaved writes, stateful), keep the push loop", dst.Name, dst.Name, suggestion)
}

// pushLoopBodyShape recognizes a loop body that is exactly one `dst.push(value)`, optionally wrapped
// in a single filter `if cond: dst.push(value)` (no else). Returns the destination identifier, the
// pushed value expression, the guard condition (nil when unguarded), and ok. Anything else — extra
// statements, an else branch, a non-push call, a non-identifier receiver — yields ok=false so only
// the clean map/filter-and-append shape is nudged.
func (a *Analyzer) pushLoopBodyShape(body []ast.Stmt) (dst *ast.Ident, value ast.Expr, cond ast.Expr, ok bool) {
	if len(body) != 1 {
		return nil, nil, nil, false
	}
	switch s := body[0].(type) {
	case *ast.ExprStmt:
		dst, value, ok = pushCallShape(s.Expr)
		return dst, value, nil, ok
	case *ast.IfStmt:
		if len(s.Then) != 1 || len(s.Else) != 0 {
			return nil, nil, nil, false
		}
		inner, iok := s.Then[0].(*ast.ExprStmt)
		if !iok {
			return nil, nil, nil, false
		}
		dst, value, ok = pushCallShape(inner.Expr)
		return dst, value, s.Cond, ok
	}
	return nil, nil, nil, false
}

// pushCallShape matches `dst.push(value)` with `dst` a plain identifier and a single argument.
func pushCallShape(e ast.Expr) (*ast.Ident, ast.Expr, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || call == nil || len(call.Args) != 1 {
		return nil, nil, false
	}
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Field != "push" {
		return nil, nil, false
	}
	dst, ok := field.Object.(*ast.Ident)
	if !ok || dst == nil {
		return nil, nil, false
	}
	return dst, call.Args[0], true
}

// exprReferencesName reports whether an expression mentions the identifier `name` anywhere. Covers
// the expression shapes a comprehension value/filter can take; unknown nodes conservatively report
// true so the "value uses the loop variable" gate never spuriously suppresses a real map (a false
// positive here only means we keep the nudge, never that we drop a genuine push loop).
func exprReferencesName(e ast.Expr, name string) bool {
	switch n := e.(type) {
	case nil:
		return false
	case *ast.Ident:
		return n.Name == name
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit, *ast.NullLit, *ast.ZeroedLit:
		return false
	case *ast.ParenExpr:
		return exprReferencesName(n.Inner, name)
	case *ast.BinaryExpr:
		return exprReferencesName(n.Left, name) || exprReferencesName(n.Right, name)
	case *ast.UnaryExpr:
		return exprReferencesName(n.Operand, name)
	case *ast.MoveExpr:
		return exprReferencesName(n.Operand, name)
	case *ast.TernaryExpr:
		return exprReferencesName(n.Value, name) || exprReferencesName(n.Cond, name) || exprReferencesName(n.Alt, name)
	case *ast.IndexExpr:
		return exprReferencesName(n.Object, name) || exprReferencesName(n.Index, name)
	case *ast.FieldExpr:
		return exprReferencesName(n.Object, name)
	case *ast.CastExpr:
		return exprReferencesName(n.Operand, name)
	case *ast.CallExpr:
		if exprReferencesName(n.Func, name) {
			return true
		}
		for _, arg := range n.Args {
			if exprReferencesName(arg, name) {
				return true
			}
		}
		return false
	default:
		return true
	}
}
