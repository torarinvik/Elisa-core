package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/statecache"
)

// Optional typed allocation hints for common graph slices. The registry still
// derives its complete schema from semanticStateTypes; unlisted slices use the
// general checked allocator with identical serialization and alias rules.
func registerSemanticSliceAllocators(r *statecache.Registry) {
	statecache.RegisterTypedSlice[[]string](r)
	statecache.RegisterTypedSlice[[]bool](r)
	statecache.RegisterTypedSlice[[]byte](r)
	statecache.RegisterTypedSlice[[]int](r)
	statecache.RegisterTypedSlice[[]int64](r)
	statecache.RegisterTypedSlice[[]uint32](r)
	statecache.RegisterTypedSlice[[]uint64](r)
	statecache.RegisterTypedSlice[[]any](r)
	statecache.RegisterTypedSlice[[]ast.Expr](r)
	statecache.RegisterTypedSlice[[]ast.Stmt](r)
	statecache.RegisterTypedSlice[[]ast.Decl](r)
	statecache.RegisterTypedSlice[[]ast.Node](r)
	statecache.RegisterTypedSlice[[]ast.PermissionRef](r)
	statecache.RegisterTypedSlice[[]*ast.FuncDecl](r)
	statecache.RegisterTypedSlice[[]lexer.Pos](r)
	statecache.RegisterTypedSlice[[]Type](r)
	statecache.RegisterTypedSlice[[]FactClass](r)
	statecache.RegisterTypedSlice[[]FlowEdge](r)
	statecache.RegisterTypedSlice[[]FactTransform](r)
	statecache.RegisterTypedSlice[[]CFGBlock](r)
	statecache.RegisterTypedSlice[[]*Symbol](r)
}
