package semantic

import "elisacore/src/lexer"

// refreshRetainedFactPositions updates copied source locations in an unchanged
// function's analysis, then renders its exit report from the structured facts.
// CFG nodes share the already shifted AST and must not be walked again.
func (a *Analyzer) refreshRetainedFactPositions(fnAnalysis *FunctionAnalysis, marks map[string][][2]int) {
	if fnAnalysis == nil {
		return
	}
	seen := map[*lexer.Pos]bool{}
	shift := func(pos *lexer.Pos) {
		if !seen[pos] {
			*pos = retainedShiftPos(*pos, marks)
			seen[pos] = true
		}
	}
	transforms := func(facts []FactTransform) {
		for i := range facts {
			shift(&facts[i].SourcePos)
		}
	}
	transforms(fnAnalysis.FactTransforms)
	for i := range fnAnalysis.BlockFactTransforms {
		transforms(fnAnalysis.BlockFactTransforms[i].Transforms)
	}
	if fnAnalysis.CFG != nil {
		for i := range fnAnalysis.CFG.Blocks {
			block := &fnAnalysis.CFG.Blocks[i]
			transforms(block.FactTransforms)
			shift(&block.Terminator.Position)
			for j := range block.Instrs {
				shift(&block.Instrs[j].Position)
			}
		}
	}
	fnAnalysis.FactExitSummary = buildFunctionFactExitSummary(fnAnalysis.FactTransforms)
}
