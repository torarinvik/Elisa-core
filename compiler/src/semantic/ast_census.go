package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/astscan"
)

type analyzerASTCensus struct {
	exprs        int
	funcDecls    int
	parallelFors int
}

func analyzeASTCensus(file *ast.File) analyzerASTCensus {
	counts := astscan.CountForAllocation(file)
	return analyzerASTCensus{exprs: counts.Exprs, funcDecls: counts.FuncDecls, parallelFors: counts.ParallelFors}
}
