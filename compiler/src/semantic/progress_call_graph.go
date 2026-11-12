package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/graphalgo"
	"elisacore/src/progresscalls"
)

// Retain the checkpoint registry identity for previously encoded schemas.
type progressCallCollector struct{ calls []string }

func collectProgressDirectCalls(stmts []ast.Stmt) []string {
	return progresscalls.CollectDirectCalls(stmts)
}

func progressTarjan(names map[*ast.FuncDecl]string, edges map[*ast.FuncDecl]map[*ast.FuncDecl]bool) [][]*ast.FuncDecl {
	ordered := sortedFuncDeclKeys(names)
	indices := make(map[*ast.FuncDecl]int, len(ordered))
	for i, fn := range ordered {
		indices[fn] = i
	}
	deps := make([][]int, len(ordered))
	for i, fn := range ordered {
		for _, callee := range sortedFuncDeclKeys(edges[fn]) {
			if index, ok := indices[callee]; ok {
				deps[i] = append(deps[i], index)
			}
		}
	}
	components := graphalgo.Components(deps, false)
	var result [][]*ast.FuncDecl
	for _, component := range components {
		group := make([]*ast.FuncDecl, 0, len(component))
		for _, index := range component {
			group = append(group, ordered[index])
		}
		result = append(result, group)
	}
	return result
}
