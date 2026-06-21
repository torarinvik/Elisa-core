package semantic

import (
	"elisacore/src/ast"
	"strings"

	"elisacore/src/unparse"
)

type scopedProofCitation struct {
	Name  string
	Facts []ast.Expr
}

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
	// docs/99: a `by scoped:` block is a CLOSED WORLD. The child scope is marked as a proof wall, so the
	// fact-gathering chain walks (SMT assert/flow facts, range facts) include only facts established
	// INSIDE this block (cited lemma ensures, nested asserts) and stop before the enclosing scope. Symbol
	// and refinement LOOKUP still ascend (lemma/type names resolve), so only ambient PROOF FACTS are
	// walled. This guarantees stability: the verdict depends solely on the cited facts, never on unrelated
	// surrounding code that would otherwise perturb the solver's hypothesis set.
	child.closedWorld = n.Scoped
	a.currentScope = child

	// While a scoped block is analyzed and its COND discharged, suppress the ambient (non-scope-walled)
	// hypothesis sources so the closed world holds ONLY the block's citations. Restored on the way out.
	savedClosed := a.inClosedWorldProof
	if n.Scoped {
		a.inClosedWorldProof = true
	}
	defer func() { a.inClosedWorldProof = savedClosed }()

	// 1. Analyze the proof block in the child scope, enforcing the no-side-effects whitelist. Each
	//    permitted statement is analyzed through the normal flow machinery, so a lemma call discharges
	//    its requires and injects its ensures as child-scope facts, a nested assert seeds its condition,
	//    etc.
	citations := []*scopedProofCitation{}
	for _, stmt := range n.Proof {
		if !a.assertProofStmtAllowed(stmt) {
			a.errorf(stmt.Pos(), "an `assert … by:` proof block may contain only verification-only statements (lemma calls, nested `assert`s, `invariant`s); a statement with real effects is not allowed because the block is erased from code")
			continue
		}
		if use, ok := stmt.(*ast.ProofUseStmt); ok {
			citations = append(citations, a.analyzeProofUseStmt(use)...)
			continue
		}
		if citation := a.scopedProofCitationForStmt(stmt); citation != nil {
			savedCitation := a.currentProofCitation
			a.currentProofCitation = citation
			a.analyzeStmt(stmt)
			a.currentProofCitation = savedCitation
			citations = append(citations, citation)
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
	if n.Scoped {
		a.reportLoadBearingScopedCitations(n, child, citations)
	}

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
	class := ProofClassLinear
	if n.Scoped {
		class = ProofClassScoped
	}
	if a.proveRequiresClause(n.Cond, nil) == requiresProven {
		a.recordProofWithClass(n.Cond.Pos(), "assert by", "assert", ProofProvenLinear, class, nil, "")
		return
	}
	proven, counterexample := a.trySMTProveRequires(n.Cond, nil)
	if proven {
		a.recordProofWithClass(n.Cond.Pos(), "assert by", "assert", ProofProvenSMT, class, nil, "")
		return
	}
	if n.Scoped {
		a.recordProofWithClass(n.Cond.Pos(), "assert by", "assert", ProofRuntime, ProofClassScoped, a.closedWorldProofFacts(), "nothing establishes the scoped goal")
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
	case *ast.AssertHoleStmt:
		return true
	case *ast.ProofUseStmt:
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

func (a *Analyzer) analyzeProofUseStmt(stmt *ast.ProofUseStmt) []*scopedProofCitation {
	if a == nil || stmt == nil {
		return nil
	}
	out := make([]*scopedProofCitation, 0, len(stmt.Citations))
	for _, citationExpr := range stmt.Citations {
		call := proofCitationCall(citationExpr)
		if call == nil {
			a.errorf(citationExpr.Pos(), "`use` citations in an `assert … by:` proof block must name a lemma or call a lemma")
			continue
		}
		citation := &scopedProofCitation{Name: proofCitationName(call)}
		savedCitation := a.currentProofCitation
		a.currentProofCitation = citation
		a.analyzeExpr(call)
		a.currentProofCitation = savedCitation
		out = append(out, citation)
	}
	return out
}

func proofCitationCall(expr ast.Expr) *ast.CallExpr {
	switch n := expr.(type) {
	case *ast.CallExpr:
		return n
	case *ast.Ident:
		return &ast.CallExpr{Position: n.Position, Func: n}
	case *ast.ParenExpr:
		return proofCitationCall(n.Inner)
	default:
		return nil
	}
}

func proofCitationName(call *ast.CallExpr) string {
	if call == nil {
		return "<unknown>"
	}
	if ident, ok := call.Func.(*ast.Ident); ok && ident != nil && ident.Name != "" {
		return ident.Name
	}
	return "<call>"
}

func (a *Analyzer) scopedProofCitationForStmt(stmt ast.Stmt) *scopedProofCitation {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok || exprStmt == nil {
		return nil
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok || call == nil || !a.callTargetsLemma(call) {
		return nil
	}
	return &scopedProofCitation{Name: proofCitationName(call)}
}

func (a *Analyzer) closedWorldProofFacts() []string {
	if a == nil || a.currentScope == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			if fact.Expr == nil {
				continue
			}
			add(unparse.FormatExpr(fact.Expr))
		}
		if sc.closedWorld {
			break
		}
	}
	return out
}

func (a *Analyzer) reportLoadBearingScopedCitations(n *ast.AssertByStmt, scope *Scope, citations []*scopedProofCitation) {
	if a == nil || n == nil || scope == nil || len(citations) == 0 {
		return
	}
	loadBearing := []string{}
	for _, citation := range citations {
		if citation == nil || len(citation.Facts) == 0 {
			continue
		}
		removed := map[ast.Expr]bool{}
		for _, fact := range citation.Facts {
			removed[fact] = true
		}
		savedFacts := scope.smtAssertFacts
		scope.smtAssertFacts = filterSMTAssertFacts(savedFacts, removed)
		if a.proveRequiresClause(n.Cond, nil) != requiresProven {
			if proven, _ := a.trySMTProveRequires(n.Cond, nil); !proven {
				loadBearing = append(loadBearing, citation.Name)
			}
		}
		scope.smtAssertFacts = savedFacts
	}
	if len(loadBearing) == 0 {
		return
	}
	a.warnf(n.Cond.Pos(), "`assert … by scoped:` load-bearing citations: %s", strings.Join(loadBearing, ", "))
}

func filterSMTAssertFacts(facts []smtFact, removed map[ast.Expr]bool) []smtFact {
	if len(facts) == 0 || len(removed) == 0 {
		return facts
	}
	out := make([]smtFact, 0, len(facts))
	for _, fact := range facts {
		if removed[fact.Expr] {
			continue
		}
		out = append(out, fact)
	}
	return out
}
