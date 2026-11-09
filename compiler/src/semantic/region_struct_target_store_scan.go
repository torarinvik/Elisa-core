package semantic

import (
	"fmt"
	"os"
	"strings"

	"elisacore/src/ast"
)

// storeScanReceiverIsBuiltinDArray reports whether a method receiver is a builtin darray (refs
// peeled). The analyzed type answers when known; a callee scanned before its body was analyzed
// falls back to declared syntax: a bare name every one of whose declarations in decl (params and
// locals) is spelled `darray[..]` / `dstr`. Anything else answers false (the caller then keeps its
// conservative mode).
func (a *Analyzer) storeScanReceiverIsBuiltinDArray(decl *ast.FuncDecl, recv ast.Expr) bool {
	decl = a.readDecl(decl)
	inner := storeScanUnwrap(recv)
	if t, known := a.lookupExprType(inner); known && t != nil {
		_, ok := stripRefForBounds(t).(*DArrayType)
		return ok
	}
	if _, isField := inner.(*ast.FieldExpr); isField && decl != nil {
		if t, ok := a.storeScanParamFieldChainType(decl, inner); ok {
			_, isDArray := stripRefForBounds(t).(*DArrayType)
			return isDArray
		}
		return false
	}
	id, ok := inner.(*ast.Ident)
	if !ok || id == nil || decl == nil {
		return false
	}
	found, all := false, true
	for _, p := range decl.Params {
		if p.Name == id.Name {
			found = true
			all = all && storeScanTypeExprIsDArray(p.Type)
		}
	}
	idx := a.storeScanIndex(decl)
	for _, v := range idx.decls[id.Name] {
		found = true
		all = all && storeScanTypeExprIsDArray(v.Type)
	}
	if idx.otherBound[id.Name] {
		all = false // a pattern / loop binder of unknown type
	}
	return found && all
}

