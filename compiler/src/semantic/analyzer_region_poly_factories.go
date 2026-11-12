package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/regionframesyntax"
	"reflect"
)

// functionBuildsAndReturnsLocalContainer detects the build-local-return shape (Stage 1 of the
// region-return-inference plan): a function whose SIGNATURE return type is an owned container
// (`darray`/`dict`/`set`/`dstr`) and which RETURNS a local that was declared as a container literal
// (`out: darray[T] = []`, `out: dict[K,V] = {}`, `out: set[T] = {}`) built up in the body.
// Such a function is region-polymorphic: its
// `out = []` opens a synthesized `__auto_*` region (maybeWrapFunctionBodyInAutoRegion wraps the
// whole body, the `return out` included), and once classified region-polymorphic the backend's
// regionPolyAutoAdopts threads+adopts that region into the caller's `__region_auto`, so the result
// outlives the call. This is the syntactic, pre-pass-safe analogue of the `new[auto]` detection:
// it needs no type info (return-type annotation + container-literal initializer are both syntactic),
// so classification precedes all body analysis and every caller threads the region in.
//
// It deliberately matches ONLY a returned local Ident declared as a container literal — not a
// returned param, field, or call result — so it never over-classifies a forwarding function. A
// false match is in any case safe (an unused threaded arena param), but the conservative shape keeps
// classification honest. The escape suppression (region_containers.go) is the actual safety gate and
// only fires for a synthesized `__auto_*` region, mirroring regionPolyAutoAdopts exactly.
func functionBuildsAndReturnsLocalContainer(fn *ast.FuncDecl) bool {
	return regionframesyntax.BuildsAndReturnsLocalContainer(fn, returnTypeIsOwnedContainer, funcHasArenaParam, typeExprIsRegionless, unwrapParenForRegionPoly, isCompilerGeneratedAutoRegionStmt)
}

// functionBuildsAndReturnsLocalStruct is the aggregate analogue of
// functionBuildsAndReturnsLocalContainer: a function whose body the parser wrapped in a synthesized
// `__auto_*` region and which RETURNS a local constructed by a struct literal (`s: T = T{}`, then
// `s.items.push(..)`, `return s`). The literal's region-less container fields grow into that auto
// region, so without adoption the returned struct carries backings freed at function exit — a
// silent use-after-free (the header copy survives, the elements do not). Classifying it
// region-polymorphic makes the auto region adopt the caller's arena, exactly as for a returned
// container local. Only aggregates that can carry region storage qualify; a false match only
// threads an unused arena.
func functionBuildsAndReturnsLocalStruct(fn *ast.FuncDecl, fnType *FuncType) bool {
	if fn == nil || fnType == nil || funcHasArenaParam(fn) || !typeCarriesRegionStorage(fnType.Return) {
		return false
	}
	st, ok := fnType.Return.(*StructType)
	if !ok {
		return false
	}
	return regionframesyntax.BuildsAndReturnsLocalStruct(fn, unwrapParenForRegionPoly, isCompilerGeneratedAutoRegionStmt, func(body []ast.Stmt, constructed map[string]bool) bool {
		return structFactoryLeavesRegionFieldsFresh(body, constructed, st)
	})
}

// structFactoryLeavesRegionFieldsFresh reports whether every struct literal bound to a constructed
// local, and every assignment through one, leaves the region-carrying fields of st at their fresh
// defaults (or an explicit empty `[]`). Anything it cannot classify returns false.
func structFactoryLeavesRegionFieldsFresh(body []ast.Stmt, constructed map[string]bool, st *StructType) bool {
	if st == nil {
		return false
	}
	ok := true
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		if !ok || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.VarDeclStmt:
				if constructed[n.Name] {
					if lit, isLit := unwrapParenForRegionPoly(n.Value).(*ast.StructLitExpr); isLit && !structLiteralLeavesRegionFieldsFresh(lit, st) {
						ok = false
						return
					}
				}
			case *ast.AssignStmt:
				if !assignmentLeavesRegionFieldsFresh(n.Target, constructed, st) {
					ok = false
					return
				}
			}
			visit(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(body))
	return ok
}

