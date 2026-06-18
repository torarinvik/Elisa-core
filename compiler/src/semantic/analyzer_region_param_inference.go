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
	cands := collectRegionPolyCandidateFuncs(decls)
	// Resolve direct free-function call targets by name for the forwarding trigger. Overloads/last-wins
	// is acceptable: a wrong match only forgoes (or, at worst, conservatively adds) an inferred region
	// param — soundness is unaffected (the region threading is still verified at the real call site).
	funcByName := map[string]*ast.FuncDecl{}
	explicit := map[*ast.FuncDecl]bool{}
	for _, fn := range cands {
		if fn == nil {
			continue
		}
		funcByName[fn.Name] = fn
		// Functions that already manage regions explicitly are left alone: a hand-written `[@r]` owns
		// the threading, and an `Arena&` param self-threads its allocator.
		if len(fn.RegionParams) != 0 || funcHasArenaParam(fn) {
			explicit[fn] = true
		}
	}
	// Fixpoint: making one function region-polymorphic turns it into a region-REQUIRING callee, so a
	// caller that merely FORWARDS a container/struct ref param to it must itself become region-poly to
	// thread the region through. Iterate until no function gains a new region param. Each per-function
	// pass is idempotent (an already-stamped param's type carries its region, so it no longer matches
	// the region-LESS predicates), so reprocessing only ever picks up newly-enabled params.
	for {
		changed := false
		for _, fn := range cands {
			if fn == nil || explicit[fn] {
				continue
			}
			if a.inferRegionParamsForGrownContainerParamsIn(fn, funcByName) {
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func (a *Analyzer) inferRegionParamsForGrownContainerParamsIn(fn *ast.FuncDecl, funcByName map[string]*ast.FuncDecl) bool {
	if fn == nil || len(fn.Body) == 0 {
		return false
	}
	changed := false
	inferred := map[string]string{} // param name -> inferred region-param name
	for i := range fn.Params {
		p := &fn.Params[i]
		var stamp func(string)
		if cstamp, ok := regionlessRefContainer(p.Type); ok {
			// A grown / literal-reassigned container ref param, OR one merely FORWARDED to a callee that
			// requires a region there (so the region must thread through this function too).
			if paramContainerIsGrown(fn.Body, p.Name) || paramContainerReassignedFromLiteral(fn.Body, p.Name) || a.paramForwardedToRegionRequiringCallee(fn.Body, p.Name, funcByName) {
				stamp = cstamp
			}
		} else if sstamp, ok := regionlessRefStruct(p.Type); ok {
			// docs/91 S4 Stage 1: a region-less by-ref STRUCT param whose CONTAINER FIELD is grown
			// (`def fill(m: mutable Mod&): m.bits.push(..)`), or which is forwarded to a region-
			// requiring callee. Stamp the inferred region onto the struct ref (`Mod& @r`): the
			// field-region propagation pushes it onto the resolved field container, and the same
			// return-escape checks that guard the explicit `[@r]` form cover every borrow-out vector
			// regardless of how the region param was introduced — no new lifetime power.
			if paramFieldContainerIsGrown(fn.Body, p.Name) || a.paramForwardedToRegionRequiringCallee(fn.Body, p.Name, funcByName) {
				stamp = sstamp
			}
		}
		if stamp == nil {
			continue
		}
		// Param names are unique within a function, so the per-param suffix yields a unique,
		// deterministic region-param name (no Date/random — must stay resume-stable).
		name := "__rg_" + p.Name
		stamp(name)
		fn.RegionParams = append(fn.RegionParams, name)
		inferred[p.Name] = name
		changed = true
	}
	if len(inferred) > 0 {
		inferReturnViewRegion(fn, inferred)
	}
	return changed
}

// paramForwardedToRegionRequiringCallee reports whether the body passes the named parameter (as a bare
// identifier, `&param`, or a reborrow cast) as an argument to a resolved direct free-function call
// whose parameter at that position REQUIRES a region (its type carries one of the callee's region
// params). Such a forward makes this function region-polymorphic over the param even though it never
// grows it itself — the callee's region must be threaded in. Conservative: an unresolved/method/
// indirect callee, or a non-region-requiring position, simply doesn't trigger (a false negative
// re-surfaces as the existing "cannot infer region parameter" error, never an unsound accept).
//
// IMPORTANT: a PascalCase callee `Name(args)` parses as an *ast.StructLitExpr (constructor-or-call
// ambiguity) at this pre-pass — name resolution only reclassifies it to a call later. So a free call
// to a region-requiring function with an uppercase name (the bulk of the loader's call graph) is a
// StructLitExpr here, not a CallExpr; both forms are handled below.
func (a *Analyzer) paramForwardedToRegionRequiringCallee(stmts []ast.Stmt, name string, funcByName map[string]*ast.FuncDecl) bool {
	found := false
	// checkCall tests whether passing `name` at some position of a call to `calleeName(args)` lands in a
	// region-requiring callee parameter.
	checkCall := func(calleeName string, args []ast.Expr) bool {
		callee := funcByName[calleeName]
		if callee == nil {
			return false
		}
		params := a.expandedFuncDeclParams(callee)
		rps := map[string]bool{}
		for _, rp := range callee.RegionParams {
			rps[rp] = true
		}
		if len(rps) == 0 {
			return false
		}
		for argPos, arg := range args {
			if forwardsParamIdent(arg, name) && argPos < len(params) && typeExprCarriesRegionParam(params[argPos].Type, rps) {
				return true
			}
		}
		return false
	}
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
			switch n := v.Interface().(type) {
			case *ast.CallExpr:
				if ident, ok := n.Func.(*ast.Ident); ok && ident != nil && checkCall(ident.Name, n.Args) {
					found = true
					return
				}
			case *ast.StructLitExpr:
				// `Name(args)` paren-call form of a PascalCase free function (Brace==false). Spreads are
				// not positional forwards, so only Args are checked.
				if n != nil && !n.Brace && checkCall(n.Name, n.Args) {
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
		default:
			return nil, false
		}
	}
}

// paramFieldContainerIsGrown reports whether the body contains a growth-method call whose receiver is
// a FIELD of the named struct parameter — `param.field.push(...)`. Mirrors paramContainerIsGrown but
// peels one field access (the container lives inside the struct). A false negative only forgoes the
// ergonomic rewrite (the old "active region" error still fires, which is safe); a false positive only
// threads an unused region param.
func paramFieldContainerIsGrown(stmts []ast.Stmt, name string) bool {
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
			// A growth nested inside an explicit `in <arena>:` (InStoreStmt) or local `region:`
			// (RegionStmt) scope already has its allocation region supplied lexically — no region
			// param threading is needed (and forcing `@r` would make the call site demand a region
			// the struct ref doesn't carry). Don't descend into those scopes.
			switch v.Interface().(type) {
			case *ast.InStoreStmt, *ast.RegionStmt:
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok {
				if growth, ok := call.Func.(*ast.FieldExpr); ok && growth != nil && containerGrowthMethods[growth.Field] {
					// receiver must be `param.<field>` (a FieldExpr whose object is the param Ident)
					if fieldRecv, ok := growth.Object.(*ast.FieldExpr); ok && fieldRecv != nil {
						if id, ok := fieldRecv.Object.(*ast.Ident); ok && id != nil && id.Name == name {
							found = true
							return
						}
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
