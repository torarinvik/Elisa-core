package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/regionforwardscan"
	"elisacore/src/regionframesyntax"
)

// collectRegionParamForwarding summarizes syntactic parameter-to-call forwarding once per
// function, recording reverse dependencies in the same walk when requested. The inference fixpoint can revisit every function multiple times as callees gain
// inferred region parameters, so reflect-walking the body in each pass made region-heavy inputs
// scale with both AST size and fixpoint iterations.
func collectRegionParamForwarding(cands []*ast.FuncDecl, funcByName map[string]*ast.FuncDecl, callers map[*ast.FuncDecl][]*ast.FuncDecl) map[*ast.FuncDecl]regionParamForwarding {
	byFunc := make(map[*ast.FuncDecl]regionParamForwarding, len(cands))
	for _, fn := range cands {
		if fn == nil || len(fn.Body) == 0 || (len(fn.Params) == 0 && callers == nil) {
			continue
		}
		var forwarding regionParamForwarding
		noteCaller := func(callee *ast.FuncDecl) {
			if callers == nil || callee == nil {
				return
			}
			previous := callers[callee]
			if len(previous) == 0 || previous[len(previous)-1] != fn {
				callers[callee] = append(previous, fn)
			}
		}
		regionforwardscan.Scan(fn, funcByName, containerGrowthMethods, walkStoreScanNodes, callers != nil, noteCaller, func(name string) {
			if forwarding.grownContainers == nil {
				forwarding.grownContainers = make(map[string]bool)
			}
			forwarding.grownContainers[name] = true
		}, func(name string, isField bool, callee *ast.FuncDecl, argPos int) {
			target := regionForwardTarget{callee: callee, argPos: argPos}
			if isField {
				if forwarding.fields == nil {
					forwarding.fields = make(map[string][]regionForwardTarget)
				}
				forwarding.fields[name] = appendUniqueRegionForwardTarget(forwarding.fields[name], target)
			} else {
				if forwarding.direct == nil {
					forwarding.direct = make(map[string][]regionForwardTarget)
				}
				forwarding.direct[name] = appendUniqueRegionForwardTarget(forwarding.direct[name], target)
			}
		})
		if forwarding.direct != nil || forwarding.fields != nil || forwarding.grownContainers != nil {
			byFunc[fn] = forwarding
		}
	}
	return byFunc
}

func appendUniqueRegionForwardTarget(targets []regionForwardTarget, target regionForwardTarget) []regionForwardTarget {
	for _, existing := range targets {
		if existing == target {
			return targets
		}
	}
	return append(targets, target)
}

// regionForwardedParamPath recognizes either a bare parameter forwarded through address-of/casts,
// or a field/index path rooted at one. A field path is kept separate so only struct parameters use
// field forwarding to infer their enclosing reference region.
func regionForwardedParamPath(arg ast.Expr) (string, bool, bool) {
	return regionframesyntax.RegionForwardedParamPath(arg)
}

func (a *Analyzer) forwardTargetsRequireRegion(targets []regionForwardTarget) bool {
	for _, target := range targets {
		callee := target.callee
		// A callee without region params can never require one; skip expanding (copying) its
		// parameter list. Any duplicate-parameter diagnostic that expansion reports was already
		// reported when the callee's signature was collected, before any body is analyzed.
		if callee == nil || target.argPos < 0 || len(callee.RegionParams) == 0 {
			continue
		}
		param, ok := a.expandedFuncDeclParamAt(callee, target.argPos)
		if !ok {
			continue
		}
		regionParams := make(map[string]bool, len(callee.RegionParams))
		for _, rp := range callee.RegionParams {
			regionParams[rp] = true
		}
		if len(regionParams) != 0 && typeExprCarriesRegionParam(param.Type, regionParams) {
			return true
		}
	}
	return false
}