// storeScanParamFieldChainType types `p.f.g` where p is a parameter of decl that no body
// construct rebinds or shadows, from the callee's resolved signature and the struct field
// types -- usable before the callee's body has been analyzed.
func (a *Analyzer) storeScanParamFieldChainType(decl *ast.FuncDecl, expr ast.Expr) (Type, bool) {
	decl = a.readDecl(decl)
	fields := []string{}
	cur := expr
	for {
		f, ok := cur.(*ast.FieldExpr)
		if !ok || f == nil {
			break
		}
		fields = append([]string{f.Field}, fields...)
		cur = storeScanUnwrap(f.Object)
	}
	root, ok := cur.(*ast.Ident)
	if !ok || root == nil || len(fields) == 0 {
		return nil, false
	}
	if t, ok := a.storeScanLocalLiteralType(decl, root.Name); ok {
		return storeScanWalkFields(t, fields)
	}
	if t, ok := a.storeScanPatternBinderType(decl, root.Name); ok {
		return storeScanWalkFields(t, fields)
	}
	index := -1
	for i, p := range decl.Params {
		if p.Name == root.Name {
			index = i
		}
	}
	if index < 0 {
		return nil, false
	}
	if a.storeScanBoundNames(decl)[root.Name] {
		return nil, false
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return nil, false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || fnType.Variadic || index >= len(fnType.Params) {
		return nil, false
	}
	return storeScanWalkFields(fnType.Params[index], fields)
}

func storeScanWalkFields(t Type, fields []string) (Type, bool) {
	for _, name := range fields {
		st, ok := peelToStructType(t)
		if !ok || st == nil || st.Fields == nil {
			return nil, false
		}
		field, ok := st.Fields[name]
		if !ok || field.Type == nil || containsTypeParam(field.Type) {
			return nil, false
		}
		t = field.Type
	}
	return t, true
}

// storeScanBoundNames: every name a body construct of decl binds, memoized per function.
func (a *Analyzer) storeScanBoundNames(decl *ast.FuncDecl) map[string]bool {
	decl = a.readDecl(decl)
	if cached, ok := a.storeScanBoundCache[decl]; ok {
		return cached
	}
	// storeScanIndex already records all body-local and non-declaration binder names. The old
	// walk also called stmtBoundNames on VarDeclStmt nodes, so retain those extra conservative
	// spellings without rescanning every expression and statement in the body.
	idx := a.storeScanIndex(decl)
	names := make(map[string]bool, len(idx.locals))
	for name := range idx.locals {
		names[name] = true
	}
	for _, declarations := range idx.decls {
		for _, declaration := range declarations {
			for _, name := range stmtBoundNames(declaration) {
				names[name] = true
			}
		}
	}
	if a.storeScanBoundCache == nil {
		a.storeScanBoundCache = map[*ast.FuncDecl]map[string]bool{}
	}
	a.storeScanBoundCache[decl] = names
	return names
}

// storeScanLocalLiteralType types a body local every declaration of which is initialized by a
// literal of one non-generic struct `T{..}` (bound by no other construct), when T names exactly
// one struct type in the program.
func (a *Analyzer) storeScanLocalLiteralType(decl *ast.FuncDecl, name string) (Type, bool) {
	decl = a.readDecl(decl)
	for _, p := range decl.Params {
		if p.Name == name {
			return nil, false
		}
	}
	idx := a.storeScanIndex(decl)
	litName, ok, other := "", true, idx.otherBound[name]
	for _, v := range idx.decls[name] {
		// An explicitly typed declaration (`action: DeferredAction = xs[i]`) is checked to hold
		// that struct, whatever its initializer; a region-qualified spelling is not modeled.
		if named, isNamed := v.Type.(*ast.NamedType); isNamed && named != nil && named.Region == "" {
			if litName != "" && named.Name != litName {
				ok = false
				break
			}
			litName = named.Name
			continue
		}
		lit, isLit := storeScanUnwrap(v.Value).(*ast.StructLitExpr)
		if !isLit || lit == nil || len(lit.TypeArgs) > 0 || (litName != "" && lit.Name != litName) {
			ok = false
			break
		}
		litName = lit.Name
	}
	if !ok || other || litName == "" {
		return nil, false
	}
	st := a.storeScanUniqueStruct(litName)
	return st, st != nil
}

// storeScanLocalDarrayStructElement reads only explicit local declarations. All
// bindings must agree, and ambiguous struct names remain unknown.
func (a *Analyzer) storeScanLocalDarrayStructElement(decl *ast.FuncDecl, name string) (Type, bool) {
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
	if idx.otherBound[name] {
		return nil, false
	}
	elementName := ""
	for _, local := range idx.decls[name] {
		elem, ok := storeFlowDarrayElementTypeExpr(local.Type)
		if !ok {
			return nil, false
		}
		named, ok := elem.(*ast.NamedType)
		if !ok || named == nil || named.Region != "" || (elementName != "" && elementName != named.Name) {
			return nil, false
		}
		elementName = named.Name
	}
	if elementName == "" {
		return nil, false
	}
	st := a.storeScanUniqueStruct(elementName)
	// A unique suffix is insufficient: an unrelated module may own that name.
	// Exact declared names also keep shadowed and ambiguous spellings conservative.
	declared, exact := a.namedTypes[elementName].(*StructType)
	return st, st != nil && exact && declared == st
}

// storeScanLiteralFieldsProgramLived proves this construction, not every value
// of its type. Omitted unqualified view fields may have static literal defaults.
func (a *Analyzer) storeScanLiteralFieldsProgramLived(decl *ast.FuncDecl, lit *ast.StructLitExpr, st *StructType, views map[string]bool) bool {
	fail := func(field, reason string) bool {
		if decl != nil && lit != nil && storePayloadTracePrefix != "" && (storePayloadTracePrefix == "1" || strings.HasPrefix(decl.Name, storePayloadTracePrefix)) {
			fmt.Fprintf(os.Stderr, "STORELITERAL decl=%s literal=%s field=%s reason=%s\n", decl.Name, lit.Name, field, reason)
		}
		return false
	}
	if lit == nil || st == nil || st.Decl == nil || !lit.Brace || len(lit.Spreads) != 0 || lit.CopyBase != nil || len(lit.TypeArgs) != 0 || len(st.TypeParams) != 0 || len(st.GenericParams) != 0 || len(st.RegionParams) != 0 || len(lit.Args) != len(lit.ArgNames) {
		return fail("", "unsupported-construction")
	}
	provided := map[string]ast.Expr{}
	for i, value := range lit.Args {
		name := lit.ArgNames[i]
		if name == "" || value == nil || provided[name] != nil {
			return fail(name, "invalid-field")
		}
		if _, exists := st.Fields[name]; !exists {
			return fail(name, "unknown-field")
		}
		provided[name] = value
	}
	for _, fieldDecl := range st.Decl.Fields {
		field, exists := st.Fields[fieldDecl.Name]
		if !exists || field.Type == nil || a.containsTypeParamCached(field.Type) || a.storeScanTypeMayEncodeAddress(field.Type, map[Type]bool{}) {
			return fail(fieldDecl.Name, "unknown-or-address-type")
		}
		if field.Ghost || field.Phantom {
			continue
		}
		value := provided[fieldDecl.Name]
		defaulted := value == nil
		if defaulted {
			value = fieldDecl.DefaultValue
		}
		if value == nil {
			return fail(fieldDecl.Name, "missing-value")
		}
		if a.storeScanHoldsNoRegionStorage(field.Type) {
			continue
		}
		if defaulted {
			// Defaults resolve in the struct's scope, never the caller's.
			// Only this static view case needs no declaration-scope inference.
			if _, isView := field.Type.(*SViewType); isView {
				if _, literal := storeScanUnwrap(value).(*ast.StringLit); literal {
					continue
				}
			}
			return fail(fieldDecl.Name, "nonliteral-reference-default")
		}
		if len(a.programRegions) != 0 && a.typeIsProgramLivedCached(field.Type) {
			continue
		}
		if _, isView := field.Type.(*SViewType); !isView {
			return fail(fieldDecl.Name, "nonprogram-field-type")
		}
		if _, literal := storeScanUnwrap(value).(*ast.StringLit); literal {
			continue
		}
		if !a.storeScanStoredElementExprProgramLived(decl, value, views) {
			return fail(fieldDecl.Name, "unproven-value")
		}
	}
	return true
}

// storeScanDeclaredStructLiteralType reads an exact non-generic declaration in
// the inspected function's namespace, without resolving or warming body types.
func (a *Analyzer) storeScanDeclaredStructLiteralType(decl *ast.FuncDecl, lit *ast.StructLitExpr) Type {
	decl = a.readDecl(decl)
	if decl == nil || lit == nil || len(lit.TypeArgs) != 0 || len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 {
		return nil
	}
	lookup := func(name string) Type {
		st, ok := a.namedTypes[name].(*StructType)
		if !ok || st == nil || len(st.TypeParams) != 0 {
			return nil
		}
		return st
	}
	if strings.Contains(lit.Name, ".") {
		return lookup(lit.Name)
	}
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		name := joinQualifiedName(privateOwnerNamespace(sym.Name), lit.Name)
		if _, exists := a.namedTypes[name]; exists {
			return lookup(name)
		}
	}
	return lookup(lit.Name)
}

