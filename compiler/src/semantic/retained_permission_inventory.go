package semantic

import "elisacore/src/ast"

// Invocation-local source-order membership only. Construct after Update's final
// structural reindex and discard after its body/summary replay scope. Rows, masks
// and FuncTypes remain live and are read anew in every nested caller replay.
// Neither inventory nor its ordinal map is published or retained across updates.
type retainedPermissionInventory struct {
	functions []scopedDecl
	first     map[*ast.FuncDecl]int
}

func newRetainedPermissionInventory(decls []scopedDecl) *retainedPermissionInventory {
	functions := permissionFunctionDecls(decls)
	first := make(map[*ast.FuncDecl]int, len(functions))
	for i, scoped := range functions {
		fn := scoped.Decl.(*ast.FuncDecl)
		if _, exists := first[fn]; !exists {
			first[fn] = i
		}
	}
	return &retainedPermissionInventory{functions: functions, first: first}
}
