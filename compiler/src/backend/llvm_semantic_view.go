//go:build cgo

package backend

import "elisacore/src/semantic"

// A semantic view has its own proof authority and synthesized node overlays.
// Immutable global/type indexes are shared once, not copied per specialization.
func (g *llvmGenerator) installCodegenResult(next *semantic.Result) func() {
	if next == nil || next == g.result {
		return func() {}
	}
	previousResult, previousLookups := g.result, g.sharedLookups
	previousSymbols := g.symbolsByNode
	previousExpr, previousBounds := g.exprOverlay, g.boundsOverlay
	previousSpans, previousSpanSize := g.provenIndexSpans, g.provenIndexSpansSize
	previousSpanResult := g.provenIndexSpansResult
	root := next.CanonicalCodegenResult()
	rootReads := newIRCodegenReadView(root, g.inputReads)
	program := g.canonicalLookups
	if program == nil || program.result != root {
		if previousLookups != nil && previousLookups.programLookups().result == root {
			program = previousLookups.programLookups()
		} else {
			program = &sharedCodegenLookups{result: root, symbolsByNode: previousSymbols, namedCount: rootReads.NamedTypesCount("canonical-result-view-named-type-count"), symbolCount: rootReads.GlobalSymbolsCount("canonical-result-view-global-symbol-count"), proofCount: root.IndexProofSpanCount()}
		}
		g.canonicalLookups = program
	}
	g.result = next
	g.sharedLookups = program
	if next != root {
		nextReads := newIRCodegenReadView(next, g.inputReads)
		g.sharedLookups = &sharedCodegenLookups{program: program, result: next, symbolsByNode: program.symbolsByNode, namedCount: nextReads.NamedTypesCount("specialized-result-view-named-type-count"), symbolCount: nextReads.GlobalSymbolsCount("specialized-result-view-global-symbol-count"), proofCount: next.IndexProofSpanCount()}
	}
	g.symbolsByNode = program.symbolsByNode
	g.exprOverlay, g.boundsOverlay = nil, nil
	g.provenIndexSpans, g.provenIndexSpansSize, g.provenIndexSpansResult = nil, 0, nil
	return func() {
		g.result, g.sharedLookups = previousResult, previousLookups
		g.symbolsByNode = previousSymbols
		g.exprOverlay, g.boundsOverlay = previousExpr, previousBounds
		g.provenIndexSpans, g.provenIndexSpansSize, g.provenIndexSpansResult = previousSpans, previousSpanSize, previousSpanResult
	}
}
