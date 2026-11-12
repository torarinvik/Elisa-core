package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/astgraph"
)

func firstASTStorageOverlap(decl ast.Decl) string { return astgraph.FirstStorageOverlap(decl) }
func firstASTStorageOverlapWithLimits(decl ast.Decl, maxDepth, maxWork int) string {
	return astgraph.FirstStorageOverlapWithLimits(decl, maxDepth, maxWork)
}
