package semantic

import (
	"elisacore/src/ast"
	"maps"
	"slices"
)

// A specialization owns the outer map and reads frozen summary headers until
// a component must be solved. The write barrier detaches headers and kinds.
func cloneStoreFlowSummariesForSpecialization(source map[*ast.FuncDecl]*storeFlowSummary) map[*ast.FuncDecl]*storeFlowSummary {
	return maps.Clone(source)
}

// detachStoreFlowKindsForWrite gives every summary in a component a private
// header and kind backing before storeFlowSolveKinds runs. The solver can touch
// initialized and uninitialized members, so copy all kind rows.
func detachStoreFlowKindsForWrite(summaries map[*ast.FuncDecl]*storeFlowSummary, members []*ast.FuncDecl) {
	copies := make(map[*storeFlowSummary]*storeFlowSummary, len(members))
	for _, member := range members {
		summary := summaries[member]
		if summary == nil || copies[summary] != nil {
			continue
		}
		private := *summary
		private.retKind = slices.Clone(summary.retKind)
		private.reachKind = slices.Clone(summary.reachKind)
		for i, row := range private.reachKind {
			private.reachKind[i] = slices.Clone(row)
		}
		copies[summary] = &private
	}
	// A summary may be aliased by declarations outside this component. Keep
	// the original alias groups intact within the worker after detaching.
	for decl, summary := range summaries {
		if private := copies[summary]; private != nil {
			summaries[decl] = private
		}
	}
}
