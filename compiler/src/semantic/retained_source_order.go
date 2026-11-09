package semantic

import "elisacore/src/ast"

// sortRetainedAddedDeclarations restores source order after insertion.
func (r *RetainedAnalysis) sortRetainedAddedDeclarations(a *Analyzer, newDecls []ast.Decl, appendedInto map[*ast.NamespaceDecl]bool, appendedTop bool) {
	// Code generation emits declarations in list order; a fresh analysis lists them as written.
	if len(appendedInto) != 0 || appendedTop {
		currentOffsets := make(map[retainedDeclPosition]int, len(newDecls))
		for _, decl := range newDecls {
			pos := decl.Pos()
			currentOffsets[retainedDeclPosition{pos.File, pos.Line, pos.Col}] = pos.Offset
		}
		for ns := range appendedInto {
			retainedSortDeclsBySource(ns.Decls, currentOffsets)
		}
		if appendedTop {
			retainedSortDeclsBySource(a.file.Decls, currentOffsets)
		}
	}
}
