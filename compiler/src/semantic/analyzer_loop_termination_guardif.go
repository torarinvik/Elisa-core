package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// GUARD-`if` modeling for the loop-TERMINATION prover (highest-risk, TRUSTED code).
//
// This file extends the loop-termination prover from straight-line bodies to bodies containing
// `if`/`elif`/`else`, by modeling the body as a set of MUTUALLY-EXCLUSIVE PATHS. Each path carries a
// path-condition φ (the conjunction of guard conditions taken to reach it, with un-taken branches
// negated) and a scalar substitution σ (the net effect of the straight-line assignments along that
// path). The SOUND termination rule is:
//
//	for EVERY non-exiting path (φ, σ):  cond ∧ invs ∧ φ ⊢ measure > measure[σ] ∧ measure >= 0
//
// ALL paths must discharge. A path that EXITS the loop (ends in `break` or `return`) carries NO
// decrease obligation — the loop terminates on that path. EVERY other path owes the decrease,
// INCLUDING the implicit no-op `else` of an `if` with no `else` (σ = identity under ¬cond): this is
// the trap the rule must defeat — "progress only in the `if` branch" must be REJECTED because the
// fall-through path does not decrease.
//
// SOUNDNESS DOMINATES. When in ANY doubt this code returns "not captured" → the caller falls back to
// the lint / runtime progress backstop (a safe rejection). Specifically it REJECTS (declines):
//   - any `continue` anywhere in the body (its mid-body skip complicates σ — conservative choice);
//   - any guard condition it cannot translate to a sound path-condition expression;
//   - any branch whose straight-line content fails the EXISTING capture rules (calls, .push,
//     field-target writes, double-stores, non-arith RHS, …);
//   - a body with more than maxLoopBodyPaths paths or nesting deeper than maxLoopGuardDepth.
//
// This is termination-only: the invariant-preservation path (captureLoopBodyEffectMode forTermination=false)
// is never reached from here.

const (
	// maxLoopBodyPaths bounds the enumerated path count; exceeding it is a safe rejection.
	maxLoopBodyPaths = 16
	// maxLoopGuardDepth bounds `if` nesting depth; exceeding it is a safe rejection.
	maxLoopGuardDepth = 6
)

// loopBodyPath is one mutually-exclusive execution path through a loop body: the guard conditions that
// must hold to reach it (conds, implicitly conjoined), the net scalar substitution along it (subst),
// and whether the path EXITS the loop (break/return) — an exiting path carries no decrease obligation.
type loopBodyPath struct {
	conds []ast.Expr
	subst map[string]ast.Expr
	exits bool
}

// pathFrag is a partial execution path accumulated while walking a statement list: the guard
// conditions taken so far, the straight-line statements accumulated so far (to be run through the
// existing capture for σ), and whether the path has already EXITED the loop (break/return).
type pathFrag struct {
	conds  []ast.Expr
	stmts  []ast.Stmt
	exited bool
}

// enumPaths recursively enumerates the execution paths through a statement list. It threads a frontier
// of open fragments (each fragment is one in-progress path). A straight-line statement is appended to
// every non-exited fragment; an `if`/`elif`/`else` forks each open fragment by branch and recursively
// enumerates the (possibly control-flow-bearing) branch body; `break`/`return` mark the fragment
// exited; `continue` or any unmodelable statement → REJECT (ok=false). depth bounds `if` nesting.
func (a *Analyzer) enumPaths(stmts []ast.Stmt, depth int) ([]pathFrag, bool) {
	if depth > maxLoopGuardDepth {
		return nil, false
	}
	frontier := []pathFrag{{}}
	for _, s := range stmts {
		if len(frontier) > maxLoopBodyPaths {
			return nil, false
		}
		switch n := s.(type) {
		case *ast.IfStmt:
			branches, ok := a.ifBranchGuards(n)
			if !ok {
				return nil, false
			}
			next := make([]pathFrag, 0, len(frontier))
			for _, p := range frontier {
				if p.exited {
					next = append(next, p) // dead code after an exit on this path
					continue
				}
				for _, br := range branches {
					// Recursively enumerate the branch body (which may itself contain if/break/return).
					subFrags, ok := a.enumPaths(br.body, depth+1)
					if !ok {
						return nil, false
					}
					for _, sf := range subFrags {
						np := pathFrag{
							conds:  concatExprs(p.conds, br.cond, sf.conds),
							stmts:  concatStmts(p.stmts, sf.stmts),
							exited: sf.exited,
						}
						next = append(next, np)
						if len(next) > maxLoopBodyPaths {
							return nil, false
						}
					}
				}
			}
			frontier = next
		case *ast.BreakStmt, *ast.ReturnStmt:
			for i := range frontier {
				if !frontier[i].exited {
					frontier[i].exited = true
				}
			}
		case *ast.ContinueStmt:
			return nil, false // conservative: reject any `continue` in this increment
		case *ast.ContractStmt:
			return nil, false // a non-leading contract mid-body is not modeled here
		default:
			for i := range frontier {
				if !frontier[i].exited {
					frontier[i].stmts = append(frontier[i].stmts, s)
				}
			}
		}
	}
	return frontier, true
}

