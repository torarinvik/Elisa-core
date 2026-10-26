package semantic

import (
	"elisacore/src/ast"
	"slices"
)

// A specialization owns its summary headers and outer map, while sharing the
// settled connectivity matrices and kind slices until a component is solved.
// The solver receives private kind slices through detachStoreFlowKindsForWrite.
func cloneStoreFlowSummariesForSpecialization(source map[*ast.FuncDecl]*storeFlowSummary) map[*ast.FuncDecl]*storeFlowSummary {
	if source == nil {
		return nil
	}
	out := make(map[*ast.FuncDecl]*storeFlowSummary, len(source))
	aliases := make(map[*storeFlowSummary]*storeFlowSummary, len(source))
	for decl, summary := range source {
		if summary == nil {
			out[decl] = nil
			continue
		}
		private := aliases[summary]
		if private == nil {
			copy := *summary
			private = &copy
			aliases[summary] = private
		}
		out[decl] = private
	}
	return out
}

// detachStoreFlowKindsForWrite gives every summary in a component private kind
// backing before storeFlowSolveKinds runs. The solver can touch initialized and
// uninitialized members, so copy all kind rows in the component.
func detachStoreFlowKindsForWrite(summaries map[*ast.FuncDecl]*storeFlowSummary, members []*ast.FuncDecl) {
	seen := make(map[*storeFlowSummary]bool, len(members))
	for _, member := range members {
		summary := summaries[member]
		if summary == nil || seen[summary] {
			continue
		}
		seen[summary] = true
		summary.retKind = slices.Clone(summary.retKind)
		summary.reachKind = slices.Clone(summary.reachKind)
		for i, row := range summary.reachKind {
			summary.reachKind[i] = slices.Clone(row)
		}
	}
}
