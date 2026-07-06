package semantic

import (
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/119 §2.2 / §6.2 — E4: a value block (an `ExprBlock`: the RHS of a bare block
// binding, a loop-expression body, an `if`/`match` value branch) is *pure over outer
// state*. It yields new values; it may not write back into a binding that lives
// outside the block. Threading updates to outer mutables is `rebind`'s job (§5), and
// `|capture|` (§6) is sugar over that. This purity is what makes block/loop
// expressions safe to read locally — the header/binding site tells you everything the
// block can change.
//
// This pass covers the *direct* mutation forms: `x <- …`, `x.f <- …`, `x[i] <- …`,
// aug-assign, and `x as& <- …`, where the assignment root is a binding from an
// enclosing scope. The mutating-CALL half of E4 (passing an outer var as a
// `mutable T&` argument) needs call-signature reasoning and is left for the capture
// batch; direct writes are the structural core.
//
// Soundness bias: a name bound ANYWHERE inside the block subtree is treated as local
// (so we never flag a write to the block's own accumulators/temps). Missing a violation
// is merely the pre-119 status quo; a false positive would break working code, so the
// pass errs toward permissiveness where the two conflict.

// checkValueBlockOuterMutation runs the direct-write E4 pass and returns the block's
// "allowed" name set (block-local bindings + licensed captures). analyzeExprBlock
// pushes that set for the duration of body analysis so the mutating-CALL half of E4
// (a call passing an outer var as `mutable T&`, incl. a mutating method receiver) can
// consult it from the call-analysis path.
func (a *Analyzer) checkValueBlockOuterMutation(expr *ast.ExprBlock) map[string]bool {
	if expr == nil {
		return nil
	}
	local := map[string]bool{}
	collectBlockBoundNames(expr.Stmts, local)
	// docs/119 §6: a captured outer binding IS licensed to be updated here — that is the
	// whole point of a capture. Each capture must name an existing MUTABLE outer binding
	// (E10 immutable / E11 undefined); then treat it as local for E4.
	for _, c := range expr.Captures {
		sym, found := a.currentScope.Lookup(c)
		switch {
		case !found || sym == nil:
			a.errorf(expr.Pos(), "capture %q names no binding in scope (docs/119 E11)", c)
		case !sym.Mutable && !isMutableRefBinding(sym):
			a.errorf(expr.Pos(), "capture %q is not a `mutable` binding; only mutables can be threaded through a value block (docs/119 E10)", c)
		}
		local[c] = true
	}
	// A nested value block (an if/match value branch inside a captured block) inherits
	// the enclosing block's licensed names: a `|capture|` on the outer binding site
	// licenses the whole RHS subtree, including its branch blocks (docs/119 §6).
	if len(a.valueBlockAllowed) > 0 {
		for name := range a.valueBlockAllowed[len(a.valueBlockAllowed)-1] {
			local[name] = true
		}
	}
	a.walkValueBlockMutations(expr.Stmts, local)
	return local
}

// isMutableRefBinding reports whether a symbol's type is a `mutable T&` reference — a
// `mutable Parser&` parameter binds an immutable reference cell (sym.Mutable is false, the
// binding can't be rebound), but mutating THROUGH it is exactly the capability a capture
// licenses (docs/119 §6). So such a binding is a legal capture target for E10.
func isMutableRefBinding(sym *Symbol) bool {
	if ref, ok := sym.Type.(*RefType); ok {
		return ref.Mutable
	}
	return false
}

// checkValueBlockMutatingCall is the mutating-CALL half of E4 (docs/119 §6.2): inside a
// value block, passing a non-local, non-captured outer binding as a `mutable T&`
// argument — including as the receiver of a mutating method (`outer.push(x)`) — is the
// hidden-write shape Elian complains about, and is rejected. Called from the
// call-analysis arg loop, where param mutability is already resolved. `arg` is the
// argument expression bound to a mutable-ref parameter.
func (a *Analyzer) checkValueBlockMutatingCall(arg ast.Expr) {
	if len(a.valueBlockAllowed) == 0 {
		return
	}
	allowed := a.valueBlockAllowed[len(a.valueBlockAllowed)-1]
	name, ok := rootIdentName(arg)
	if !ok || name == "" || name == "_" || allowed[name] {
		return
	}
	// Compiler-synthesized bindings (`__region_auto`, `__rg_*` region threading, …)
	// are not user state — region inference threads them into allocating calls
	// inside value blocks, and user code cannot name or capture a `__` binding.
	// E4 is about user-visible outer mutation, so these are exempt.
	if strings.HasPrefix(name, "__") {
		return
	}
	sym, found := a.currentScope.Lookup(name)
	if !found || sym == nil {
		return
	}
	switch sym.Kind {
	case SymbolLocal, SymbolParam:
	default:
		return
	}
	a.errorf(arg.Pos(), "value block may not mutate the outer binding %q through a call (docs/119 E4); pass it by value, capture it in the header (`|%s|`), or thread the update with `rebind`", name, name)
}

// exprBlockPureOverOuter reports whether a value block is provably pure over OUTER
// state (docs/119 §6.4): with an empty capture set, E4 has already rejected every
// direct write and every mutating call to a non-local binding, and there is no
// captured binding licensed for mutation — so the block cannot change any state that
// outlives it. This structural fact is what the WP engine, loop-invariant inference,
// and the docs/86/118 termination provers want: a pure-over-outer-state loop body means
// the measure can only move through the loop's own header accumulators, and any fact
// about an outer place survives the block unconditionally. (The per-variable fact
// model already preserves untouched-variable facts; this makes the guarantee explicit
// and queryable rather than incidental.)
func exprBlockPureOverOuter(expr *ast.ExprBlock) bool {
	return expr != nil && len(expr.Captures) == 0
}

// checkValueBlockMutatingBuiltinMethod is the builtin-collection-method arm of the
// mutating-call E4 (docs/119 §6.2): a mutating builtin (`xs.push(v)`, `d.clear()`,
// `s.add(x)`) models its receiver as a VALUE, not a `mutable T&` arg, so it slips past
// the ref-arg hook — mirror checkFrameForMutatingBuiltinMethod to catch it.
func (a *Analyzer) checkValueBlockMutatingBuiltinMethod(expr *ast.CallExpr) {
	if len(a.valueBlockAllowed) == 0 {
		return
	}
	if recv, ok := a.mutatingBuiltinMethodReceiver(expr); ok {
		a.checkValueBlockMutatingCall(recv)
	}
}

// collectBlockBoundNames gathers every name introduced by a binding within the block
// subtree (its own decls, loop variables, destructuring patterns, and those of nested
// control-flow bodies). Nested value blocks (ExprBlock expressions) run their own E4
// pass; their bindings are still swept up here so a write to them from an inner
// statement is never mis-flagged.
func collectBlockBoundNames(stmts []ast.Stmt, out map[string]bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.VarDeclStmt:
			out[n.Name] = true
		case *ast.TupleBindStmt:
			for _, nm := range n.Names {
				out[nm.Name] = true
			}
		case *ast.ForStmt:
			if n.Name != "" {
				out[n.Name] = true
			}
			collectBlockBoundNames(n.Body, out)
		case *ast.IterForStmt:
			for _, nm := range parallelForMoveBindNames(n.Pattern) {
				out[nm] = true
			}
			collectBlockBoundNames(n.Body, out)
		case *ast.WhileStmt:
			collectBlockBoundNames(n.Body, out)
		case *ast.IfStmt:
			collectBlockBoundNames(n.Then, out)
			for _, e := range n.Elifs {
				collectBlockBoundNames(e.Body, out)
			}
			collectBlockBoundNames(n.Else, out)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				for _, nm := range parallelForMatchArmPatternNames(arm.Pattern) {
					out[nm] = true
				}
				collectBlockBoundNames(arm.Body, out)
			}
		}
	}
}

