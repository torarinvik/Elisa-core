package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// storeScanIdentCounts counts identifier occurrences, excluding field selectors and
// deduplicating shared AST pointers exactly as the former per-name walk did.
func storeScanIdentCounts(node any) map[string]int {
	counts := map[string]int{}
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if id, ok := v.Interface().(*ast.Ident); ok && id != nil {
				counts[id.Name]++
				if root := rootNameOfDotted(id.Name); root != id.Name {
					counts[root]++
				}
				return
			}
			if fe, ok := v.Interface().(*ast.FieldExpr); ok && fe != nil {
				rec(reflect.ValueOf(fe.Object))
				return
			}
			rec(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				rec(v.Elem())
			}
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(node))
	return counts
}

// storeScanViewLocalsCached memoizes the per-function view-local set (it depends on the source
// parameter only through the non-byte-darray gate).
func (a *Analyzer) storeScanViewLocalsCached(decl *ast.FuncDecl, other int) map[string]bool {
	decl = a.readDecl(decl)
	if !a.storeScanSourceHasNoBytes(decl, other) {
		return nil
	}
	if views, ok := a.storeScanViewCache[decl]; ok {
		return views
	}
	views := a.storeScanViewLocals(decl)
	if a.storeScanViewCache == nil {
		a.storeScanViewCache = map[*ast.FuncDecl]map[string]bool{}
	}
	a.storeScanViewCache[decl] = views
	return views
}

// storeScanValueIsView reports whether a value is a plain `sview`/`cstr` (by analyzed type, or as a
// view local from storeScanViewLocals). References and `view[T]` slices are not.
func (a *Analyzer) storeScanValueIsView(decl *ast.FuncDecl, value ast.Expr, views map[string]bool) bool {
	decl = a.readDecl(decl)
	inner := storeScanUnwrap(value)
	if id, ok := inner.(*ast.Ident); ok && id != nil && views[id.Name] {
		return true
	}
	t, known := a.lookupExprType(inner)
	if !known || t == nil {
		// Before the body is analyzed: `p.f[i]` / `p.f` typed from the signature.
		t, known = a.storeScanDeclTypeOf(decl, inner)
	}
	if known && t != nil {
		return a.storeScanViewOnlyCached(t)
	}
	if id, ok := inner.(*ast.Ident); ok && id != nil {
		return views[id.Name]
	}
	return false
}

func storeScanTypeIsViewOnly(t Type) bool {
	switch StripAggregateStateType(t).(type) {
	case *SViewType, *CStrType:
		return true
	}
	return typeComponentsViewOnly(t)
}

