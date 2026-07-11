package semantic

import (
	"sort"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// The join rule (docs/125 §1d): "any value whose meaning differs between incoming
// control-flow paths must cross the join explicitly." The forbidden implicit channel is
// STATEMENT-JOIN AMBIENT MUTATION — a branch statement (`if`/`match`) whose fall-through
// arms assign several OUTER variables and then fall through, leaving the reader (and the
// checker) to reconstruct phi nodes from ambient writes. The blessed forms cross the join
// explicitly: an `if`/`match` VALUE, a `rebind`, a state payload, a loop-header accumulator.
//
// This is R7. Per docs/125 §7 it is measured (census) before it is enforced. This file is the
// CENSUS TOOL — findJoinRuleSites — exercised by TestJoinRuleCensus; it is deliberately NOT
// wired into checkFlowComplexity yet.
//
// A variable written with `x <- v` (AssignStmt) is necessarily an OUTER binding — you cannot
// reassign one you did not already declare — so the "outer" test is syntactic and needs no
// types. An arm that DIVERTS (return/break/continue/raise) never reaches the join, so its
// writes are not phi'd; only fall-through arms count. A variable is counted only when TWO OR
// MORE sibling fall-through arms write it (a genuine value-picking join, not a lone guard),
// and compiler-synthesized `__`-names (machine desugars, if-let temps) are excluded.
//
// CENSUS VERDICT (2026-07-11, stage1 compiler + stdlib, 145+18 files): enforcement DEFERRED.
// At the 0-FP-defensible threshold the anti-pattern is effectively ABSENT — the corpus already
// crosses joins explicitly (loop-header captures, threaded accumulators, match values). The
// handful of ≥2-var hits are all blessed forms the syntactic pass can't yet distinguish:
// post-desugar machine if-ladders (`{depth, lexer}`), self-referential accumulation
// (`n <- n + 1` across arms), same-value writes (`flag <- true` in two arms), and captured
// threaded resources (`table`, already in a `|table, …|` manifest). Wiring a warn-tier lint
// now would fire only on those false positives or on the common default-then-overwrite idiom
// (`x, y` extracted from a `match` with a `_` default) — which §1d classes as style, not a
// divergence between two things the programmer wrote. Re-run the census as the codebase grows;
// graduate only if genuine multi-variable divergent-phi sites actually appear.

// joinRuleSite is one candidate branch statement: its position and the outer variables its
// fall-through arms phi-reconstruct.
type joinRuleSite struct {
	pos  lexer.Pos
	vars []string
}

// findJoinRuleSites walks a function body and returns every branch statement whose
// fall-through arms collectively assign `minVars` or more DISTINCT outer variables. Recurses
// into nested blocks so a join deep inside a loop or another branch is still found.
func findJoinRuleSites(body []ast.Stmt, minVars int) []joinRuleSite {
	var sites []joinRuleSite
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch n := stmt.(type) {
			case *ast.IfStmt:
				if vars := ifChainJoinVars(n); len(vars) >= minVars {
					sites = append(sites, joinRuleSite{pos: n.Position, vars: vars})
				}
				walkIfChain(n, walk)
			case *ast.MatchStmt:
				if vars := matchJoinVars(n); len(vars) >= minVars {
					sites = append(sites, joinRuleSite{pos: n.Position, vars: vars})
				}
				for _, arm := range n.Arms {
					walk(arm.Body)
				}
			case *ast.WhileStmt:
				walk(n.Body)
			case *ast.ForStmt:
				walk(n.Body)
			case *ast.IterForStmt:
				walk(n.Body)
			case *ast.CanStmt:
				walk(n.Body)
			}
		}
	}
	walk(body)
	return sites
}

// walkIfChain recurses into every arm body of an if/elif/else chain.
func walkIfChain(ifStmt *ast.IfStmt, walk func([]ast.Stmt)) {
	cur := ifStmt
	for {
		walk(cur.Then)
		for _, elif := range cur.Elifs {
			walk(elif.Body)
		}
		if next := flowElseIfContinuation(cur.Else); next != nil {
			cur = next
			continue
		}
		walk(cur.Else)
		return
	}
}