// walkValueBlockMutations reports E4 for any direct assignment whose root binding is
// not block-local. It descends into nested control-flow bodies (a loop body writing an
// outer var is still an escape from THIS block) but not into expression operands —
// nested value blocks handle themselves.
func (a *Analyzer) walkValueBlockMutations(stmts []ast.Stmt, local map[string]bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.AssignStmt:
			a.reportE4IfOuter(n.Target, local, n.Pos())
		case *ast.AugAssignStmt:
			a.reportE4IfOuter(n.Target, local, n.Pos())
		case *ast.AsRefAssignStmt:
			a.reportE4IfOuter(n.Target, local, n.Pos())
		case *ast.ForStmt:
			a.walkValueBlockMutations(n.Body, local)
		case *ast.IterForStmt:
			a.walkValueBlockMutations(n.Body, local)
		case *ast.WhileStmt:
			a.walkValueBlockMutations(n.Body, local)
		case *ast.IfStmt:
			a.walkValueBlockMutations(n.Then, local)
			for _, e := range n.Elifs {
				a.walkValueBlockMutations(e.Body, local)
			}
			a.walkValueBlockMutations(n.Else, local)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				a.walkValueBlockMutations(arm.Body, local)
			}
		}
	}
}

func (a *Analyzer) reportE4IfOuter(target ast.Expr, local map[string]bool, pos lexer.Pos) {
	name, ok := rootIdentName(target)
	if !ok || name == "" || name == "_" || local[name] {
		return
	}
	// The name is not block-local. If it resolves to a binding from an enclosing
	// scope, this write escapes the value block — E4.
	sym, found := a.currentScope.Lookup(name)
	if !found || sym == nil {
		return
	}
	switch sym.Kind {
	case SymbolLocal, SymbolParam:
	default:
		return // globals/consts/funcs are not the "outer mutable state" this guards
	}
	a.errorf(pos, "value block may not mutate the outer binding %q (docs/119 E4); a block/loop expression yields new values — use `rebind` to thread the update back", name)
}
