package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"time"
)

// selectRetainedScratch selects or materializes the independent pre-body snapshot.
func (r *RetainedAnalysis) selectRetainedScratch(pre *preBodyState, scratch, file *ast.File, prepared *RetainedScratch, fast retainedFastBodies, pairedNew map[*ast.FuncDecl]*ast.FuncDecl, marks map[string][][2]int) (*preBodyState, *ast.File, error) {
	fastBodies, fastResolvedTypeNameRows := fast.bodies, fast.rows
	fastResolvedTypeNameContributions, fastResolvedTypeNameSourceOwners := fast.contributions, fast.owners
	if pre != nil {
		// Prepared above: nothing to wait for.
	} else if fastBodies != nil {
		// The edit leaves every declaration-level result as the retained baseline holds it.
		// The new prefix is independently relocated; mutating/aliasing the old
		// baseline would invalidate preBodyDifference and later disk revisions.
		resolvedTypeNames := r.pre.resolvedTypeNames
		if len(fastResolvedTypeNameContributions) != 0 {
			resolvedTypeNames = cloneResolvedTypeNameContributions(resolvedTypeNames)
			for key, body := range fastResolvedTypeNameContributions {
				contribution := resolvedTypeNames.functions[key]
				contribution.body = body
				resolvedTypeNames.functions[key] = contribution
			}
		}
		diags := r.retainedShiftedPreBodyDiagnostics(pairedNew, marks)
		pre = &preBodyState{classification: r.pre.classification, fingerprint: r.pre.fingerprint, global: r.pre.global, resolvedTypeNames: resolvedTypeNames, resolvedTypeNameSourceOwners: fastResolvedTypeNameSourceOwners, resolvedTypeNameRows: fastResolvedTypeNameRows, diags: diags, plainHeaders: r.pre.plainHeaders, plainGlobal: r.pre.plainGlobal, plainInputs: r.pre.plainInputs, plainIdentities: r.pre.plainIdentities, hasProofReport: r.pre.hasProofReport}
		r.LastStats.FastPrepared = true
	} else if prepared.AcceptsScratch(scratch) {
		if err := prepared.materialize(); err != nil {
			return nil, nil, fmt.Errorf("scratch parse failed: %v", err)
		}
		if prepared.file == nil || prepared.file == file {
			prepared.reject(fmt.Errorf("scratch parse must be independent of the primary file"))
			return nil, nil, fmt.Errorf("scratch parse must be independent of the primary file")
		}
		scratch = prepared.file
		prepared.Wait()
		if prepared.parseErr != nil || prepared.pre == nil {
			return nil, nil, fmt.Errorf("scratch preparation failed: %v", prepared.parseErr)
		}
		pre = prepared.pre
		r.LastStats.ScratchPrepared = true
		r.LastStats.Scratch = prepared.elapsed
	} else {
		if scratch == nil || scratch == file {
			return nil, nil, fmt.Errorf("missing independent scratch parse")
		}
		pendingRetainedMu.Lock()
		scratchStart := time.Now()
		pre = scratchPreBody(scratch, r.options)
		r.LastStats.Scratch = time.Since(scratchStart)
		pendingRetainedMu.Unlock()
	}
	return pre, scratch, nil
}
