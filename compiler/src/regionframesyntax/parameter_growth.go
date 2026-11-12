package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/astprunedscan"
)

func ParamFieldContainerIsGrown(stmts []ast.Stmt, name string, isAuto func(*ast.RegionStmt) bool, growthMethods map[string]bool, rootIdent func(ast.Expr) *ast.Ident) bool {
	found := false
	astprunedscan.WalkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		// A growth nested inside an explicit `in <arena>:` (InStoreStmt) or USER-written
		// local `region:` (RegionStmt) scope already has its allocation region supplied
		// lexically — no region param threading is needed (and forcing `@r` would make the
		// call site demand a region the struct ref doesn't carry). Don't descend into those.
		// BUT a compiler-SYNTHESIZED `__auto_*` region (the parser's speculative
		// maybeWrapFunctionBodyInAutoRegion wrap, e.g. when the body forwards a constructed
		// struct-literal element to a push) is NOT a real allocation region for the param's
		// field: growing a by-ref struct param's field into a per-call auto arena that frees
		// on return is a use-after-free. We MUST see through such a wrap so the field growth
		// stamps `@r` and threads the caller's region. Descend into synthesized regions only.
		switch s := node.(type) {
		case *ast.InStoreStmt:
			return false
		case *ast.RegionStmt:
			if !isAuto(s) {
				return false
			}
			// fall through to descend into the synthesized wrap's body
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if growth, ok := call.Func.(*ast.FieldExpr); ok && growth != nil && growthMethods[growth.Field] {
				// Receiver must be a FIELD/INDEX PATH rooted at the param — `param.f.push(..)`,
				// `param.a.b.push(..)` (any depth), `param.f[i].push(..)`. A path deeper than the
				// bare param means the grown container is a (possibly nested) field of the struct
				// param, so the param carries the region the field backing grows into. (The bare-param
				// case `param.push` is a CONTAINER param, handled by paramContainerIsGrown.)
				// IndexExpr as well as FieldExpr: `param.f[i].push(..)` is a path rooted at
				// the param exactly as `param.f.push(..)` is, and the comment above already
				// promised that shape. Matching only FieldExpr silently failed it — the
				// growth fell through to the "has no region to grow into" diagnostic, which reads
				// as "annotate this" rather than "the compiler missed a case it claims".
				// rootIdentExpr already walks both, so only this guard was narrow. A bare
				// `param.push(..)` still does NOT match (it is neither) and stays with
				// paramContainerIsGrown, which is the container-param case.
				isPath := false
				switch growth.Object.(type) {
				case *ast.FieldExpr, *ast.IndexExpr:
					isPath = true
				}
				if isPath {
					if root := rootIdent(growth.Object); root != nil && root.Name == name {
						found = true
						return false
					}
				}
			}
		}
		return true
	})
	return found
}

func ParamContainerIsGrown(stmts []ast.Stmt, name string, growthMethods map[string]bool) bool {
	found := false
	astprunedscan.WalkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		if call, ok := node.(*ast.CallExpr); ok {
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
