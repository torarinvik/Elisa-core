package semantic

import (
	"elisacore/src/ast"
	"slices"
)

// These bound allocation shape only. Unsupported/large shapes execute the old
// complete copy, never a different semantic result or a reduced flow budget.
const specializationSummaryPoolMinObjects = 256
const specializationSummaryPoolObjects = 1 << 16
const specializationSummaryPoolKindElements = 1 << 24
const specializationSummaryPoolRowHeaders = 1 << 20

// Keep the ordinary helper as the independent exact policy and fallback. Every
// kind cell remains private; only allocation grouping changes for constructors.
func cloneStoreFlowSummariesForSpecialization(source map[*ast.FuncDecl]*storeFlowSummary) map[*ast.FuncDecl]*storeFlowSummary {
	// Small maps are faster on the original path; pooling targets object count.
	if len(source) < specializationSummaryPoolMinObjects {
		return cloneStoreFlowSummariesForBodyWorker(source)
	}
	result, ok := tryCloneStoreFlowSummaryPool(source)
	if !ok {
		return cloneStoreFlowSummariesForBodyWorker(source)
	}
	return result
}

func tryCloneStoreFlowSummaryPool(source map[*ast.FuncDecl]*storeFlowSummary) (map[*ast.FuncDecl]*storeFlowSummary, bool) {
	if source == nil {
		return nil, true
	}
	if len(source) > specializationSummaryPoolObjects {
		return nil, false
	}
	indexes := make(map[*storeFlowSummary]int, len(source))
	unique := make([]*storeFlowSummary, 0, len(source))
	kindLengths, outerLengths := map[int]int{}, map[int]int{}
	kindCount, outerCount := 0, 0
	addLength := func(length int, lengths map[int]int, count *int, limit int) bool {
		if length == 0 {
			return true
		}
		if length > limit-*count {
			return false
		}
		*count += length
		if lengths != nil {
			lengths[length] = 0
		}
		return true
	}
	for _, summary := range source {
		if summary == nil {
			continue
		}
		if _, exists := indexes[summary]; exists {
			continue
		}
		indexes[summary] = len(unique)
		unique = append(unique, summary)
		if !addLength(len(summary.retKind), kindLengths, &kindCount, specializationSummaryPoolKindElements) || !addLength(len(summary.reachKind), outerLengths, &outerCount, specializationSummaryPoolRowHeaders) {
			return nil, false
		}
		for _, row := range summary.reachKind {
			if !addLength(len(row), kindLengths, &kindCount, specializationSummaryPoolKindElements) {
				return nil, false
			}
		}
	}
	// A nonempty slices.Clone may expose rounded spare capacity. Probe the same
	// runtime/element types once per distinct length; do not guess size classes.
	for length := range kindLengths {
		kindLengths[length] = cap(slices.Clone(make([]storeFlowEdgeKind, length)))
	}
	for length := range outerLengths {
		outerLengths[length] = cap(slices.Clone(make([][]storeFlowEdgeKind, length)))
	}
	kindCount, outerCount = 0, 0
	for _, summary := range unique {
		if !addLength(kindLengths[len(summary.retKind)], nil, &kindCount, specializationSummaryPoolKindElements) {
			return nil, false
		}
		if !addLength(outerLengths[len(summary.reachKind)], nil, &outerCount, specializationSummaryPoolRowHeaders) {
			return nil, false
		}
		for _, row := range summary.reachKind {
			if !addLength(kindLengths[len(row)], nil, &kindCount, specializationSummaryPoolKindElements) {
				return nil, false
			}
		}
	}
	summaries := make([]storeFlowSummary, len(unique))
	kinds := make([]storeFlowEdgeKind, kindCount)
	headers := make([][]storeFlowEdgeKind, outerCount)
	nextKind, nextHeader := 0, 0
	copyKinds := func(source []storeFlowEdgeKind) []storeFlowEdgeKind {
		if len(source) == 0 {
			return slices.Clone(source)
		}
		capacity := kindLengths[len(source)]
		target := kinds[nextKind : nextKind+len(source) : nextKind+capacity]
		nextKind += capacity
		copy(target, source)
		return target
	}
	for i, source := range unique {
		target := &summaries[i]
		*target = *source // Complete old shallow value policy for all other fields.
		target.retKind = copyKinds(source.retKind)
		if len(source.reachKind) == 0 {
			target.reachKind = slices.Clone(source.reachKind)
			continue
		}
		capacity := outerLengths[len(source.reachKind)]
		target.reachKind = headers[nextHeader : nextHeader+len(source.reachKind) : nextHeader+capacity]
		nextHeader += capacity
		for row, value := range source.reachKind {
			target.reachKind[row] = copyKinds(value)
		}
	}
	out := make(map[*ast.FuncDecl]*storeFlowSummary, len(source))
	for decl, summary := range source {
		if summary == nil {
			out[decl] = nil
		} else {
			out[decl] = &summaries[indexes[summary]]
		}
	}
	return out, true
}
