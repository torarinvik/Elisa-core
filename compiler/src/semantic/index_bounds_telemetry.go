package semantic

import "elisacore/src/ast"

// Runtime telemetry uses the same lexical lifetime as proof authority, including
// cloned defaults, globals and lambdas. It counts distinct current IndexExpr
// sites requiring the ordinary panic guard, never quiet analysis visit events.
// Explicit fallback/get indexing remains in the historical proven/authorized
// bucket: its recovery branch is not an ordinary unchecked-index panic guard.
// This is semantic telemetry, not a promise about post-LLVM machine branches.
type indexBoundsRuntimeSite struct {
	Owner  ast.Node
	Header bool
	Origin ast.Node
}

func (a *Analyzer) recordIndexBoundsRuntimeGuard(expr *ast.IndexExpr) {
	if expr == nil || a.suppressOptimizationFacts {
		return
	}
	site := indexBoundsRuntimeSite{Origin: a.currentIndexProofOrigin}
	if a.currentIndexProofContext {
		site.Owner, site.Header = a.currentIndexProofHeaderOwner, a.currentIndexProofHeader
	} else if a.currentFuncDecl != nil {
		site.Owner = a.currentFuncDecl
		site.Origin = site.Owner
	}
	if fn, ok := site.Owner.(*ast.FuncDecl); ok {
		site.Owner = a.canonicalFuncDecl(fn)
	}
	if a.indexBoundsRuntimeSites == nil {
		a.indexBoundsRuntimeSites = map[*ast.IndexExpr]indexBoundsRuntimeSite{}
	}
	a.indexBoundsRuntimeSites[expr] = site
	if r := a.retained; r != nil && r.indexProofsUpdating {
		r.rememberFunctionIndexProof(site.Owner, expr)
	}
}

func (a *Analyzer) dropIndexBoundsRuntimeSite(expr *ast.IndexExpr) {
	site, present := a.indexBoundsRuntimeSites[expr]
	if !present {
		return
	}
	delete(a.indexBoundsRuntimeSites, expr)
	// The shared reverse set retains a key while either observation is live.
	if a.indexBoundsProven[expr] {
		return
	}
	if r := a.retained; r != nil {
		if fn, ok := site.Owner.(*ast.FuncDecl); ok {
			delete(r.indexProofsByFunc[fn], expr)
		} else {
			delete(r.indexProofsByDecl[site.Owner], expr)
		}
	}
}

func (a *Analyzer) currentBoundsElisionCounts() ProofElisionCounts {
	proven := 0
	for _, value := range a.indexBoundsProven {
		if value {
			proven++
		}
	}
	return ProofElisionCounts{Elided: proven, Runtime: len(a.indexBoundsRuntimeSites)}
}

func (a *Analyzer) currentElisionSummary() ProofElisionSummary {
	counts := a.currentBoundsElisionCounts()
	return ComputeElisionSummary(a.proofReport, counts.Elided, counts.Runtime)
}
