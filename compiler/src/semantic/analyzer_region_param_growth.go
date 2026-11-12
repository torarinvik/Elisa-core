package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/regionframesyntax"
)

// paramFieldContainerIsGrown reports whether the body contains a growth-method call whose receiver is
// a FIELD of the named struct parameter — `param.field.push(...)`. Mirrors paramContainerIsGrown but
// peels one field access (the container lives inside the struct). A false negative only forgoes the
// ergonomic rewrite (the old "active region" error still fires, which is safe); a false positive only
// threads an unused region param.
func paramFieldContainerIsGrown(stmts []ast.Stmt, name string) bool {
	return regionframesyntax.ParamFieldContainerIsGrown(stmts, name, isCompilerGeneratedAutoRegionStmt, containerGrowthMethods, rootIdentExpr)
}

// paramStoresBodyLocal reports whether the body may store a value built by this body (a value
// mentioning a body-declared local) into storage reached through the struct param `name`: a
// field assignment `p.f <- local`, a method call on a p-rooted receiver taking a local
// (`p.items.push(local)`), or a call passing both p and a local (the callee may store one into the
// other). Such a value is allocated in the function's ambient region, so the param's region must
// be the ambient one. Over-approximating is safe (adoption only lengthens lifetimes); it is
// restricted so read-only bodies keep their own scratch region.
func paramStoresBodyLocal(fn *ast.FuncDecl, name string) bool {
	locals := map[string]bool{}
	walkStoreScanNodes(fn.Body, func(node any) {
		if v, ok := node.(*ast.VarDeclStmt); ok && v != nil && v.Name != name && localMayOwnContainer(v) {
			locals[v.Name] = true
		}
	})
	if len(locals) == 0 {
		return false
	}
	target := map[string]bool{name: true}
	found := false
	walkStoreScanNodes(fn.Body, func(node any) {
		if found {
			return
		}
		switch n := node.(type) {
		case *ast.AssignStmt:
			if n == nil {
				return
			}
			if _, bare := n.Target.(*ast.Ident); bare {
				return
			}
			if root := rootIdentExpr(n.Target); root != nil && root.Name == name && storeScanMentions(n.Value, locals) {
				found = true
			}
		case *ast.CallExpr:
			if n == nil {
				return
			}
			mentionsLocal := false
			for _, arg := range n.Args {
				if storeScanMentions(arg, locals) {
					mentionsLocal = true
				}
			}
			if !mentionsLocal {
				return
			}
			if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil {
				if root := rootIdentExpr(field.Object); root != nil && root.Name == name {
					found = true
					return
				}
			}
			for _, arg := range n.Args {
				if storeScanMentions(arg, target) {
					found = true
					return
				}
			}
		}
	})
	return found
}

// localMayOwnContainer: a body local whose declared type (or, untyped, whose initializer) may own
// ambient-allocated container storage -- a container, a named/generic aggregate, or a literal /
// call-built value. Scalars, pointers and references allocate nothing in the ambient region.
func localMayOwnContainer(v *ast.VarDeclStmt) bool {
	return regionframesyntax.LocalMayOwnContainer(v, storeScanUnwrap)
}
func paramIsWritableStructRef(p *ast.ParamDecl) bool {
	return regionframesyntax.ParamIsWritableStructRef(p)
}

// hasOtherFreshFieldTarget reports whether fn has a by-reference parameter other than name.
// Binding the ambient region to one struct param is only sound when it is the single store
// target reachable through a reference; any other reference param could receive a value
// allocated in the adopted region, so the binding is withheld (conservative).
func (a *Analyzer) hasOtherFreshFieldTarget(fn *ast.FuncDecl, name string) bool {
	for i := range fn.Params {
		if fn.Params[i].Name == name {
			continue
		}
		// Only a MUTABLE reference can receive a stored value; a read-only `darray[T]&` is never a
		// second target (same rule as containerParamCount).
		if paramIsWritableStructRef(&fn.Params[i]) {
			return true
		}
	}
	return false
}