// ifChainJoinVars returns the outer variables genuinely PHI-RECONSTRUCTED by an if/elif/else
// chain: those assigned in TWO OR MORE distinct fall-through arms, so the join actually picks
// between ≥2 written values. A variable written in a single guarded arm (`if c: x <- v` with
// no sibling writer) is an ordinary guard-update — the join picks between "written" and
// "prior", which is not the ambient-phi anti-pattern — and is deliberately NOT counted, so
// the rule stays 0-FP on guarded bulk updates and running-max scans.
func ifChainJoinVars(ifStmt *ast.IfStmt) []string {
	var arms [][]string
	cur := ifStmt
	for {
		arms = append(arms, fallThroughAssignList(cur.Then))
		for _, elif := range cur.Elifs {
			arms = append(arms, fallThroughAssignList(elif.Body))
		}
		if next := flowElseIfContinuation(cur.Else); next != nil {
			cur = next
			continue
		}
		arms = append(arms, fallThroughAssignList(cur.Else))
		return varsWrittenInAtLeastTwoArms(arms)
	}
}

// matchJoinVars returns the variables a match statement phi-reconstructs (written in ≥2
// fall-through arms).
func matchJoinVars(match *ast.MatchStmt) []string {
	arms := make([][]string, 0, len(match.Arms))
	for _, arm := range match.Arms {
		arms = append(arms, fallThroughAssignList(arm.Body))
	}
	return varsWrittenInAtLeastTwoArms(arms)
}

// varsWrittenInAtLeastTwoArms returns the names that appear in the assignment lists of two or
// more distinct arms, sorted — the genuinely phi-reconstructed set.
func varsWrittenInAtLeastTwoArms(arms [][]string) []string {
	armCount := map[string]int{}
	for _, arm := range arms {
		seen := map[string]bool{}
		for _, name := range arm {
			if !seen[name] {
				seen[name] = true
				armCount[name]++
			}
		}
	}
	phi := map[string]bool{}
	for name, c := range armCount {
		if c >= 2 {
			phi[name] = true
		}
	}
	return sortedSet(phi)
}

// fallThroughAssignList returns the distinct bare-Ident assignment targets in a block, or nil
// if the block diverts (its writes never reach the join).
func fallThroughAssignList(body []ast.Stmt) []string {
	if flowBlockEndsInExit(body) {
		return nil
	}
	set := map[string]bool{}
	collectAssignTargets(body, set)
	return sortedSet(set)
}

// collectAssignTargets records the names of all bare-Ident AssignStmt targets in a block,
// recursing through nested branches/loops (a var assigned in a nested arm is still ambient
// state that outlives this statement).
func collectAssignTargets(body []ast.Stmt, set map[string]bool) {
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.AssignStmt:
			// A `__`-prefixed target is a compiler-synthesized binding (a machine mode/payload
			// local, an `if let` temp, …). Those are the blessed desugars, not hand-written
			// ambient mutation, so they never count toward the join rule.
			if id, ok := n.Target.(*ast.Ident); ok && !isSynthesizedName(id.Name) {
				set[id.Name] = true
			}
		case *ast.IfStmt:
			walkIfChain(n, func(b []ast.Stmt) { collectAssignTargets(b, set) })
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				collectAssignTargets(arm.Body, set)
			}
		case *ast.WhileStmt:
			collectAssignTargets(n.Body, set)
		case *ast.ForStmt:
			collectAssignTargets(n.Body, set)
		case *ast.IterForStmt:
			collectAssignTargets(n.Body, set)
		case *ast.CanStmt:
			collectAssignTargets(n.Body, set)
		}
	}
}

// isSynthesizedName reports whether a binding name is compiler-generated (the desugar
// convention is a `__` prefix), so the join rule ignores it.
func isSynthesizedName(name string) bool {
	return len(name) >= 2 && name[0] == '_' && name[1] == '_'
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
