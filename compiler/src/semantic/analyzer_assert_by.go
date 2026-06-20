package semantic

import (
	"elisacore/src/ast"
)

// analyzeAssertBy handles a proof-carrying `assert COND by:` statement (Dafny-style proof blocks).
//
// SEMANTICS & SOUNDNESS:
//
//   - The proof block is analyzed in a CHILD scope of the current one. Facts the block establishes
//     (lemma-call ensures, nested asserts, branch refinements) accumulate on that child scope only.
//     Because the SMT/flow fact lookup walks parent→child, those facts are visible while proving COND
//     but vanish the instant we pop back to the parent — so they are scoped OUT of everything after the
//     assert. ONLY COND is re-exported into the parent scope.
//
//   - COND must be PROVEN from caller facts ∪ block facts (cheap linear tier first, then the SMT tier
//     if enabled). An unproven COND is a hard error under strict proofs and a proof-lint otherwise —
//     exactly the failure mode of an ordinary assertion that cannot be discharged. The block can only
//     ever ADD true hypotheses, so it can never make an unprovable COND falsely pass: if COND does not
//     follow, the assert still fails.
//
//   - The block has NO real side effects: it is restricted to a whitelist of verification-only
//     statements (lemma calls, nested asserts/assert-by, in-body invariants, static asserts/blocks).
//     A real mutating or value-producing statement inside `by:` is rejected. The whole block is erased
//     from codegen (it is never reached by the backend, which only lowers COND as a normal assert).
func (a *Analyzer) analyzeAssertBy(n *ast.AssertByStmt) {
	if a == nil || n == nil {
		return
	}

	saved := a.currentScope
	child := NewScope(saved)
	a.currentScope = child

	// 1. Analyze the proof block in the child scope, enforcing the no-side-effects whitelist. Each
	//    permitted statement is analyzed through the normal flow machinery, so a lemma call discharges
	//    its requires and injects its ensures as child-scope facts, a nested assert seeds its condition,
	//    etc.
	for _, stmt := range n.Proof {
		if !a.assertProofStmtAllowed(stmt) {
			a.errorf(stmt.Pos(), "an `assert … by:` proof block may contain only verification-only statements (lemma calls, nested `assert`s, `invariant`s); a statement with real effects is not allowed because the block is erased from code")
			continue
		}
		a.analyzeStmt(stmt)
	}

	// 2. Prove COND under caller ∪ block facts. currentScope is still the child here, so the block's
	//    facts are in scope for the proof.
	condType := a.analyzeCondExpr(n.Cond)
	if condType != nil && !IsBoolType(condType) {
		a.errorf(n.Cond.Pos(), "assert condition must be bool, got %s", condType)
	}
	a.proveAssertByCond(n)

	// 3. Pop back to the caller scope. The child scope (and ALL block facts) is dropped here — only COND
	//    survives, recorded below into the caller scope.
	a.currentScope = saved

	// 4. Export ONLY COND as a fact for the code after the assert, exactly as a plain assert would.
	a.applyConditionRefinements(a.currentScope, n.Cond, true)
	a.applyIndexBoundsFactsForCondition(n.Cond, true)
	a.recordSMTAssertFact(n.Cond)
}

// proveAssertByCond discharges COND from the current (child) scope's facts: cheap linear tier first,
// then the SMT tier if enabled. An unprovable COND is reported with the counterexample machinery —
// a hard error under strict proofs, a proof-lint otherwise (mirroring an ordinary assert that cannot
// be statically discharged).
func (a *Analyzer) proveAssertByCond(n *ast.AssertByStmt) {
	if n.Cond == nil {
		return
	}
	if a.proveRequiresClause(n.Cond, nil) == requiresProven {
		a.recordProof(n.Cond.Pos(), "assert by", "assert", ProofProvenLinear)
		return
	}
	proven, counterexample := a.trySMTProveRequires(n.Cond, nil)
	if proven {
		a.recordProof(n.Cond.Pos(), "assert by", "assert", ProofProvenSMT)
		return
	}
	msg := "`assert … by:` condition could not be proven from its proof block; the block's facts plus the caller's facts must entail it (add intermediate `assert`s or call a helper `lemma`)"
	if a.enforceStrictProofs {
		a.errorf(n.Cond.Pos(), "%s%s", msg, a.counterexampleSuffix(counterexample))
	} else {
		a.proofLint(n.Cond.Pos(), "%s%s", msg, a.counterexampleSuffix(counterexample))
	}
}

// assertProofStmtAllowed reports whether a statement may appear in an `assert … by:` proof block. The
// block is erased from codegen, so it must carry NO real effects: only verification-only statements
// are permitted. A bare expression statement is allowed only when it is a lemma call (ghost code);
// any other expression — a value-producing or mutating call, an assignment, a loop, a return, etc. —
// is rejected.
func (a *Analyzer) assertProofStmtAllowed(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.AssertByStmt:
		return true
	case *ast.StaticAssertStmt, *ast.StaticAssertBlockStmt, *ast.StaticBlockStmt:
		return true
	case *ast.PassStmt:
		return true
	case *ast.ContractStmt:
		// Only an in-body `invariant` is a pure assertion; a stray requires/ensure is not meaningful here.
		return n.Kind == ast.ContractInvariant
	case *ast.ExprStmt:
		// A lemma call (ghost code) is permitted; a plain `assert(COND)` call is permitted (it only seeds
		// a fact). Any other expression statement has potential effects and is rejected.
		if cond, ok := assertedCondition(n.Expr); ok {
			_ = cond
			return true
		}
		return a.callTargetsLemma(n.Expr)
	default:
		return false
	}
}
