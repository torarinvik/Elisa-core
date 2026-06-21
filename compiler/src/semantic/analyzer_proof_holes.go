package semantic

import (
	"sort"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// docs/98 — proof holes + missing-fact suggestions.
//
// When an obligation cannot be discharged, instead of dumping a raw SMT counterexample we print a
// CONSTRUCTIVE diagnostic assembled from facts the analyzer already tracks: the GOAL to prove, the
// KNOWN FACTS currently in scope (interval `rangeFacts` + boolean `smtAssertFacts`), and a heuristic
// SUGGESTED missing invariant/precondition. This is the ergonomics multiplier that turns "feeding the
// solver" into "programming with an assistant".

// proofHoleReport renders the structured goal / known-facts / suggestion block for an unprovable
// `goal` evaluated against the current scope's facts. The text is deterministic (facts sorted) so it
// is stable across runs and testable.
func (a *Analyzer) proofHoleReport(header string, goal ast.Expr) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n  goal:        ")
	b.WriteString(unparse.FormatExpr(goal))

	facts := a.inScopeKnownFacts()
	b.WriteString("\n  known facts:")
	if len(facts) == 0 {
		b.WriteString(" (none in scope)")
	} else {
		for _, f := range facts {
			b.WriteString("\n    - ")
			b.WriteString(f)
		}
	}

	if sug := a.suggestMissingFact(goal, facts); sug != "" {
		b.WriteString("\n  suggested:   ")
		b.WriteString(sug)
	}
	return b.String()
}

// inScopeKnownFacts collects the analyzer's current hypothesis set as readable strings: each known
// interval bound from `rangeFacts` (closer scopes shadow outer) plus each boolean `smtAssertFacts`
// entry. De-duplicated and sorted for determinism.
func (a *Analyzer) inScopeKnownFacts() []string {
	if a == nil || a.currentScope == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}

	boundSeen := map[string]bool{}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		names := make([]string, 0, len(sc.rangeFacts))
		for name := range sc.rangeFacts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if boundSeen[name] {
				continue // a closer scope's interval shadows the outer one
			}
			boundSeen[name] = true
			r := sc.rangeFacts[name]
			if r.loKnown {
				add(name + " >= " + intToStr(r.lo))
			}
			if r.hiKnown {
				add(name + " <= " + intToStr(r.hi))
			}
		}
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			if fact.Expr == nil {
				continue
			}
			add(unparse.FormatExpr(fact.Expr))
		}
	}
	sort.Strings(out)
	return out
}

// suggestMissingFact applies the increment-1 heuristics: missing upper bound (the canonical
// unbounded-index `i < n`), missing lower bound, and missing loop invariant. Advisory only — a wrong
// guess never affects soundness.
func (a *Analyzer) suggestMissingFact(goal ast.Expr, facts []string) string {
	bin, ok := stripOptimizationParens(goal).(*ast.BinaryExpr)
	if !ok {
		return ""
	}
	left := unparse.FormatExpr(bin.Left)
	right := unparse.FormatExpr(bin.Right)

	switch bin.Op {
	case lexer.TOKEN_LT, lexer.TOKEN_LTEQ:
		// Goal `a < b` / `a <= b`: the upper end of `a` is unbounded relative to `b`. If no known
		// fact already relates them, the missing invariant/precondition IS the goal.
		if !factsRelate(facts, left, right) {
			inv := left + " " + opString(bin.Op) + " " + right
			if a.loopDepth > 0 {
				return "no fact bounds `" + left + "` above; add a loop invariant `" + inv +
					"` or a precondition `requires " + inv + "`"
			}
			return "no fact bounds `" + left + "` above; add a precondition `requires " + inv + "`"
		}
	case lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
		// Goal `a > b` / `a >= b`: missing lower bound on `a`.
		if !factsRelate(facts, left, right) {
			inv := left + " " + opString(bin.Op) + " " + right
			if a.loopDepth > 0 {
				return "no fact bounds `" + left + "` below; establish `" + inv +
					"` before the loop or add a precondition `requires " + inv + "`"
			}
			return "no fact bounds `" + left + "` below; add a precondition `requires " + inv + "`"
		}
	}
	return ""
}