// forwardsParamIdent reports whether an argument expression passes the parameter `name` through: a bare
// identifier, `&name` (address-of), or a reborrow reinterpret cast `(&name).cast[T&]` — peeling parens,
// address-of, and casts (S4 Stage 3: the loader's pervasive `(&self).cast[mutable T&]` reborrow idiom).
// This only decides region-poly inference; the region is still verified at the real call site (where
// the cast now preserves the operand's region), so seeing through the cast here is sound.
func forwardsParamIdent(arg ast.Expr, name string) bool {
	return regionframesyntax.ForwardsParamIdent(arg, name)
}
func typeExprCarriesRegionParam(t ast.TypeExpr, params map[string]bool) bool {
	return regionframesyntax.TypeExprCarriesRegionParam(t, params)
}

// inferReturnViewRegion ties a function's region-less `view[T]` return type to an inferred region
// param when the body provably returns a slice (`param[a:b]`) of that param's container — `def
// head(out: mutable darray[u8]&, n) -> view[u8]: out.push(..); return out[0:n]`. Without this the
// returned view is region-less, so a caller storing it past the source region's death is not caught
// (a use-after-free — the same hole the explicit `-> view[u8] @r` form now closes). Stamping the
// return type's region makes the call-site substitution resolve it to the caller's region, where the
// existing escape checker proves the borrow cannot outlive its backing.
//
// Tightly scoped for soundness: only when EXACTLY ONE region param was inferred (so the source
// region is unambiguous), the declared return type is a region-less view, and a `return param[...]`
// over the matching grown param is present. A returned view into a dying LOCAL is already rejected
// upstream, so a function reaching here with a compiling body returns a view backed by the param's
// (caller-provided) region — exactly the region we stamp.
func inferReturnViewRegion(fn *ast.FuncDecl, inferred map[string]string) {
	regionframesyntax.InferReturnViewRegion(fn, inferred)
}
func functionReturnsSliceOfParam(stmts []ast.Stmt, name string) bool {
	return regionframesyntax.FunctionReturnsSliceOfParam(stmts, name)
}

func regionlessRefContainer(typ ast.TypeExpr) (*string, bool) {
	return regionframesyntax.RegionlessRefContainer(typ)
}

func regionlessRefStruct(typ ast.TypeExpr) (*string, bool) {
	return regionframesyntax.RegionlessRefStruct(typ)
}

// refParamIsPackedEnum reports whether a parameter type is a by-reference (`&`) region-backed PACKED
// enum (a handle-tree node type like `Ast::Expr`) with no explicit region already on the ref. Peels the
// mutable/owned/ref qualifiers, resolves the referent, and checks it is a packed EnumType (or a packed
// enum root). Used to restrict the scalar-out-param region-thread to the handle-enum UAF class — an
// ordinary struct out-param (no region-backed storage) is left to the existing field-grown/forwarded
// triggers. Conservative: an unresolved/non-enum type returns false (no thread, prior behavior).
func (a *Analyzer) refParamIsPackedEnum(typ ast.TypeExpr) bool {
	var ref *ast.RefType
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.OwnedType:
			typ = t.Elem
		case *ast.RefType:
			if t.Region != "" {
				return false
			}
			ref = t
			typ = t.Elem
		default:
			if ref == nil {
				return false
			}
			// These builtin surface forms resolve to container types or invalid,
			// never to an enum. Their elements cannot change that classification.
			if builtin, ok := typ.(*ast.BuiltinTypeExpr); ok {
				switch builtin.Name {
				case "array", "darray", "dict", "set", "view", "cstr":
					return false
				}
			}
			// Resolve under diagnostic suppression: this runs at the pre-body classification pass where a
			// generic type-param name (`T`/`K` from an enclosing `extend[T]`) is not yet in scope, so a
			// bare resolveType would emit a spurious "unknown type" error. We only need the boolean.
			savedSuppress := a.suppressDiagnostics
			restoreReportProbe2 := a.beginProofReportProbe()
			defer restoreReportProbe2.restore()
			a.suppressDiagnostics = true
			resolved := a.resolveType(typ)
			a.suppressDiagnostics = savedSuppress
			restoreReportProbe2.restore()
			et, ok := resolved.(*EnumType)
			return ok && et != nil && (et.Packed || et.Root().Packed)
		}
	}
}