// storeScanDeclReturnsViewOnly: decl's resolved return type is view-only (and nothing else is
// returned through lmut thread slots), so whatever it returns is at most a view.
func (a *Analyzer) storeScanDeclReturnsViewOnly(decl *ast.FuncDecl) bool {
	decl = a.readDecl(decl)
	if decl == nil || len(decl.LmutThreadSlots) > 0 {
		return false
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || ft == nil || ft.Return == nil || a.containsTypeParamCached(ft.Return) {
		return false
	}
	return a.storeScanViewOnlyCached(ft.Return)
}

// storeScanDeclTypeOf types a param field chain `p.f.g`, or an index into one `p.f[i]`, from
// decl's resolved signature (storeScanParamFieldChainType).
func (a *Analyzer) storeScanDeclTypeOf(decl *ast.FuncDecl, expr ast.Expr) (Type, bool) {
	decl = a.readDecl(decl)
	if decl == nil || expr == nil {
		return nil, false
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if e == nil {
			return nil, false
		}
		return a.storeScanSubjectDeclType(decl, e)
	case *ast.FieldExpr:
		return a.storeScanParamFieldChainType(decl, e)
	case *ast.IndexExpr:
		if e == nil || e.Index2 != nil {
			return nil, false
		}
		var t Type
		var ok bool
		switch object := storeScanUnwrap(e.Object).(type) {
		case *ast.FieldExpr:
			t, ok = a.storeScanParamFieldChainType(decl, object)
		case *ast.Ident:
			t, ok = a.storeScanSubjectDeclType(decl, object)
		}
		if !ok {
			return nil, false
		}
		switch c := stripRefForBounds(t).(type) {
		case *DArrayType:
			if c != nil && c.Elem != nil {
				return c.Elem, true
			}
		case *ArrayType:
			if c != nil && c.Elem != nil {
				return c.Elem, true
			}
		}
	}
	return nil, false
}

// storeScanLoopBinderElemType types a by-value loop binder over a parameter or parameter field
// as a copy of its element type. Only when that loop is the name's ONLY binding in decl (no parameter, no
// declaration, no other pattern or loop binds it), so every use of the name is that copy.
func (a *Analyzer) storeScanLoopBinderElemType(decl *ast.FuncDecl, id *ast.Ident) (Type, bool) {
	decl = a.readDecl(decl)
	if decl == nil || id == nil {
		return nil, false
	}
	idx := a.storeScanIndex(decl)
	if !idx.otherBound[id.Name] || len(idx.decls[id.Name]) != 0 || idx.nonIterBinder[id.Name] || len(idx.iterBinders[id.Name]) != 1 {
		return nil, false
	}
	for _, p := range decl.Params {
		if p.Name == id.Name {
			return nil, false
		}
	}
	loop := idx.iterBinders[id.Name][0]
	if loop == nil || loop.Mode != ast.IterBindValue {
		return nil, false
	}
	if pat, ok := loop.Pattern.(*ast.MoveBindNamePattern); !ok || pat == nil || pat.Name != id.Name {
		return nil, false
	}
	if local, isIdent := storeScanUnwrap(loop.Source).(*ast.Ident); isIdent && local != nil {
		// A local whose every declaration spells `darray[<scalar>]`: each element copy is that
		// scalar, read off the declared type expression (no resolver needed for a builtin).
		if name, ok := a.storeScanLocalScalarDArrayElem(decl, local.Name); ok {
			return &BuiltinType{Name: name}, true
		}
		if elem, ok := a.storeScanLocalProgramViewDarrayElement(decl, local.Name); ok {
			return elem, true
		}
		if t, ok := a.storeScanSubjectDeclType(decl, local); ok && t != nil {
			if c, ok := stripRefForBounds(t).(*DArrayType); ok && c != nil && c.Elem != nil {
				return c.Elem, true
			}
		}
		return nil, false
	}
	source, ok := storeScanUnwrap(loop.Source).(*ast.FieldExpr)
	if !ok || source == nil {
		return nil, false
	}
	t, ok := a.storeScanParamFieldChainType(decl, source)
	if !ok || t == nil {
		return nil, false
	}
	if c, isDArray := stripRefForBounds(t).(*DArrayType); isDArray && c != nil && c.Elem != nil {
		return c.Elem, true
	}
	return nil, false
}

// storeScanLocalProgramViewDarrayElement reads an explicit local element contract.
// The buffer itself remains locally owned; only the copied view is program-lived.
func (a *Analyzer) storeScanLocalProgramViewDarrayElement(decl *ast.FuncDecl, name string) (Type, bool) {
	decl = a.readDecl(decl)
	if decl == nil || len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 {
		return nil, false
	}
	for _, p := range decl.Params {
		if p.Name == name {
			return nil, false
		}
	}
	idx := a.storeScanIndex(decl)
	if idx.otherBound[name] || len(idx.decls[name]) != 1 {
		return nil, false
	}
	elem, ok := storeFlowDarrayElementTypeExpr(idx.decls[name][0].Type)
	if !ok {
		return nil, false
	}
	named, ok := elem.(*ast.NamedType)
	if !ok || named == nil || !a.programRegions[named.Region] {
		return nil, false
	}
	for _, region := range decl.RegionParams {
		if region == named.Region {
			return nil, false
		}
	}
	for _, node := range idx.flat {
		if region, ok := node.(*ast.RegionStmt); ok && region != nil && region.Name == named.Region {
			return nil, false
		}
	}
	switch named.Name {
	case "sview":
		return &SViewType{Region: named.Region}, true
	}
	return nil, false
}

// storeScanLocalScalarDArrayElem: the builtin scalar element name of a local (not a parameter,
// never bound by a pattern or loop) whose every declaration spells `darray[E]` for the same
// numeric or bool E.
func (a *Analyzer) storeScanLocalScalarDArrayElem(decl *ast.FuncDecl, name string) (string, bool) {
	decl = a.readDecl(decl)
	for _, p := range decl.Params {
		if p.Name == name {
			return "", false
		}
	}
	idx := a.storeScanIndex(decl)
	if idx.otherBound[name] || len(idx.decls[name]) == 0 {
		return "", false
	}
	elem := ""
	for _, v := range idx.decls[name] {
		got, ok := storeScanTypeExprScalarDArrayElem(v.Type)
		if !ok || (elem != "" && got != elem) {
			return "", false
		}
		elem = got
	}
	return elem, elem != ""
}

func storeScanTypeExprScalarDArrayElem(t ast.TypeExpr) (string, bool) {
	for {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return "", false
			}
			t = tt.Elem
			continue
		case *ast.BuiltinTypeExpr:
			if tt == nil || tt.Name != "darray" || len(tt.TypeArgs) != 1 || len(tt.ValueArgs) != 0 {
				return "", false
			}
			var elemName string
			switch e := tt.TypeArgs[0].(type) {
			case *ast.NamedType:
				if e != nil && e.Region == "" {
					elemName = e.Name
				}
			case *ast.BuiltinTypeExpr:
				if e != nil && len(e.TypeArgs) == 0 && len(e.ValueArgs) == 0 {
					elemName = e.Name
				}
			}
			switch elemName {
			case "u8", "u16", "u32", "u64", "usize", "i8", "i16", "i32", "i64", "isize", "f32", "f64", "bool":
				return elemName, true
			}
			return "", false
		}
		return "", false
	}
}

