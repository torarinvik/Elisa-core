package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/astgraph"
	"reflect"
)

var astGraphPkgPath = astgraph.PackagePath

func copyASTGraph(v reflect.Value, memo map[uintptr]reflect.Value) reflect.Value {
	return astgraph.Copy(v, memo)
}
func astCopyNeedsTraversal(t reflect.Type) bool   { return astgraph.NeedsTraversal(t) }
func astCopyTraversalFields(t reflect.Type) []int { return astgraph.TraversalFields(t) }
func copyASTDecl(d ast.Decl) ast.Decl             { return astgraph.CopyDecl(d) }
