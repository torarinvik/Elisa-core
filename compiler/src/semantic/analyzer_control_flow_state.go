package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// analyzerControlFlowState holds bounds, loop, and branch facts scoped to function analysis.
type analyzerControlFlowState struct {
	currentIndexBounds map[string]indexBoundFact
	// currentBoundEqual is a flow-sensitive equivalence relation over upper-bound EXPRESSION STRINGS
	// (the same canonical form as indexBoundFact.Upper / indexableUpperBoundString): a symmetric
	// adjacency set recording that two length-ish expressions are provably equal — `n == xs.count`
	// from a guard, or `n = xs.count` from an immutable binding. It lets a loop index whose bound is
	// `n` discharge an `xs[i]` access whose bound is `xs.count` (docs/86 brick 86-6, the cross-variable
	// coupling §8 flagged). It rides EXACTLY the same clone/save/restore/invalidate sites as
	// currentIndexBounds, so it can never outlive a branch or survive a mutation that the index facts
	// themselves wouldn't (soundness).
	currentBoundEqual map[string]map[string]bool
	// indexBoundsShared / boundEqualShared mark currentIndexBounds / currentBoundEqual as possibly
	// aliased by an enclosing block's saved copy: a writer clones first (ownIndexBounds /
	// ownBoundEqual). Entering a block only sets the flag instead of copying both maps.
	indexBoundsShared                 bool
	boundEqualShared                  bool
	currentFunctionUsedPermissions    map[string]bool
	currentFunctionUsedPermissionRefs []ast.PermissionRef
	// currentFunctionGuardedIndexes collects the positions of index accesses in the current function
	// that would emit a runtime bounds check (bounds-requiring type, not statically proven). It backs
	// the `NoBoundsChecks` shape law (docs/89): a function `fulfills NoBoundsChecks` is violated iff
	// this list is non-empty. Saved/restored per function like currentFunctionUsedPermissionRefs.
	currentFunctionGuardedIndexes []lexer.Pos
	// currentFunctionExpectsVectorize is set while analyzing a function that `fulfills Vectorizes` (a
	// measure law, docs/89 Stage 5). It opts the function's range loops into the existing autovec
	// verifier: each ForStmt is tagged AutovecExpected, so the post-optimization pass warns (verify-
	// only, never build-gating) if the loop did not vectorize. Saved/restored per function.
	currentFunctionExpectsVectorize bool
	currentProgressSummary          *FunctionProgressSummary
	loopDepth                       int
	// loopAffineFrames collects each loop's `continue` affine states; loopRepeatReported keeps the
	// repeated-consume diagnostic to one per root across nested loops.
	loopAffineFrames []loopAffineFrame
	// storageViewLoopUseFrames records each enclosing loop's first view uses (back-edge check).
	storageViewLoopUseFrames []storageViewLoopUseFrame
	loopRepeatReported       map[*Symbol]bool
	// protocolLeakReported dedupes "must be consumed before scope exit" between an early exit
	// (return, try, break, continue) and the end of the function.
	protocolLeakReported map[affineValueKey]bool
	// activeOuterLoopInvariants is a stack of proven outer-loop invariant clauses accumulated as the
	// analyzer descends into nested loops. Each push corresponds to one WhileStmt whose invariants were
	// fully proven; each pop happens after the body is analyzed. The inner-loop invariant prover reads
	// this stack to use outer invariants as additional hypotheses when the outer invariant's dependency
	// variables are disjoint from the inner loop's assigned variables (conservative: if the inner loop
	// mutates ANY variable the outer invariant depends on, the outer invariant is NOT admitted).
	activeOuterLoopInvariants []*ast.ContractStmt
	implicitIntConvFuncs      map[*ast.FuncDecl]bool
	rangeFactWriteLogs        [][]rangeFactWrite
	pendingLoopInvariantLints map[*ast.ContractStmt]string
	// seqLoopContinueStep is the index update a `continue` performs in the range loop whose
	// synthesized while body the sequential model is walking (nil for a while loop).
	seqLoopContinueStep               map[string]ast.Expr
	currentTrustedNonProgressDepth    int
	currentTrustedAssumeProgressDepth int
	currentTrustedStaleRefDepth       int
	// currentUnsafePointerCastGrantDepth tracks an explicit local
	// `can Unsafe.PointerCast` or `trusted Unsafe.PointerCast` scope. The raw
	// StringView ABI carrier may only be constructed within this boundary;
	// surface `sview` values should otherwise come from validated constructors.
	currentUnsafePointerCastGrantDepth int
	// currentGrantedStaleRefDepth tracks an enclosing *tracked* `can Unsafe.StaleRef:`
	// grant (vs the untracked `trusted` suppression in currentTrustedStaleRefDepth).
	// A forwarded-ref store inside it is allowed AND surfaces Unsafe.StaleRef in the
	// function's effect signature (recorded by analyzeCanStmt), so the unsafe store is
	// auditable through the capability system rather than invisibly suppressed.
	currentGrantedStaleRefDepth int
	// currentGranted{NonProgress,AssumeProgress}Depth: an enclosing tracked `can Unsafe.NonProgress:`
	// / `can Unsafe.AssumeProgress:` discharges a wrapped loop's progress obligation just like
	// `trusted` (the grant is in scope), but — unlike `trusted` — also propagates the effect to the
	// function's signature. Mirrors currentGrantedStaleRefDepth above.
	currentGrantedNonProgressDepth    int
	currentGrantedAssumeProgressDepth int
	currentReturnProvenance           regionRefState
	currentReturnBorrowedOwnerRefs    borrowedOwnerRefSummary
	currentConservativeCallWidenings  map[*Symbol][]conservativeCallWidening
	currentRegionFactTransforms       []FactTransform
	conditionalCallPoststateOriginals map[*ast.CallExpr]map[*Symbol]Type
}