// factsRelate reports whether any known fact mentions both sides of the goal comparison — a cheap
// proxy for "the prover already has something connecting these two terms" so we do not suggest a fact
// that is in some form already present.
func factsRelate(facts []string, left, right string) bool {
	for _, f := range facts {
		if strings.Contains(f, left) && strings.Contains(f, right) {
			return true
		}
	}
	return false
}

func opString(op lexer.TokenKind) string {
	switch op {
	case lexer.TOKEN_LT:
		return "<"
	case lexer.TOKEN_LTEQ:
		return "<="
	case lexer.TOKEN_GT:
		return ">"
	case lexer.TOKEN_GTEQ:
		return ">="
	}
	return "?"
}

func intToStr(v int64) string {
	return strconv.FormatInt(v, 10)
}

// checkStrictAssertProofHole offers a constructive proof-hole HINT for a plain `assert(cond)` under
// strict proofs (docs/98) whose integer-comparison goal the static tiers cannot discharge. Outside
// `-strict` an assert is a debug runtime check, so this is a no-op there. The discharge ladder mirrors
// `assert … by:`: linear tier, then the SMT tier when enabled. When neither closes the goal the assert
// REMAINS a runtime check (ProofRuntime) and the constructive diagnostic is emitted as a non-fatal
// warning — never a hard error, so it cannot reject previously-valid code.
//
// Increment 1 deliberately scopes the trigger to integer-comparison goals (`a < b`, `a >= b`, …): this
// is the canonical unbounded-index obligation the heuristics target, and it keeps the new error off
// asserts the static tiers were never meant to decide (pointer/bool/struct shapes), so no in-tree
// strict fixture regresses on an assert the prover legitimately cannot model.
func (a *Analyzer) checkStrictAssertProofHole(pos lexer.Pos, cond ast.Expr) {
	// Gated on BOTH strict proofs and the SMT tier (docs/90/98). The SMT discharge path is the only
	// in-tree prover that consumes the analyzer's FULL in-scope hypothesis set (flow facts + asserts +
	// intervals); the linear `proveRequiresClause` tier is built for callee-requires substitution, not
	// in-body asserts, so it would reject facts a strict author legitimately established. Requiring
	// `-smt` keeps the prove-it-or-fail bar SOUND (no false "unprovable" on a genuinely entailed assert)
	// and leaves plain `-strict` builds without z3 exactly as they were: asserts are runtime checks.
	// Opt-in only: the proof-hole assistant is something the user INVOKES (docs/98), not an always-on
	// diagnostic. Without this gate the hint would fire on every strict+smt compile of a runtime-checked
	// stdlib assert (e.g. stores_packed_aos.elisa), flooding normal builds. Reserved for the future
	// `assert ?` / `--explain-hole` surface; tests opt in via AnalyzeOptions{EmitProofHoleHints: true}.
	if a == nil || !a.emitProofHoleHints || !a.enforceStrictProofs || !a.smtEnabled || cond == nil {
		return
	}
	if !isIntComparisonGoal(cond) {
		return
	}
	if a.proveRequiresClause(cond, nil) == requiresProven {
		a.recordProof(pos, "assert", "assert", ProofProvenLinear)
		return
	}
	if proven, _ := a.trySMTProveRequires(cond, nil); proven {
		a.recordProof(pos, "assert", "assert", ProofProvenSMT)
		return
	}
	// A plain `assert` is a leaf/debug runtime check, NOT a load-bearing prove-or-fail obligation
	// (docs/98; the three-way fallback discussion). So the assert stays a runtime check (ProofRuntime,
	// recorded above) and the constructive proof-hole report is a NON-FATAL hint, never a hard error —
	// it must not reject previously-valid code (e.g. stdlib asserts that are genuine runtime invariants).
	// The hard prove-or-fail bar is reserved for an explicit `assert ?` hole (a later increment).
	a.warnf(pos, "%s", a.proofHoleReport("proof hole: assertion could not be proven", cond))
}

// isIntComparisonGoal reports whether `cond` is a top-level integer relational comparison — the goal
// shape the increment-1 proof-hole engine reasons about.
func isIntComparisonGoal(cond ast.Expr) bool {
	bin, ok := stripOptimizationParens(cond).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	switch bin.Op {
	case lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
		return true
	}
	return false
}