func concatExprs(parts ...[]ast.Expr) []ast.Expr {
	var out []ast.Expr
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func concatStmts(parts ...[]ast.Stmt) []ast.Stmt {
	var out []ast.Stmt
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// enumerateLoopBodyPaths models a loop body (which may contain `if`/`elif`/`else`, `break`, `return`)
// as a set of mutually-exclusive paths. It returns ok=false (REJECT — caller falls back) for any body
// it cannot soundly account for: a `continue`, an untranslatable guard, a branch that violates the
// existing straight-line capture rules, or a path-count / nesting blowup. Each finalized non-exit path
// runs its accumulated straight-line statements through the EXISTING capture
// (captureLoopBodyEffectForTermination) to produce σ; an empty path (the implicit no-op else) gets the
// identity σ; an exiting path (break/return) needs no σ.
func (a *Analyzer) enumerateLoopBodyPaths(body []ast.Stmt) ([]loopBodyPath, bool) {
	// Strip the leading contract clauses (invariant/decreases) — they are specifications, not effects.
	stmts := stripLeadingContracts(body)
	frags, ok := a.enumPaths(stmts, 0)
	if !ok {
		return nil, false
	}
	if len(frags) == 0 || len(frags) > maxLoopBodyPaths {
		return nil, false
	}
	out := make([]loopBodyPath, 0, len(frags))
	for _, p := range frags {
		path := loopBodyPath{conds: p.conds, exits: p.exited}
		if p.exited {
			// Exiting path: no decrease obligation, so no σ is needed. We still require that the
			// straight-line prefix BEFORE the exit is itself modelable? No — once the loop exits the
			// measure is irrelevant. But an UNMODELABLE prefix (e.g. a call) before a break is harmless to
			// termination of THIS loop. We conservatively still capture it to ensure there is no hidden
			// non-exit fall-through; capture failure on an exiting path does not block termination, so we
			// skip capture entirely here.
			out = append(out, path)
			continue
		}
		if len(p.stmts) == 0 {
			// Implicit no-op else (or an empty branch): σ = identity (∅). This is a LEGITIMATE,
			// non-exiting path that makes no progress — it MUST be modeled (and will fail the decrease
			// proof), defeating "progress only in the `if` branch".
			path.subst = map[string]ast.Expr{}
			out = append(out, path)
			continue
		}
		subst, ok := a.captureLoopBodyEffectForTermination(wrapStmtsForCapture(p.stmts))
		if !ok {
			return nil, false
		}
		path.subst = subst
		out = append(out, path)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// branchGuard is one branch of an `if` chain: the conditions that must hold to take it (the branch's
// own condition plus the negation of all earlier conditions) and its body statements.
type branchGuard struct {
	cond []ast.Expr
	body []ast.Stmt
}

// ifBranchGuards expands an `if cond: Then [elif ci: Bi]… [else: E]` into its mutually-exclusive
// branches, INCLUDING the implicit no-op fall-through branch (empty body) under the negation of every
// condition when there is NO `else`. Each guard condition must be translatable to a sound negation;
// if a condition cannot be soundly negated, the whole modeling REJECTS (dropping a path condition, or
// admitting an un-negatable one, would be unsound).
func (a *Analyzer) ifBranchGuards(n *ast.IfStmt) ([]branchGuard, bool) {
	if n == nil || n.Cond == nil {
		return nil, false
	}
	var negatedPrior []ast.Expr
	var out []branchGuard

	addBranch := func(cond ast.Expr, body []ast.Stmt) bool {
		neg, ok := negateCond(cond)
		if !ok {
			return false
		}
		conds := append(append([]ast.Expr{}, negatedPrior...), ast.CloneExpr(cond))
		out = append(out, branchGuard{cond: conds, body: body})
		negatedPrior = append(negatedPrior, neg)
		return true
	}

	if !addBranch(n.Cond, n.Then) {
		return nil, false
	}
	for _, e := range n.Elifs {
		if e.Cond == nil {
			return nil, false
		}
		if !addBranch(e.Cond, e.Body) {
			return nil, false
		}
	}
	if len(n.Else) > 0 {
		// Explicit else: guarded by the negation of every prior condition, body = Else.
		out = append(out, branchGuard{cond: append([]ast.Expr{}, negatedPrior...), body: n.Else})
	} else {
		// IMPLICIT no-op else: guarded by ¬(all conditions), empty body (σ = identity). THIS is the path
		// that defeats "progress only in the `if` branch": it does not decrease the measure, so a loop
		// whose only progress is in the `if` branch will fail this path and be rejected.
		out = append(out, branchGuard{cond: append([]ast.Expr{}, negatedPrior...), body: nil})
	}
	return out, true
}

// negateCond returns a sound logical negation of a guard condition, plus ok=false if the condition is
// outside the small grammar we can faithfully negate. We negate by wrapping in `not (…)` — the SMT
// boolTerm translator handles `not`/`and`/comparisons; if it cannot translate the (negated) condition
// the proof simply declines downstream (sound). We still gate the SHAPE here so we never silently drop
// a path condition we cannot represent: any expression that produces a usable boolean term is fine,
// but to keep this conservative and obviously sound we accept comparisons, boolean identifiers/fields,
// boolean literals, and `and`/`or`/`not`/parenthesized combinations thereof.
func negateCond(cond ast.Expr) (ast.Expr, bool) {
	if cond == nil || !condShapeTranslatable(cond) {
		return nil, false
	}
	return &ast.UnaryExpr{
		Position: cond.Pos(),
		Op:       lexer.TOKEN_NOT,
		Operand:  &ast.ParenExpr{Position: cond.Pos(), Inner: ast.CloneExpr(cond)},
	}, true
}

// condShapeTranslatable reports whether a guard condition is within a grammar we are confident the SMT
// boolTerm translator models faithfully (comparisons over pure-arith operands, boolean idents/fields,
// boolean literals, and `and`/`or`/`not`/parenthesized combinations). Anything else (calls, indexing,
// casts to bool, etc.) → reject, so a path condition is never dropped or mis-modeled.
func condShapeTranslatable(cond ast.Expr) bool {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		return n != nil && condShapeTranslatable(n.Inner)
	case *ast.BoolLit:
		return true
	case *ast.Ident:
		return true // a boolean variable; boolTerm models it as a bool constant
	case *ast.FieldExpr:
		// A boolean field read like `self.done`; the object must be a pure field path (no call/index).
		return n != nil && !n.Safe && isPureFieldPath(n.Object)
	case *ast.UnaryExpr:
		if n == nil {
			return false
		}
		if n.Op == lexer.TOKEN_NOT {
			return condShapeTranslatable(n.Operand)
		}
		return false
	case *ast.BinaryExpr:
		if n == nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			return condShapeTranslatable(n.Left) && condShapeTranslatable(n.Right)
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
			return condArithOperandTranslatable(n.Left) && condArithOperandTranslatable(n.Right)
		}
		return false
	default:
		return false
	}
}

// condArithOperandTranslatable accepts the operand of a comparison: pure integer arithmetic over
// literals/variables/field-reads/value-casts (the same family the measure prover already reasons over).
func condArithOperandTranslatable(expr ast.Expr) bool {
	return exprIsPureArithOrRead(expr)
}

// isPureFieldPath reports whether expr is a stable field path rooted at an identifier through `.field`
// hops only (no call/index/deref/safe-nav) — used to gate boolean field reads in guard conditions.
func isPureFieldPath(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n != nil
	case *ast.ParenExpr:
		return n != nil && isPureFieldPath(n.Inner)
	case *ast.FieldExpr:
		return n != nil && !n.Safe && isPureFieldPath(n.Object)
	default:
		return false
	}
}

// stripLeadingContracts drops the leading invariant/decreases contract clauses from a loop body.
func stripLeadingContracts(body []ast.Stmt) []ast.Stmt {
	i := 0
	for i < len(body) {
		cs, ok := body[i].(*ast.ContractStmt)
		if !ok {
			break
		}
		if cs.Kind != ast.ContractInvariant && cs.Kind != ast.ContractDecreases {
			break
		}
		i++
	}
	return body[i:]
}

// wrapStmtsForCapture returns a statement slice suitable for captureLoopBodyEffectForTermination — the
// straight-line statements of a single path (no leading contracts, since those were stripped). Empty
// paths (the implicit no-op else) yield an empty body, whose capture must still report a valid identity
// substitution; captureLoopBodyEffect rejects a wholly-empty body (len(order)==0), so we special-case it.
func wrapStmtsForCapture(stmts []ast.Stmt) []ast.Stmt {
	return stmts
}

// proveLoopMeasureDecreasesAllPaths proves the lexicographic measure strictly decreases (and stays
// >= 0) on EVERY non-exiting path. An exiting path (break/return) carries no obligation. EVERY
// non-exiting path must discharge — if ANY one fails, the loop is NOT proven (return false). The
// per-path antecedent is `loopCond ∧ φ` (the loop condition conjoined with the path's guard
// conditions), passed as the `cond` to the existing single-path measure prover with the path's σ.
//
// Soundness note: the antecedent only ever ADDS hypotheses (loopCond ∧ φ). Adding a true path-condition
// hypothesis is sound — it strengthens what we may assume on that path, exactly mirroring the concrete
// control flow. We NEVER drop the loop condition and NEVER drop a guard (ifBranchGuards/negateCond reject
// rather than omit), so the antecedent can never be spuriously widened.
func (a *Analyzer) proveLoopMeasureDecreasesAllPaths(loopCond ast.Expr, invs []*ast.ContractStmt, measures []ast.Expr, paths []loopBodyPath) bool {
	if len(paths) == 0 {
		return false
	}
	provedAny := false
	for _, p := range paths {
		if p.exits {
			continue // loop terminates on this path — no decrease obligation
		}
		pathCond := buildPathCond(loopCond, p.conds)
		if proven, _ := a.proveLoopMeasureDecreases(pathCond, invs, measures, p.subst); !proven {
			return false // this non-exit path does not provably decrease → reject the whole loop
		}
		provedAny = true
	}
	// If every path exited, the loop trivially terminates (no back-edge). But a `decreases`-bearing loop
	// with no non-exit path is degenerate; require at least one proven decreasing path to record an SMT
	// proof (otherwise decline to the runtime backstop — conservative).
	return provedAny
}

// buildPathCond conjoins the loop condition with a path's guard conditions into a single boolean
// expression for the SMT hypothesis. A nil loop condition (`while true`) contributes nothing; if there
// are no conditions at all the result is nil (the caller treats nil as "no extra hypothesis", but a nil
// loop cond with no guards means an unguarded straight-line path — which the straight-line capture path
// already handled). Returns nil only when there is genuinely nothing to assert.
func buildPathCond(loopCond ast.Expr, guards []ast.Expr) ast.Expr {
	var acc ast.Expr
	conjoin := func(e ast.Expr) {
		if e == nil {
			return
		}
		if acc == nil {
			acc = e
			return
		}
		acc = &ast.BinaryExpr{Position: e.Pos(), Op: lexer.TOKEN_AND, Left: acc, Right: e}
	}
	if loopCond != nil {
		conjoin(ast.CloneExpr(loopCond))
	}
	for _, g := range guards {
		conjoin(g)
	}
	return acc
}
