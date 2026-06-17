package backend

import "elisacore/src/ast"

// Auto-re-checking of in-body variable invariants (docs/90 brick 90-14).
//
// An in-body `invariant <bool-expr>` statement is checked once where it is written (emitStmtInner's
// ContractStmt case). Brick 90-14 makes it a STANDING contract: once declared, the invariant is
// re-asserted after every subsequent mutation (within its block scope) of any variable it mentions —
// so `invariant x >= 0` placed before a loop re-checks on each `x <- ...` inside the loop, exactly
// the loop-invariant idiom. This rides the same mutation envelope the analyzer uses to invalidate
// range facts (docs/90 brick 90-11): the three assignment forms.
//
// Soundness/discipline: like every contract this is DEBUG-gated (registered only when the in-place
// check is itself emitted) and never drives codegen decisions — a re-check just re-evaluates the
// real condition, so it can only trap on a genuine violation, never falsely on valid code. A missed
// node kind in the free-ident walk under-checks (a real violation might go unre-checked) but never
// over-traps; under-checking a debug aid is acceptable, a false trap is not. The invariant is scoped
// to the block it was declared in (truncated on block exit), so a re-check never re-evaluates a
// condition whose variables have left scope.

// activeInvariant is a standing in-body invariant: its condition plus the set of identifier names it
// reads, so a later assignment to one of those names triggers a re-check.
type activeInvariant struct {
	cond ast.Expr
	vars map[string]bool
}

// registerActiveInvariant records an in-body invariant as standing for the remainder of its block.
func (s *functionState) registerActiveInvariant(cond ast.Expr) {
	vars := map[string]bool{}
	collectInvariantIdentNames(cond, vars)
	if len(vars) == 0 {
		return
	}
	s.activeInvariants = append(s.activeInvariants, activeInvariant{cond: cond, vars: vars})
}

// recheckInvariantsAfter re-asserts every standing invariant whose variable set includes the root
// identifier mutated by the just-emitted statement. A no-op unless the statement is an assignment
// form and there is at least one standing invariant.
func (s *functionState) recheckInvariantsAfter(stmt ast.Stmt) error {
	if len(s.activeInvariants) == 0 || s.currentBlockTerminated() {
		return nil
	}
	name, ok := assignTargetRootIdent(stmt)
	if !ok {
		return nil
	}
	for _, inv := range s.activeInvariants {
		if inv.vars[name] {
			if err := s.emitContractCheck(inv.cond, "invariant failed"); err != nil {
				return err
			}
		}
	}
	return nil
}

// assignTargetRootIdent returns the root identifier name written by an assignment statement, for the
// three mutating forms (the same envelope as analyzer range-fact invalidation, docs/90 brick 90-11).
func assignTargetRootIdent(stmt ast.Stmt) (string, bool) {
	var target ast.Expr
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		target = n.Target
	case *ast.AugAssignStmt:
		target = n.Target
	case *ast.AsRefAssignStmt:
		target = n.Target
	default:
		return "", false
	}
	return exprRootIdentName(target)
}

// exprRootIdentName peels field/index/deref/paren wrappers to the base identifier of an lvalue.
func exprRootIdentName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.ParenExpr:
		return exprRootIdentName(n.Inner)
	case *ast.FieldExpr:
		return exprRootIdentName(n.Object)
	case *ast.IndexExpr:
		return exprRootIdentName(n.Object)
	case *ast.UnaryExpr:
		return exprRootIdentName(n.Operand)
	default:
		return "", false
	}
}

// collectInvariantIdentNames gathers the identifier names an invariant condition reads, over the
// expression fragment in-body invariants use. An unrecognized node simply contributes nothing (its
// subtree is conservatively unwalked) — under-checking, never over-trapping.
func collectInvariantIdentNames(expr ast.Expr, out map[string]bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		out[n.Name] = true
	case *ast.ParenExpr:
		collectInvariantIdentNames(n.Inner, out)
	case *ast.UnaryExpr:
		collectInvariantIdentNames(n.Operand, out)
	case *ast.BinaryExpr:
		collectInvariantIdentNames(n.Left, out)
		collectInvariantIdentNames(n.Right, out)
	case *ast.FieldExpr:
		collectInvariantIdentNames(n.Object, out)
	case *ast.IndexExpr:
		collectInvariantIdentNames(n.Object, out)
		collectInvariantIdentNames(n.Index, out)
	case *ast.CallExpr:
		collectInvariantIdentNames(n.Func, out)
		for _, arg := range n.Args {
			collectInvariantIdentNames(arg, out)
		}
	}
}
