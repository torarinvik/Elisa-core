//go:build cgo

package backend

import "elisacore/src/ast"

// With no previous partition membership, estimate work from the source span of
// each owned function. This changes scheduling only. A large body should start
// before the tail of small partitions even if its symbol hashes to a late slot.
func partitionIRWorkWeights(worklists [][]ast.Decl) []int {
	weights := make([]int, len(worklists))
	for part, decls := range worklists {
		weights[part] = partitionIRDeclWeight(decls)
	}
	return weights
}

func partitionIRFunctionWeight(fn *ast.FuncDecl) int {
	if len(fn.Body) == 0 {
		return 0
	}
	lines := fn.Body[len(fn.Body)-1].Pos().Line - fn.Pos().Line + 1
	return max(lines, len(fn.Body))
}

func partitionIRDeclWeight(decls []ast.Decl) int {
	weight := 0
	for _, decl := range decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			weight += partitionIRFunctionWeight(node)
		case *ast.NamespaceDecl:
			weight += partitionIRDeclWeight(node.Decls)
		case *ast.StaticIfDecl:
			weight += partitionIRDeclWeight(node.Then) + partitionIRDeclWeight(node.Else)
			for _, branch := range node.Elifs {
				weight += partitionIRDeclWeight(branch.Body)
			}
		case *ast.ImplDecl:
			for _, member := range node.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					weight += partitionIRFunctionWeight(fn)
				}
			}
		}
	}
	return weight
}