// structLiteralLeavesRegionFieldsFresh: every argument for a region-carrying field of st is an
// empty `[]` or (for a struct-typed field) a nested literal that itself leaves its fields fresh.
func structLiteralLeavesRegionFieldsFresh(lit *ast.StructLitExpr, st *StructType) bool {
	if lit == nil || st == nil || lit.CopyBase != nil || len(lit.Spreads) != 0 {
		return false
	}
	for i, arg := range lit.Args {
		name := ""
		if i < len(lit.ArgNames) && lit.ArgNames[i] != "" {
			name = lit.ArgNames[i]
		} else if st.Decl != nil && i < len(st.Decl.Fields) {
			name = st.Decl.Fields[i].Name
		}
		f, found := st.Fields[name]
		if name == "" || !found {
			return false
		}
		if !typeCarriesRegionStorage(f.Type) {
			continue
		}
		switch a := unwrapParenForRegionPoly(arg).(type) {
		case *ast.ListLitExpr:
			if len(a.Elems) != 0 || a.Owner != nil {
				return false
			}
		case *ast.StructLitExpr:
			inner, isStruct := f.Type.(*StructType)
			if !isStruct || !structLiteralLeavesRegionFieldsFresh(a, inner) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// assignmentLeavesRegionFieldsFresh: an assignment whose target is rooted at a constructed local
// must write a field path whose LEAF type carries no region storage (`t.a.flag <- true`).
func assignmentLeavesRegionFieldsFresh(target ast.Expr, constructed map[string]bool, st *StructType) bool {
	var path []string
	cur := unwrapParenForRegionPoly(target)
	for {
		switch t := cur.(type) {
		case *ast.FieldExpr:
			path = append([]string{t.Field}, path...)
			cur = unwrapParenForRegionPoly(t.Object)
			continue
		case *ast.IndexExpr:
			path = append([]string{""}, path...)
			cur = unwrapParenForRegionPoly(t.Object)
			continue
		}
		break
	}
	id, isIdent := cur.(*ast.Ident)
	if !isIdent || !constructed[id.Name] {
		return true
	}
	if len(path) == 0 {
		return false
	}
	var t Type = st
	for _, seg := range path {
		s, isStruct := t.(*StructType)
		if seg == "" || !isStruct {
			return false
		}
		f, found := s.Fields[seg]
		if !found {
			return false
		}
		t = f.Type
	}
	return !typeCarriesRegionStorage(t)
}

// funcHasArenaParam reports whether any explicit parameter is an `Arena` (modulo `mutable`/`&`
// wrappers) — such a function threads its own allocator and is excluded from build-local-return
// classification, mirroring the parser's maybeWrapFunctionBodyInAutoRegion gate.
func funcHasArenaParam(fn *ast.FuncDecl) bool {
	for i := range fn.Params {
		if typeExprNamedIs(fn.Params[i].Type, "Arena") {
			return true
		}
	}
	return false
}

// typeExprNamedIs reports whether a type expression names `name` after stripping `mutable`/`&`.
func typeExprNamedIs(typ ast.TypeExpr, name string) bool {
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.RefType:
			typ = t.Elem
		default:
			nt, ok := typ.(*ast.NamedType)
			return ok && nt.Name == name
		}
	}
}

// typeExprIsRegionless reports whether a container local's declared type carries NO explicit `@r`
// region annotation — only a region-less container literal lands in the inferred (adoptable) region.
// A nil (untyped) declaration is conservatively NOT treated as region-less build-local-return: the
// typed `out: darray[T] = []` form is the Stage 1 canonical shape (and matches join/make signatures).
func typeExprIsRegionless(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	switch t := typ.(type) {
	case *ast.BuiltinTypeExpr:
		return t.Region == ""
	case *ast.NamedType:
		return t.Name == "dstr" // dstr has no region-annotation surface; always region-less here
	}
	return false
}

// returnTypeIsOwnedContainer reports whether a function's return-type annotation is a growable owned
// container — `darray`/`dict`/`set` (BuiltinTypeExpr) or `dstr` (NamedType, the u8 darray). Views
// (`sview`/`cstr`) and `array` (fixed) are excluded: they are not region-owned growables.
func returnTypeIsOwnedContainer(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	switch t := typ.(type) {
	case *ast.NamedType:
		return t.Name == "dstr"
	case *ast.BuiltinTypeExpr:
		switch t.Name {
		case "darray", "dict", "set":
			return t.Region == ""
		}
	}
	return false
}