// storeScanUniqueStruct: the one struct type whose name is name (unqualified, or its last
// `.`-segment), nil when none or several exist. The index is built once.
func (a *Analyzer) storeScanUniqueStruct(name string) *StructType {
	if a.storeScanStructIndex == nil {
		a.storeScanStructIndex = map[string]*StructType{}
		ambiguous := map[string]bool{}
		seen := map[*StructType]bool{}
		for _, t := range a.namedTypes {
			st, isStruct := t.(*StructType)
			if !isStruct || st == nil || seen[st] || len(st.TypeParams) > 0 {
				continue
			}
			seen[st] = true
			bare := st.Name
			if i := strings.LastIndex(bare, "."); i >= 0 {
				bare = bare[i+1:]
			}
			if prev, dup := a.storeScanStructIndex[bare]; dup && prev != st {
				ambiguous[bare] = true
			}
			a.storeScanStructIndex[bare] = st
		}
		for bare := range ambiguous {
			a.storeScanStructIndex[bare] = nil
		}
	}
	bare := name
	if i := strings.LastIndex(bare, "."); i >= 0 {
		bare = bare[i+1:]
	}
	return a.storeScanStructIndex[bare]
}

// storeScanTypeExprIsDArray: `darray[..]` / `dstr`, through ref and mutable wrappers.
func storeScanTypeExprIsDArray(t ast.TypeExpr) bool {
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
			return tt != nil && (tt.Name == "darray" || tt.Name == "dstr")
		case *ast.NamedType:
			return tt != nil && tt.Name == "dstr"
		}
		return false
	}
}

