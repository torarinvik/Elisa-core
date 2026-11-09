package semantic

// storeFlowRetKind / storeFlowReachKind read an edge kind for a consumer. storeFlowKindsOff
// (tests only) reads every edge as Header: the monotonicity check compares verdicts with and
// without the element refinement.
func (a *Analyzer) storeFlowRetKind(sum *storeFlowSummary, i int) storeFlowEdgeKind {
	if a.storeFlowKindsOff {
		return storeFlowEdgeHeader
	}
	return sum.retKindOf(i)
}

func (a *Analyzer) storeFlowReachKind(sum *storeFlowSummary, other, index int) storeFlowEdgeKind {
	if a.storeFlowKindsOff {
		return storeFlowEdgeHeader
	}
	return sum.reachKindOf(other, index)
}
