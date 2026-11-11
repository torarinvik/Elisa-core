package semantic

import (
	"elisacore/src/ast"
)

// analyzerProofSolverState holds solver configuration, lifecycle, and memoized proof queries.
type analyzerProofSolverState struct {
	// SMT discharge tier (docs/90). The solver is opened LAZILY on the first obligation that needs it
	// (so a compile with no hard obligations never spawns a process) and closed at the end of
	// analysis. smtUnavailable latches once Open fails, so we don't retry a missing solver per query.
	smtEnabled     bool
	smtBinary      string
	smtSolver      smtSolverHandle
	smtUnavailable bool
	// smtSnapshotTainted prevents reuse of semantic results produced after a
	// solver failure or an Unknown that cannot be safely memoized. It is sticky
	// across retained updates because unaffected bodies may retain those results.
	smtSnapshotTainted bool
	smtStats           SMTStats
	// smtQueryCache memoizes solver verdicts keyed on the exact query (VC IR brick 5). z3 is
	// deterministic for a given query + timeout, so an identical query — same preamble, hypotheses, and
	// negated obligation — has the same verdict and counterexample; reusing it is sound and skips the
	// round-trip. Lazily created; lives for one analysis pass (a query embeds its function's facts, so a
	// cross-function cache hit represents the same query).
	smtQueryCache map[string]smtQueryResult
	// smtEscalation > 0 while withSMTEscalation re-runs a failed obligation.
	smtEscalation int
	// smtHistory lists the obligation queries asked so far in smtHistoryFor's body, cache hits
	// included, for the warm-session stage of an escalated retry.
	smtHistory               []string
	smtHistoryFor            *ast.FuncDecl
	smtProofCache            *smtProofCache
	smtProofCacheInitialized bool
	// smtDeclIDs gives each declaration a run-stable identity for SMT symbol names (smtStableDeclID).
	smtDeclIDs        map[*ast.FuncDecl]string
	smtDeclIDOrdinals map[string]int
	// recursiveProofMemo caches recursiveProofMembers per resolution context.
	recursiveProofMemo map[recursiveProofKey]map[*ast.FuncDecl]bool
	// aggregateEquationEligibleMemo caches aggregateRecordDefiningEquationEligible likewise.
	aggregateEquationEligibleMemo map[recursiveProofKey]bool
	// pinnedRelationalEdges is the edge snapshot for a batch of read-only range lookups.
	pinnedRelationalEdges      []relationalEdge
	pinnedRelationalEdgesValid bool
	// smtDeadPathCache memoizes cone-of-influence dead-path verdicts by the dropped text.
	smtDeadPathCache map[string]smtVerdict
	// recursiveCertInProgress holds the recursive calls whose termination certificate or callee
	// requires is being proven.
	// The certificate's own goal translates the caller's immutable locals, and a local DEFINED by that
	// same call would request the certificate again without end (stack overflow).
	recursiveCertInProgress map[*ast.CallExpr]bool
	proofReport             []ProofFact
	proofLifecycle          proofReportLifecycle
	currentProofCategory    ProofObligationCategory                   // elision-telemetry tag stamped on the next recordProof call; cleared after each call
	indexBoundsRuntimeSites map[*ast.IndexExpr]indexBoundsRuntimeSite // live ordinary panic-guard sites; speculative visits do not count
}