// paramFieldAssignedFreshContainer reports whether the body replaces a field of the named struct
// param with a container allocated in this body: `p.f <- []`, `p.f <- [x for ...]`, or
// `p.f <- fresh` where `fresh` is a body local initialised from such a literal (the rehash
// shape). The fresh backing must live in the caller's region, not the per-call auto region, so
// the param needs a threaded region that the function's ambient allocations adopt. Syntactic
// and deliberately narrow: a scalar field assignment never matches, and funcByName rules out
// callees that return a scalar.
func paramFieldAssignedFreshContainer(stmts []ast.Stmt, name string, funcByName map[string]*ast.FuncDecl) bool {
	freshLocals := map[string]bool{}
	var assigns []*ast.AssignStmt
	walkPruned(stmts, func(node any) bool {
		switch n := node.(type) {
		case *ast.InStoreStmt:
			return false
		case *ast.RegionStmt:
			if !isCompilerGeneratedAutoRegionStmt(n) {
				return false
			}
		case *ast.VarDeclStmt:
			if _, isStr := n.Value.(*ast.StringLit); n != nil && n.Name != "" && !isStr && reassignmentAllocatesFreshBacking(n.Value) {
				freshLocals[n.Name] = true
			}
			// A struct-literal local (`local: Inner = Inner{xs: []}`) owns fresh container
			// fields that the body then grows; storing it (or one of its fields) into the
			// param must build those fields in the caller's region too.
			if _, isLit := stripParenExpr(n.Value).(*ast.StructLitExpr); n != nil && n.Name != "" && isLit {
				freshLocals[n.Name] = true
			}
		case *ast.AssignStmt:
			if n != nil && !n.Optional {
				assigns = append(assigns, n)
			}
		}
		return true
	})
	for _, asn := range assigns {
		field, ok := asn.Target.(*ast.FieldExpr)
		if !ok || field == nil {
			continue
		}
		if root := rootIdentExpr(field); root == nil || root.Name != name {
			continue
		}
		value := asn.Value
		for {
			paren, ok := value.(*ast.ParenExpr)
			if !ok {
				break
			}
			value = paren.Inner
		}
		if id, ok := value.(*ast.Ident); ok && id != nil && freshLocals[id.Name] {
			return true
		}
		// `o.inner.xs <- local.xs`: a field path of a fresh local carries its fresh backing.
		if field, ok := value.(*ast.FieldExpr); ok && field != nil {
			if root := rootIdentExpr(field); root != nil && freshLocals[root.Name] {
				return true
			}
		}
		if _, isStruct := value.(*ast.StructLitExpr); isStruct {
			return true
		}
		// `flow.normal <- snapshot(names)`: a region-polymorphic factory builds its result in
		// the ambient region, which must then be the param's (caller's) region, not this
		// body's auto region freed at return. A non-polymorphic callee is unaffected. Only a
		// free-function FACTORY qualifies: a call that reads the param (`p.f <- p.f.push(v)`,
		// `p.f <- grow(p.f)`) rebinds existing storage and builds nothing fresh.
		// A callee returning a scalar (`p.head <- index_of(p, 1)`) builds no container either.
		if call, isCall := value.(*ast.CallExpr); isCall && call != nil {
			if callee, isFree := call.Func.(*ast.Ident); isFree {
				if fd := funcByName[callee.Name]; fd != nil && storeFlowScalarTypeExpr(fd.ReturnType) {
					continue
				}
				return true
			}
		}
		if _, isStr := value.(*ast.StringLit); !isStr && reassignmentAllocatesFreshBacking(value) {
			return true
		}
	}
	return false
}

// paramContainerIsGrown reports whether the body contains a growth-method call (push/extend/…)
// whose receiver is the named parameter — `param.push(...)`. A false negative only forgoes the
// ergonomic rewrite (the "has no region to grow into" error still fires, which is
// safe); a false positive only threads an unused region param. Scans structurally via reflection,
// mirroring bodyCallsStoreNeedingOutsideRegion.
func paramContainerIsGrown(stmts []ast.Stmt, name string) bool {
	return regionframesyntax.ParamContainerIsGrown(stmts, name, containerGrowthMethods)
}

// containerParamGrownWithRegionValue reports whether the body grows the named container parameter
// (`name.push(v)` / .extend / .insert / …) with a value whose RESOLVED TYPE carries region-backed
// storage — a packed-enum node or a nested container. Such an insert moves a region-allocated value
// into a CALLER-OWNED container, so the value's backing must live in the param's region or it dangles
// when this function returns (the void-grower use-after-free: a region-poly callee's payload darray is
// pushed into `out` but lands in a grower-local region freed on return). Stamping `__rg_<param>` makes
// the param's region threadable; the backend then binds the grower's ambient allocation region to it so
// the inserted value's region-poly producer allocates into the param's region (and is adopted with it).
//
// PRE-CLASSIFICATION-SAFE: decides purely from RESOLVED TYPES (callee return types, local binding
// types, packed-enum constructors), NEVER region-poly classification — which runs after this pass
// (analyzer.go: inferRegionParamsForGrownContainerParams precedes classifyRegionPolymorphicFunctions).
// Traces an inserted ident through its var-decl / assignment / `if EXPR is x` refinement binding to its
// producing expression. Conservative both ways: an unresolved insert simply doesn't trigger (the prior
// behavior, sound), and over-triggering only threads a region the caller — which owns the container —
// can always supply.
func (a *Analyzer) containerParamGrownWithRegionValue(fn *ast.FuncDecl, name string, funcByName map[string]*ast.FuncDecl, funcScopes map[*ast.FuncDecl]scopedDecl) bool {
	if fn == nil {
		return false
	}
	var baseRegionValued func(ast.Expr) bool
	var structLitLocals map[string]bool
	argIsRegionValued := func(arg ast.Expr) bool {
		if id, ok := stripParenExpr(arg).(*ast.Ident); ok && id != nil {
			// A struct literal can give a local container fields with fresh backing.
			// Only an inserted identifier needs the local declaration index.
			if structLitLocals == nil {
				structLitLocals = map[string]bool{}
				walkStoreScanNodes(fn.Body, func(node any) {
					if vd, ok := node.(*ast.VarDeclStmt); ok && vd != nil && vd.Name != "" {
						if _, isLit := stripParenExpr(vd.Value).(*ast.StructLitExpr); isLit {
							structLitLocals[vd.Name] = true
						}
					}
				})
			}
			if structLitLocals[id.Name] {
				return true
			}
		}
		if baseRegionValued == nil {
			baseRegionValued = a.regionValueTester(fn, funcByName, funcScopes)
		}
		return baseRegionValued(arg)
	}

	// Scan for `name.<growth>(ARG…)` where any inserted ARG is region-valued.
	found := false
	walkPruned(fn.Body, func(node any) bool {
		if found {
			return false
		}
		if call, ok := node.(*ast.CallExpr); ok && call != nil {
			if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && containerGrowthMethods[field.Field] {
				if id, ok := field.Object.(*ast.Ident); ok && id != nil && id.Name == name {
					for _, arg := range call.Args {
						if argIsRegionValued(arg) {
							found = true
							return false
						}
					}
				}
			}
		}
		return true
	})
	return found
}