// storeScanTypeExprIsView: a declared `sview` / `cstr` (no reference wrapper).
func storeScanTypeExprIsView(t ast.TypeExpr) bool {
	switch tt := t.(type) {
	case *ast.NamedType:
		return tt != nil && (tt.Name == "sview" || tt.Name == "cstr")
	case *ast.BuiltinTypeExpr:
		return tt != nil && (tt.Name == "sview" || tt.Name == "cstr")
	}
	return false
}

// storeScanTypeExprIsNonByteDArray: `darray[E]` (through ref/mutable wrappers) whose element is
// spelled as a non-byte scalar or a plain view. Any other element (a named struct or enum, which may
// hold a dstr) is left to the resolved-type check (typeHoldsNoBytes).
func storeScanTypeExprIsNonByteDArray(t ast.TypeExpr) bool {
	for {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.RefType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.BuiltinTypeExpr:
			if tt == nil || tt.Name != "darray" || len(tt.TypeArgs) != 1 {
				return false
			}
			var elemName string
			switch e := tt.TypeArgs[0].(type) {
			case *ast.NamedType:
				if e != nil {
					elemName = e.Name
				}
			case *ast.BuiltinTypeExpr:
				if e != nil && len(e.TypeArgs) == 0 {
					elemName = e.Name
				}
			}
			switch elemName {
			case "sview", "cstr", "u16", "u32", "u64", "usize", "i16", "i32", "i64", "isize", "f32", "f64", "bool":
				return true
			}
			return false
		}
		return false
	}
}

