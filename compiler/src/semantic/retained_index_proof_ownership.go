package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

// prepareIndexProofOwnership validates and snapshots the owners before an update mutates analysis state.
func (r *RetainedAnalysis) prepareIndexProofOwnership() error {
	if r.a != nil && (r.a.smtUnavailable || r.a.smtSnapshotTainted) {
		r.LastStats = RetainedUpdateStats{Fallback: "previous analysis had transient solver state; retry with a full analysis"}
		return fmt.Errorf("%s", r.LastStats.Fallback)
	}
	if r.proofCoordinatesDeferred {
		r.LastStats = RetainedUpdateStats{Fallback: "proof report coordinates were deferred; retry with a full analysis"}
		return fmt.Errorf("%s", r.LastStats.Fallback)
	}
	r.indexProofsUpdating = true
	r.indexProofsByFunc = map[*ast.FuncDecl]map[*ast.IndexExpr]bool{}
	if r.a != nil {
		for expr, proven := range r.a.indexBoundsProven {
			if !proven || expr == nil {
				continue
			}
			fn, owned := r.a.indexBoundsProofOwners[expr]
			if !owned || fn == nil {
				r.indexProofsUpdating = false
				r.indexProofsByFunc = nil
				r.indexProofsByDecl = nil
				r.LastStats = RetainedUpdateStats{Fallback: "retained index proof has no exact semantic ownership; retry with a full analysis"}
				return fmt.Errorf("%s", r.LastStats.Fallback)
			}
			r.rememberFunctionIndexProof(fn, expr)
		}
	}
	if r.a != nil {
		for expr, site := range r.a.indexBoundsRuntimeSites {
			if expr == nil || site.Owner == nil {
				r.indexProofsUpdating, r.indexProofsByFunc, r.indexProofsByDecl = false, nil, nil
				r.LastStats = RetainedUpdateStats{Fallback: "retained runtime bounds site has no exact lexical ownership; retry with a full analysis"}
				return fmt.Errorf("%s", r.LastStats.Fallback)
			}
			r.rememberFunctionIndexProof(site.Owner, expr)
		}
	}
	return nil
}