// storeScanViewLocals: when the scan's source parameter is a NON-BYTE darray (`darray[sview]`,
// `darray[ValueType]`, ...), no `sview`/`cstr` value can be or view the source's own backing --
// only a byte darray yields a view into its storage (`as_sview`) -- so a view derived from the
// source is (part of) an element value: content, never the header. Returns the locals whose every
// declaration is spelled `sview`/`cstr` and that no other construct binds. Its caller checks
// that the source holds no bytes before asking for these locals.
func (a *Analyzer) storeScanViewLocals(decl *ast.FuncDecl) map[string]bool {
	decl = a.readDecl(decl)
	views, poisoned := map[string]bool{}, map[string]bool{}
	for _, p := range decl.Params {
		poisoned[p.Name] = true
	}
	for _, node := range a.storeScanIndex(decl).viewLocalNodes() {
		switch n := node.(type) {
		case *ast.VarDeclStmt:
			if n == nil {
				continue
			}
			// Declared `sview`/`cstr`, or initialized from a value whose analyzed type is built only
			// from scalars and plain views (no container, view[T], reference or pointer).
			if t, known := a.lookupExprType(n.Value); storeScanTypeExprIsView(n.Type) || (n.Value != nil && known && t != nil && typeComponentsViewOnly(t)) {
				views[n.Name] = true
			} else {
				poisoned[n.Name] = true
			}
		case ast.Stmt:
			for _, name := range stmtBoundNames(n) {
				poisoned[name] = true
			}
		}
	}
	for name := range poisoned {
		delete(views, name)
	}
	for name := range a.storeScanViewFilledLocals(decl) {
		views[name] = true
	}
	return views
}

// storeScanViewFilledLocals: locals (of any type) that start empty -- every declaration is an
// argument-less struct literal or an empty list -- and are otherwise mentioned only as the root of
// a builtin darray `push`/`extend` receiver whose appended values are view-only or scalar, or in a
// `return`. From a byte-free source such a local can only ever hold views (its own storage is
// fresh), so it is a view value for the store scan.
func (a *Analyzer) storeScanViewFilledLocals(decl *ast.FuncDecl) map[string]bool {
	decl = a.readDecl(decl)
	idx := a.storeScanIndex(decl)
	fresh, poisoned := map[string]bool{}, map[string]bool{}
	for _, p := range decl.Params {
		poisoned[p.Name] = true
	}
	allowed := map[string]int{}
	for _, node := range idx.viewLocalNodes() {
		switch n := node.(type) {
		case *ast.VarDeclStmt:
			if n == nil {
				continue
			}
			if storeScanEmptyLiteral(n.Value) {
				fresh[n.Name] = true
			} else {
				poisoned[n.Name] = true
			}
		case *ast.ReturnStmt:
			if n != nil && n.Value != nil {
				for name := range fresh {
					allowed[name] += idx.identCount(n.Value, name)
				}
			}
		case *ast.CallExpr:
			field, isMethod := n.Func.(*ast.FieldExpr)
			if n == nil || !isMethod || field == nil || (field.Field != "push" && field.Field != "extend") {
				continue
			}
			root := rootIdentExpr(field.Object)
			if root == nil || !a.storeScanReceiverIsBuiltinDArray(decl, field.Object) {
				continue
			}
			valid := true
			for _, arg := range n.Args {
				t, known := a.lookupExprType(storeScanUnwrap(arg))
				if !known || t == nil {
					t, known = a.storeScanDeclTypeOf(decl, storeScanUnwrap(arg))
				}
				if !known || t == nil {
					valid = false
					break
				}
				if field.Field == "extend" {
					switch c := stripRefForBounds(t).(type) {
					case *DArrayType:
						t = c.Elem
					case *ArrayType:
						t = c.Elem
					default:
						valid = false
					}
					if !valid {
						break
					}
				}
				if t == nil || !(a.storeScanViewOnlyCached(t) || storeScanPlainScalarType(t)) {
					valid = false
					break
				}
			}
			if valid {
				allowed[root.Name] += idx.identCount(field.Object, root.Name)
			}
		case ast.Stmt:
			for _, name := range stmtBoundNames(n) {
				poisoned[name] = true
			}
		}
	}
	filled := map[string]bool{}
	for name := range fresh {
		if !poisoned[name] && idx.bodyIdentCount(decl.Body, name) == allowed[name] {
			filled[name] = true
		}
	}
	return filled
}

// storeScanEmptyLiteral: `T{}` with no fields, spreads or copy base, or `[]`.
func storeScanEmptyLiteral(value ast.Expr) bool {
	switch v := storeScanUnwrap(value).(type) {
	case *ast.StructLitExpr:
		return v != nil && len(v.Args) == 0 && len(v.Spreads) == 0 && v.CopyBase == nil
	case *ast.ListLitExpr:
		return v != nil && len(v.Elems) == 0
	}
	return false
}