// storeScanSourceHasNoBytes: the scan's source parameter holds no byte storage anywhere (no
// darray[u8]/dstr, no fixed byte array, through fields, payloads and elements), so no view-only
// value derived from it can view its backing. A syntactic non-byte darray qualifies directly;
// otherwise the callee's resolved parameter type decides. Type parameters, packed enums and
// anything unrecognized are treated as possibly holding bytes.
func (a *Analyzer) storeScanSourceHasNoBytes(decl *ast.FuncDecl, other int) bool {
	decl = a.readDecl(decl)
	if decl == nil || other < 0 || other >= len(decl.Params) {
		return false
	}
	if storeScanTypeExprIsNonByteDArray(decl.Params[other].Type) {
		return true
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || other >= len(fnType.Params) {
		return false
	}
	return typeHoldsNoBytes(fnType.Params[other], map[Type]bool{})
}

func typeHoldsNoBytes(t Type, seen map[Type]bool) bool {
	if t == nil {
		return false
	}
	if seen[t] {
		return true // a cycle adds no new component
	}
	seen[t] = true
	switch tt := StripAggregateStateType(t).(type) {
	case *BuiltinType:
		switch tt.Name {
		case "u8", "i8":
			return true // a scalar byte by value is not storage a view can point into
		case "u16", "u32", "u64", "usize", "i16", "i32", "i64", "isize", "f32", "f64", "bool", "char":
			return true
		}
		return false
	case *SViewType, *CStrType, *BitIntType, *ConstEnumType, *BitGroupType:
		return true
	case *RefType:
		return tt != nil && typeHoldsNoBytes(tt.Elem, seen)
	case *DArrayType:
		if tt == nil || tt.Elem == nil {
			return false
		}
		if b, isBuiltin := tt.Elem.(*BuiltinType); isBuiltin && (b.Name == "u8" || b.Name == "i8") {
			return false
		}
		return typeHoldsNoBytes(tt.Elem, seen)
	case *ArrayType:
		if tt == nil || tt.Elem == nil {
			return false
		}
		if b, isBuiltin := tt.Elem.(*BuiltinType); isBuiltin && (b.Name == "u8" || b.Name == "i8") {
			return false
		}
		return typeHoldsNoBytes(tt.Elem, seen)
	case *TupleType:
		for _, f := range tt.Fields {
			if !typeHoldsNoBytes(f.Type, seen) {
				return false
			}
		}
		return true
	case *OptionalType:
		return typeHoldsNoBytes(tt.Value, seen)
	case *ErrorUnionType:
		return typeHoldsNoBytes(tt.Value, seen)
	case *StructType:
		if tt == nil || tt.Store {
			return false
		}
		for _, f := range tt.Fields {
			if !typeHoldsNoBytes(f.Type, seen) {
				return false
			}
		}
		return true
	case *EnumType:
		if tt == nil || tt.Packed || tt.Root().Packed {
			return false
		}
		for _, v := range tt.Variants {
			for _, payload := range v.Payload {
				if !typeHoldsNoBytes(payload, seen) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// storeScanHolderMayAlias: the callee can make local w.local alias storage it is given. A
// container local whose elements share no storage holds only its own buffer unless its header
// is rebound -- which a by-value parameter cannot do to the caller's local.
func (a *Analyzer) storeScanHolderMayAlias(decl *ast.FuncDecl, w storeScanCallWrite) bool {
	decl = a.readDecl(decl)
	if w.callee == nil || a.storeScanContainerElemsMayShare(decl, &ast.Ident{Name: w.local}) {
		return true
	}
	sym := a.funcDeclSymbols[w.callee]
	if sym == nil {
		return true
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || w.into < 0 || w.into >= len(fnType.Params) {
		return true
	}
	_, isRef := fnType.Params[w.into].(*RefType)
	return isRef
}

// storeScanPlaceDepth: the number of field / index projections between a place and its root
// identifier (`h` 0, `h[0]` 1, `h[0][0]` and `h.lists[0]` 2).
func storeScanPlaceDepth(expr ast.Expr) int {
	depth := 0
	for {
		switch e := stripAddrAndParens(expr).(type) {
		case *ast.FieldExpr:
			if e == nil {
				return depth
			}
			depth++
			expr = e.Object
		case *ast.IndexExpr:
			if e == nil {
				return depth
			}
			depth++
			expr = e.Object
		default:
			return depth
		}
	}
}
