package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// containerGrowthMethods are the by-reference container operations that ALLOCATE into the
// container's backing region. A call to one of these on a parameter is what forces that
// parameter to carry a region: the push site needs an ambient arena, and across a function
// boundary the only one available is the caller's, threaded in. Read-only operations
// (`.count`, indexing, iteration) never need it, so a darray& param used purely for reading
// is deliberately NOT made region-polymorphic.
var containerGrowthMethods = map[string]bool{
	"push":    true,
	"extend":  true,
	"insert":  true,
	"resize":  true,
	"reserve": true,
	"append":  true,
	"put":     true, // dict
	"add":     true, // set
}

// inferRegionParamsForGrownContainerParams is the docs/75 S2 callee-side inference for
// cross-function lifetime. A function that GROWS a region-less by-reference container parameter
// — `def fill(out: mutable darray[T]&): out.push(...)` — is rewritten in place into the explicit
// region-parameter form `def fill[@__rg_out](out: mutable darray[T]& @__rg_out): ...`,
// so the rest of the pipeline sees exactly the (proven sound, S1) annotated shape: the
// caller's region arena is threaded as a hidden Arena& and resolves the growth allocator, and
// resolveType's region push-down unifies `@__rg_out` through the ref onto the container so it
// binds against the `&v` argument's region.
//
// This MUST run BEFORE collectValueSymbols so the synthesized region params and the stamped
// param `@r` are baked into the FuncType at construction time (RegionParams + the param's
// resolved container Region), keeping the AST and the type in lockstep without a second mutation.
//
// SAFETY: this adds no new lifetime power — it only spells, automatically, the `[@r]`/`@r`
// a user could write by hand (and which S1 proved sound under ASan). The "what is stored INTO
// the grown param" lifetime obligation stays guarded by the existing escape checker
// (checkNestedRegionStoreEscape et al.); inference only decides where the param's own backing
// arena comes from.
func (a *Analyzer) inferRegionParamsForGrownContainerParams(decls []scopedDecl) {
	for _, fn := range collectRegionPolyCandidateFuncs(decls) {
		inferRegionParamsForGrownContainerParamsIn(fn)
	}
}

func inferRegionParamsForGrownContainerParamsIn(fn *ast.FuncDecl) {
	if fn == nil || len(fn.Body) == 0 {
		return
	}
	// Don't touch functions that already manage regions explicitly: a hand-written `[@r]`
	// owns the threading, and an `Arena&` param self-threads its allocator (same gate as
	// functionBuildsAndReturnsLocalContainer / the parser's auto-region wrap).
	if len(fn.RegionParams) != 0 || funcHasArenaParam(fn) {
		return
	}
	inferred := map[string]string{} // param name -> inferred region-param name
	for i := range fn.Params {
		p := &fn.Params[i]
		stamp, ok := regionlessRefContainer(p.Type)
		if !ok {
			continue
		}
		if !paramContainerIsGrown(fn.Body, p.Name) && !paramContainerReassignedFromLiteral(fn.Body, p.Name) {
			continue
		}
		// Param names are unique within a function, so the per-param suffix yields a unique,
		// deterministic region-param name (no Date/random — must stay resume-stable).
		name := "__rg_" + p.Name
		stamp(name)
		fn.RegionParams = append(fn.RegionParams, name)
		inferred[p.Name] = name
	}
	inferReturnViewRegion(fn, inferred)
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
	aliases := map[string]bool{}
	invalidAliases := map[string]bool{}
	valueIsParamSlice := func(expr ast.Expr) bool {
		if slice, ok := unwrapParenForRegionPoly(expr).(*ast.SliceExpr); ok && slice != nil {
			if id, ok := unwrapParenForRegionPoly(slice.Object).(*ast.Ident); ok && id != nil && id.Name == name {
				return true
			}
		}
		if id, ok := unwrapParenForRegionPoly(expr).(*ast.Ident); ok && id != nil && aliases[id.Name] && !invalidAliases[id.Name] {
			return true
		}
		return false
	}
	var collect func([]ast.Stmt)
	collect = func(body []ast.Stmt) {
		for _, stmt := range body {
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				if !invalidAliases[s.Name] && s.Value != nil && valueIsParamSlice(s.Value) {
					aliases[s.Name] = true
				}
			case *ast.AssignStmt:
				if id, ok := s.Target.(*ast.Ident); ok && id != nil {
					if s.Value != nil && valueIsParamSlice(s.Value) {
						if !invalidAliases[id.Name] {
							aliases[id.Name] = true
						}
					} else if aliases[id.Name] {
						delete(aliases, id.Name)
						invalidAliases[id.Name] = true
					}
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
		beforeAliases := len(aliases)
		beforeInvalid := len(invalidAliases)
		collect(stmts)
		if len(aliases) == beforeAliases && len(invalidAliases) == beforeInvalid {
			break
		}
	}

	found := false
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if ret, ok := v.Interface().(*ast.ReturnStmt); ok && ret != nil {
				if valueIsParamSlice(ret.Value) {
					found = true
					return
				}
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(stmts))
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

// paramContainerIsGrown reports whether the body contains a growth-method call (push/extend/…)
// whose receiver is the named parameter — `param.push(...)`. A false negative only forgoes the
// ergonomic rewrite (the old "requires an active in <arena>: scope" error still fires, which is
// safe); a false positive only threads an unused region param. Scans structurally via reflection,
// mirroring bodyCallsStoreNeedingOutsideRegion.
func paramContainerIsGrown(stmts []ast.Stmt, name string) bool {
	found := false
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok {
				if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && containerGrowthMethods[field.Field] {
					if id, ok := field.Object.(*ast.Ident); ok && id != nil && id.Name == name {
						found = true
						return
					}
				}
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(stmts))
	return found
}

// paramContainerReassignedFromLiteral reports whether the body REASSIGNS the named ref-container
// parameter from a freshly-allocating literal — `param <- [a, b]`, `param <- [x for x in ...]`, or
// `param <- "..."` (the dstr string-literal sugar, still a StringLit at this pre-pass; flow analysis
// later rewrites it to a byte-list literal). Like push, whole-container reassignment from a literal
// allocates a new backing buffer that must land in the param's region — so it equally forces the
// param to be region-polymorphic, allocating into the caller's region (sound) rather than a local
// auto-region (which would dangle through the ref — use-after-free). Only literal RHS forms qualify;
// reassignment from a call/other container raises region-compatibility questions push never had, so
// it is conservatively excluded (a false negative just reinstates the old "active scope" error).
func paramContainerReassignedFromLiteral(stmts []ast.Stmt, name string) bool {
	found := false
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if asn, ok := v.Interface().(*ast.AssignStmt); ok && asn != nil && !asn.Optional {
				if id, ok := asn.Target.(*ast.Ident); ok && id != nil && id.Name == name && reassignmentAllocatesFreshBacking(asn.Value) {
					found = true
					return
				}
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(stmts))
	return found
}

// reassignmentAllocatesFreshBacking reports whether an assignment RHS allocates a new container
// backing buffer in place: a list/dict/set literal, a comprehension, or a string literal (the dstr
// sugar, rewritten to a byte-list literal in flow analysis). Parens are unwrapped.
func reassignmentAllocatesFreshBacking(value ast.Expr) bool {
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
