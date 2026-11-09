package semantic

import (
	"elisacore/src/ast"
)

// collectRegionParamForwarding summarizes syntactic parameter-to-call forwarding once per
// function. The inference fixpoint can revisit every function multiple times as callees gain
// inferred region parameters, so reflect-walking the body in each pass made region-heavy inputs
// scale with both AST size and fixpoint iterations.
func collectRegionParamForwarding(cands []*ast.FuncDecl, funcByName map[string]*ast.FuncDecl) map[*ast.FuncDecl]regionParamForwarding {
	byFunc := make(map[*ast.FuncDecl]regionParamForwarding, len(cands))
	for _, fn := range cands {
		if fn == nil || len(fn.Body) == 0 || len(fn.Params) == 0 {
			continue
		}
		forwarding := regionParamForwarding{
			direct: map[string][]regionForwardTarget{},
			fields: map[string][]regionForwardTarget{},
		}
		walkStoreScanNodes(fn.Body, func(node any) {
			// The walker also reaches the call inside any parenthesized
			// expression, so inspect its underlying node only once.
			var expr ast.Expr
			switch n := node.(type) {
			case *ast.CallExpr:
				expr = n
			case *ast.StructLitExpr:
				expr = n
			default:
				return
			}
			calleeName, args, isCall := ast.PrepassCallShape(expr)
			callee := funcByName[calleeName]
			if !isCall || callee == nil {
				return
			}
			for argPos, arg := range args {
				name, isField, ok := regionForwardedParamPath(arg)
				if !ok {
					continue
				}
				// Only declared parameter names are queried by the inference
				// fixpoint; local forwarding cannot affect those queries.
				isParam := false
				for _, param := range fn.Params {
					if param.Name == name {
						isParam = true
						break
					}
				}
				if !isParam {
					continue
				}
				target := regionForwardTarget{callee: callee, argPos: argPos}
				if isField {
					forwarding.fields[name] = appendUniqueRegionForwardTarget(forwarding.fields[name], target)
				} else {
					forwarding.direct[name] = appendUniqueRegionForwardTarget(forwarding.direct[name], target)
				}
			}
		})
		byFunc[fn] = forwarding
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
func regionForwardedParamPath(arg ast.Expr) (name string, isField bool, ok bool) {
	for {
		switch e := unwrapParenForRegionPoly(arg).(type) {
		case *ast.AddrOfExpr:
			if e == nil {
				return "", false, false
			}
			arg = e.Operand
		case *ast.CastExpr:
			if e == nil {
				return "", false, false
			}
			arg = e.Operand
		default:
			goto inspectPath
		}
	}

inspectPath:
	current := unwrapParenForRegionPoly(arg)
	for current != nil {
		switch e := current.(type) {
		case *ast.FieldExpr:
			if e == nil {
				return "", false, false
			}
			isField = true
			current = unwrapParenForRegionPoly(e.Object)
		case *ast.IndexExpr:
			if e == nil {
				return "", false, false
			}
			current = unwrapParenForRegionPoly(e.Object)
		case *ast.Ident:
			if e == nil || e.Name == "" {
				return "", false, false
			}
			return e.Name, isField, true
		default:
			return "", false, false
		}
	}
	return "", false, false
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
	for {
		switch e := unwrapParenForRegionPoly(arg).(type) {
		case *ast.Ident:
			return e != nil && e.Name == name
		case *ast.AddrOfExpr:
			if e == nil {
				return false
			}
			arg = e.Operand
		case *ast.CastExpr:
			if e == nil {
				return false
			}
			arg = e.Operand
		default:
			return false
		}
	}
}

// typeExprCarriesRegionParam reports whether a parameter type-expr is annotated with a region that is
// one of the callee's region params (so passing a value there REQUIRES supplying that region). Peels
// the mutable/owned/tail/ref qualifiers and checks the region annotation on the reference, the
// container builtin, or a generic type.
func typeExprCarriesRegionParam(t ast.TypeExpr, regionParams map[string]bool) bool {
	for t != nil {
		switch n := t.(type) {
		case *ast.MutableType:
			t = n.Elem
		case *ast.OwnedType:
			t = n.Elem
		case *ast.TailType:
			t = n.Elem
		case *ast.RefType:
			if n.Region != "" && regionParams[n.Region] {
				return true
			}
			t = n.Elem
		case *ast.BuiltinTypeExpr:
			return n.Region != "" && regionParams[n.Region]
		case *ast.GenericType:
			return n.Region != "" && regionParams[n.Region]
		default:
			return false
		}
	}
	return false
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
	if len(inferred) != 1 {
		return
	}
	view, ok := fn.ReturnType.(*ast.BuiltinTypeExpr)
	if !ok || view.Name != "view" || view.Region != "" {
		return
	}
	for paramName, regionName := range inferred {
		if functionReturnsSliceOfParam(fn.Body, paramName) {
			view.Region = regionName
		}
	}
}

// functionReturnsSliceOfParam reports whether the body returns a slice whose object is the named
// parameter as a bare identifier, either directly (`return param[a:b]`) or through simple local
// aliases (`w = param[a:b]; return w`). Conservative: a false negative just leaves the return
// region-less (the pre-existing, safe-but-unchecked state), never the reverse.
func functionReturnsSliceOfParam(stmts []ast.Stmt, name string) bool {
	// SOUNDNESS: a local that is EVER aliased to the grown param's slice (on any path) is treated as
	// param-backed when returned, so the region stamp is applied and the escape checker can reject a
	// dangling return. We deliberately do NOT invalidate the alias on a later reassignment: doing so
	// flow-insensitively (a conditional `if c: w <- other`) would drop the stamp on the path that
	// still returns the param-backed view — a use-after-free hole (a regression that was caught at
	// runtime). Over-stamping only over-rejects (e.g. `w = param[a:b]; w <- other; return w`, where w
	// is always rebound), which is sound; precise flow-sensitive invalidation is a future refinement.
	aliases := map[string]bool{}
	valueIsParamSlice := func(expr ast.Expr) bool {
		if slice, ok := unwrapParenForRegionPoly(expr).(*ast.SliceExpr); ok && slice != nil {
			if id, ok := unwrapParenForRegionPoly(slice.Object).(*ast.Ident); ok && id != nil && id.Name == name {
				return true
			}
		}
		if id, ok := unwrapParenForRegionPoly(expr).(*ast.Ident); ok && id != nil && aliases[id.Name] {
			return true
		}
		return false
	}
	var collect func([]ast.Stmt)
	collect = func(body []ast.Stmt) {
		for _, stmt := range body {
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				if s.Value != nil && valueIsParamSlice(s.Value) {
					aliases[s.Name] = true
				}
			case *ast.AssignStmt:
				if id, ok := s.Target.(*ast.Ident); ok && id != nil && s.Value != nil && valueIsParamSlice(s.Value) {
					aliases[id.Name] = true
				}
			case *ast.IfStmt:
				collect(s.Then)
				for _, elif := range s.Elifs {
					collect(elif.Body)
				}
				collect(s.Else)
			case *ast.WhileStmt:
				collect(s.Body)
			case *ast.ForStmt:
				collect(s.Body)
			case *ast.IterForStmt:
				collect(s.Body)
			case *ast.ScopeStmt:
				collect(s.Body)
			case *ast.CanStmt:
				collect(s.Body)
			case *ast.RegionStmt:
				collect(s.Body)
			case *ast.InStoreStmt:
				collect(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					collect(arm.Body)
				}
			}
		}
	}
	for {
		before := len(aliases)
		collect(stmts)
		if len(aliases) == before {
			break
		}
	}

	found := false
	walkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		if ret, ok := node.(*ast.ReturnStmt); ok && ret != nil {
			if valueIsParamSlice(ret.Value) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// regionlessRefContainer reports whether a parameter type is a by-reference (`&`) region-less
// growable container, and if so returns a closure that stamps a region name onto the right AST node.
// Two container spellings qualify:
//   - `darray`/`dict`/`set` — a *ast.BuiltinTypeExpr; the region is stamped onto its `.Region`.
//   - `dstr` — a *ast.NamedType (the u8-darray string; it has no `@r` annotation surface, so it
//     can't carry a region itself). The region is stamped onto the enclosing *ast.RefType instead,
//     which resolveType then pushes DOWN onto the resolved DArrayType via stampContainerRegion —
//     the identical normalization the builtin path relies on, so both spellings produce the same
//     resolved region-carrying container type.
//
// The `&` is required: only a reference makes the callee's growth (header realloc + count) visible
// in the caller — a by-value container param copies the header, so growth would be lost (the S1
// footgun). An existing `@r` anywhere (on the ref or the container) means the user is already
// managing the region: leave it alone.
func regionlessRefContainer(typ ast.TypeExpr) (func(region string), bool) {
	var ref *ast.RefType
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.OwnedType:
			typ = t.Elem
		case *ast.RefType:
			if t.Region != "" {
				return nil, false
			}
			ref = t
			typ = t.Elem
		case *ast.BuiltinTypeExpr:
			if ref == nil || t.Region != "" {
				return nil, false
			}
			switch t.Name {
			case "darray", "dict", "set":
				return func(region string) { t.Region = region }, true
			}
			return nil, false
		case *ast.NamedType:
			if ref == nil || t.Name != "dstr" {
				return nil, false
			}
			// dstr has no region surface of its own; stamp the ref, which resolveType
			// pushes down onto the resolved DArrayType.
			r := ref
			return func(region string) { r.Region = region }, true
		default:
			return nil, false
		}
	}
}

// regionlessRefStruct reports whether a parameter type is a by-reference (`&`) region-less STRUCT
// (a *ast.NamedType that is NOT a region-carrying container — those go through regionlessRefContainer),
// and if so returns a closure that stamps a region name onto the enclosing *ast.RefType. A struct
// has no `@r` surface of its own; the region rides on the ref, and the field-region propagation in
// analyzeFieldExpr (plus the backend's containerRegionName reading RefType.Region for struct refs)
// pushes it onto the resolved container field — the same normalization the dstr path relies on.
//
// The `&` is required for the same reason as containers: only a reference makes the callee's field
// growth (header realloc + count) visible in the caller. An existing `@r` on the ref means the user
// already manages the region: leave it alone (Stage 0's explicit form).
func regionlessRefStruct(typ ast.TypeExpr) (func(region string), bool) {
	var ref *ast.RefType
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.OwnedType:
			typ = t.Elem
		case *ast.RefType:
			if t.Region != "" {
				return nil, false
			}
			ref = t
			typ = t.Elem
		case *ast.NamedType:
			if ref == nil || t.Name == "dstr" {
				return nil, false
			}
			r := ref
			return func(region string) { r.Region = region }, true
		case *ast.GenericType:
			// A by-ref generic struct instantiation (`v: mutable Vector[T]&`) whose container
			// field is grown needs the same caller-threaded region as a concrete struct ref.
			if ref == nil || t.Region != "" {
				return nil, false
			}
			r := ref
			return func(region string) { r.Region = region }, true
		default:
			return nil, false
		}
	}
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
