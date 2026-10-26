package semantic

import (
	"elisacore/src/ast"
	"slices"
	"unsafe"
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
	// Keep small and heavily aliased catalogs on individual headers. Once a
	// clone has many distinct headers, bounded blocks avoid one allocation per
	// function without another catalog traversal or a large unused reservation.
	const headersPerBlock = max(1, (16<<10)/int(unsafe.Sizeof(storeFlowSummary{})))
	var headers []storeFlowSummary
	for decl, summary := range source {
		if summary == nil {
			out[decl] = nil
			continue
		}
		private := aliases[summary]
		if private == nil {
			if len(aliases) < 64 {
				copy := *summary
				private = &copy
			} else {
				if len(headers) == 0 {
					headers = make([]storeFlowSummary, min(headersPerBlock, len(source)-len(aliases)))
				}
				headers[0] = *summary
				private = &headers[0]
				headers = headers[1:]
			}
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
