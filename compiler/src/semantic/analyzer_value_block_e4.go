package semantic

import (
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

func (a *Analyzer) checkValueBlockOuterMutation(expr *ast.ExprBlock) {
	if expr == nil {
		return
	}
	local := map[string]bool{}
	collectBlockBoundNames(expr.Stmts, local)
	a.walkValueBlockMutations(expr.Stmts, local)
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
