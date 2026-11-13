package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/astprunedscan"
)

// ParamContainerNeedsRegion reports whether direct growth or a fresh-backing
// assignment needs the parameter's region. Program-lifetime store scopes suppress
// only growth; assignments inside them still need to be inspected.
func ParamContainerNeedsRegion(stmts []ast.Stmt, name string, permRoots, growthMethods map[string]bool, rootName func(ast.Expr) string) bool {
	found := false
	visitAssignment := func(node any) bool {
		if found {
			return false
		}
		if asn, ok := node.(*ast.AssignStmt); ok && asn != nil && !asn.Optional {
			if id, ok := asn.Target.(*ast.Ident); ok && id != nil && id.Name == name && ReassignmentAllocatesFreshBacking(asn.Value) {
				found = true
				return false
			}
		}
		return true
	}
	astprunedscan.WalkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		if in, ok := node.(*ast.InStoreStmt); ok && in != nil && permRoots[rootName(in.Store)] {
			// Inspect the entire store expression too: it may contain an expression block.
			astprunedscan.WalkPruned(in, visitAssignment)
			return false
		}
		if call, ok := node.(*ast.CallExpr); ok && call != nil {
			if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && growthMethods[field.Field] {
				if id, ok := field.Object.(*ast.Ident); ok && id != nil && id.Name == name {
					found = true
					return false
				}
			}
		}
		return visitAssignment(node)
	})
	return found
}

// ParamContainerGrownNeedingRegion excludes growth under program-lifetime
// stores. It is also used to fingerprint the retained-analysis fast path.
func ParamContainerGrownNeedingRegion(stmts []ast.Stmt, name string, permRoots, growthMethods map[string]bool, rootName func(ast.Expr) string) bool {
	found := false
	astprunedscan.WalkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		if in, ok := node.(*ast.InStoreStmt); ok && in != nil && permRoots[rootName(in.Store)] {
			return false
		}
		if call, ok := node.(*ast.CallExpr); ok && call != nil {
			if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && growthMethods[field.Field] {
				if id, ok := field.Object.(*ast.Ident); ok && id != nil && id.Name == name {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// ParamContainerReassignedFromLiteral tracks the separate retained fingerprint
// for fresh backing assignments, including those inside a program-lifetime store.
func ParamContainerReassignedFromLiteral(stmts []ast.Stmt, name string) bool {
	found := false
	astprunedscan.WalkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		if asn, ok := node.(*ast.AssignStmt); ok && asn != nil && !asn.Optional {
			if id, ok := asn.Target.(*ast.Ident); ok && id != nil && id.Name == name && ReassignmentAllocatesFreshBacking(asn.Value) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// ReassignmentAllocatesFreshBacking recognizes the literal forms that allocate
// a new container backing buffer, ignoring parenthesization.
func ReassignmentAllocatesFreshBacking(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	switch value.(type) {
	case *ast.ListLitExpr, *ast.ListComprehensionExpr, *ast.StringLit:
		return true
	}
	return false
}
