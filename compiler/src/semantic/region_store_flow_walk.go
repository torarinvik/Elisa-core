package semantic

import (
	"reflect"
	"sync"

	"elisacore/src/ast"
)

// storeFlowEach visits v; each statement is a unit whose direct (non-statement) content unit
// handles, nested statements being units of their own.
func (a *Analyzer) storeFlowEach(v reflect.Value, seen map[uintptr]bool, unit func(stmt reflect.Value, nested []reflect.Value) []reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			a.storeFlowEach(v.Elem(), seen, unit)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			a.storeFlowEach(v.Index(i), seen, unit)
		}
	case reflect.Pointer:
		if v.IsNil() || !v.CanInterface() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		if v.Type().Implements(astStmtType) {
			a.storeFlowStmt(v, seen, unit)
			return
		}
		a.storeFlowEach(v.Elem(), seen, unit)
	case reflect.Struct:
		rfs := reflectFields(v.Type())
		for i := 0; i < v.NumField(); i++ {
			if rfs[i].IsExported() {
				a.storeFlowEach(v.Field(i), seen, unit)
			}
		}
	}
}

func (a *Analyzer) storeFlowNodes(v reflect.Value, c *storeFlowCtx, seen map[uintptr]bool) {
	a.storeFlowEach(v, seen, func(stmt reflect.Value, nested []reflect.Value) []reflect.Value {
		return a.storeFlowUnitWithNested(stmt, c, nested)
	})
}

var storeFlowUnitNamesPool = sync.Pool{New: func() any { values := make([]string, 0, 16); return &values }}

func releaseStoreFlowUnitNames(holder *[]string, names []string) {
	if cap(names) > 4096 {
		return
	}
	clear(names[:cap(names)])
	*holder = names[:0]
	storeFlowUnitNamesPool.Put(holder)
}

// storeFlowUnit links the names a statement mentions outside nested statements, applying call
// summaries to the calls among them, and returns the nested statements.
func (a *Analyzer) storeFlowUnit(stmt reflect.Value, c *storeFlowCtx) []reflect.Value {
	return a.storeFlowUnitWithNested(stmt, c, nil)
}

func (a *Analyzer) storeFlowUnitWithNested(stmt reflect.Value, c *storeFlowCtx, nested []reflect.Value) []reflect.Value {
	var calls []*ast.CallExpr
	// A scalar local's initializer carries nothing into it. Collect only
	// its calls from the start, without first gathering unused name facts.
	scalarDecl := a.storeFlowScalarDecl(stmt)
	callsOnly := scalarDecl || a.storeFlowCondOnly(stmt)
	queryScan := storeFlowQueryScan{}
	namesHolder := storeFlowUnitNamesPool.Get().(*[]string)
	names := a.storeFlowCollectMentionsWithQueryInto(stmt.Elem(), stmt.Pointer(), nil, &nested, &calls, callsOnly, true, &queryScan, (*namesHolder)[:0])
	defer releaseStoreFlowUnitNames(namesHolder, names)
	binds := a.storeFlowBinds
	if callsOnly && binds && !scalarDecl {
		callsOnly = false
		nested, calls = nested[:0], nil
		queryScan = storeFlowQueryScan{}
		names = a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, &nested, &calls, false, true, &queryScan)
	}
	for _, pair := range queryScan.result(a, stmt) {
		c.link(pair)
	}
	if target, roots := a.storeFlowElementCopy(stmt); target != "" {
		// `v: darray[sview] = [x for x in src]`: the copy's elements are views or scalars read
		// out of src. They may point where src's elements point, but the copy is fresh storage:
		// nothing written into it reaches src, and src does not alias it.
		c.flow(roots, []string{target})
		c.link([]string{target})
		return nested
	}
	_, isReturn := stmt.Interface().(*ast.ReturnStmt)
	// Names inside calls to summarized callees are linked per the callee's connections instead.
	handled := map[*ast.CallExpr][]string{}
	// Innermost calls first (calls lists a call before the calls in its arguments): an
	// argument that is itself a summarized call contributes only what reaches its result.
	for k := len(calls) - 1; k >= 0; k-- {
		if retNames, ok := a.applyCallStoreFlow(calls[k], c, handled); ok {
			handled[calls[k]] = retNames
		}
	}
	if assign, ok := stmt.Interface().(*ast.AssignStmt); ok && assign != nil && a.storeFlowPointerFreePlace(assign.Target) {
		// A borrow-bearing expression can be used to compute a scalar or another
		// pointer-free field, but that assignment does not retain the expression's
		// storage. Keep any call effects on the RHS, while excluding scalar field
		// reads that would otherwise connect their container roots to the target.
		rhsNames := a.storeFlowMentions(reflect.ValueOf(assign.Value), 0, handled, nil, nil, false)
		c.link(storeFlowRemoveNames(rhsNames, a.storeFlowPointerFreeAccessRoots(assign.Value)))
		return nested
	}
	if binding := a.storeFlowViewTarget(stmt); binding != "" && !binds && len(handled) == len(calls) {
		// A view local (`v: sview = …` / `v <- …`) is never stored through: whatever the
		// initializer mentions may reach v, but nothing flows back into those names, and they
		// are not joined to each other through it. Every call inside must be summarized, so the
		// names in its arguments were already placed by the callee's connections.
		if len(calls) != 0 || callsOnly {
			names = a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), handled, nil, nil, false)
		}
		c.flow(names, []string{binding})
		c.link([]string{binding})
	} else if isReturn && !binds && len(handled) == len(calls) {
		// `return e` with every call summarized: what e mentions reaches the result, but the
		// return itself stores nothing into those names nor joins them to each other.
		if len(calls) != 0 || callsOnly {
			names = a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), handled, nil, nil, false)
		}
		c.flow(names, []string{storeFlowRetName})
	} else if len(handled) == 0 {
		if isReturn {
			names = append(names, storeFlowRetName)
		}
		c.link(names)
	} else if call, binding := a.storeFlowViewBinding(stmt); call != nil && handled[call] != nil {
		// `v: sview = f(...)`: the result views bytes the arguments hold, so they reach v, but a
		// view binding is never stored through: nothing flows back into the arguments, and the
		// arguments feeding it are not joined to each other through it.
		c.flow(handled[call], []string{binding})
		c.link([]string{binding})
	} else {
		// Names inside a handled call were placed by its summary; the rest of the statement
		// joins with whatever the callee lets reach its result.
		outside := a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), handled, nil, nil, callsOnly)
		if isReturn {
			outside = append(outside, storeFlowRetName)
		}
		c.link(outside)
	}
	return nested
}

// storeFlowPointerFreePlace reports an assignment place whose resolved value cannot
// contain Elisa region storage. A write to such a field/index may mutate its owning
// aggregate, but it cannot store a borrow there.
func (a *Analyzer) storeFlowPointerFreePlace(expr ast.Expr) bool {
	typ := a.exprType(expr)
	if typ == nil {
		typ = a.storeFlowPlaceType(expr)
	}
	if typ != nil {
		return storeFlowPointerFreeReturn(typ, map[Type]bool{})
	}
	// During summary convergence, assignment targets are visited before their
	// index-base expression has necessarily been type-checked. Recover the
	// element type from every same-named typed binder rather than treating a
	// cold `values: darray[u32]` base as unknown and linking its scalar store to
	// every name in the assignment. Shadowing and untyped/generic bindings fail
	// closed below.
	index, ok := returnBorrowStripParens(expr).(*ast.IndexExpr)
	return ok && a.storeFlowPointerFreeIndexedElement(index.Object)
}

// storeFlowPointerFreeValue is storeFlowPointerFreePlace, plus the length of a certain builtin
// darray (`xs.count`), whose type is known even before the chain is type-checked.
func (a *Analyzer) storeFlowPointerFreeValue(expr ast.Expr) bool {
	if fe, ok := returnBorrowStripParens(expr).(*ast.FieldExpr); ok && fe != nil && fe.Field == "count" && a.storeFlowDarrayPlace(fe.Object) {
		return true
	}
	return a.storeFlowPointerFreePlace(expr)
}

func (a *Analyzer) storeFlowPointerFreeIndexedElement(base ast.Expr) bool {
	base = returnBorrowStripParens(base)
	id, ok := base.(*ast.Ident)
	if !ok || id == nil || id.Name == "" || a.storeFlowCurrentDecl == nil {
		return false
	}
	idx := a.storeFlowBinderIndexFor(a.storeFlowCurrentDecl)
	if idx.untyped[id.Name] {
		return false
	}
	if idx.hasRangeName(id.Name) {
		return false
	}
	found := false
	for _, binder := range idx.typedBindings(id.Name) {
		elem, isDarray := storeFlowDarrayElementTypeExpr(binder.t)
		if !isDarray || !a.storeFlowPointerFreeTypeExpr(elem, map[Type]bool{}) {
			return false
		}
		found = true
	}
	return found
}

func storeFlowDarrayElementTypeExpr(t ast.TypeExpr) (ast.TypeExpr, bool) {
	for t != nil {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return nil, false
			}
			t = tt.Elem
		case *ast.RefType:
			if tt == nil {
				return nil, false
			}
			t = tt.Elem
		case *ast.BuiltinTypeExpr:
			if tt != nil && tt.Name == "darray" && len(tt.TypeArgs) == 1 && len(tt.ValueArgs) == 0 {
				return tt.TypeArgs[0], true
			}
			return nil, false
		default:
			return nil, false
		}
	}
	return nil, false
}

// storeFlowElementCopy recognizes `v: darray[E] = [x for x in src]` where E is a view or
// scalar type and src is a plain name or field chain: an element-wise copy into fresh storage.
// It returns the (renamed) target and src's roots, or "" when the statement is not one.
func (a *Analyzer) storeFlowElementCopy(stmt reflect.Value) (string, []string) {
	vd, ok := stmt.Interface().(*ast.VarDeclStmt)
	if !ok || vd == nil || vd.Owner != nil || vd.Value == nil {
		return "", nil
	}
	elem, isDarray := storeFlowDarrayElementTypeExpr(vd.Type)
	if !isDarray || !(storeFlowViewTypeExpr(elem) || storeFlowScalarTypeExpr(elem)) {
		return "", nil
	}
	comp, ok := returnBorrowStripParens(vd.Value).(*ast.ListComprehensionExpr)
	if !ok || comp == nil || comp.SecondName != "" || comp.RangeEnd != nil || comp.Key != nil || comp.Filter != nil || comp.Owner != nil || len(comp.Bindings) != 0 || comp.Set || comp.Parallel || comp.Source == nil {
		return "", nil
	}
	if id, ok := comp.Value.(*ast.Ident); !ok || id == nil || id.Name != comp.Name {
		return "", nil
	}
	roots, isChain := storeFlowChainRoots(comp.Source)
	if !isChain || len(roots) == 0 {
		return "", nil
	}
	return a.storeFlowRenamed(vd.Name), a.storeFlowRenamedAll(roots)
}

// storeFlowPointerFreeAccessRoots counts roots named only by pointer-free field or
// index reads. Those reads produce scalar/POD values and must not make their aggregate
// appear to exchange a borrow with another expression in the same assignment.
func (a *Analyzer) storeFlowPointerFreeAccessRoots(expr ast.Expr) map[string]int {
	roots := map[string]int{}
	var walk func(reflect.Value)
	walk = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		switch value.Kind() {
		case reflect.Interface:
			if !value.IsNil() {
				walk(value.Elem())
			}
		case reflect.Pointer:
			if value.IsNil() || !value.CanInterface() {
				return
			}
			var place ast.Expr
			switch node := value.Interface().(type) {
			case *ast.FieldExpr:
				place = node
			case *ast.IndexExpr:
				place = node
			}
			if place != nil && a.storeFlowPointerFreePlace(place) {
				if names, ok := storeFlowChainRoots(place); ok {
					for _, name := range names {
						roots[name]++
					}
					return
				}
			}
			walk(value.Elem())
		case reflect.Struct:
			for _, index := range storeFlowTypeInfoOf(value.Type()).fields {
				walk(value.Field(index))
			}
		case reflect.Slice, reflect.Array:
			for index := 0; index < value.Len(); index++ {
				walk(value.Index(index))
			}
		}
	}
	walk(reflect.ValueOf(expr))
	return roots
}

func storeFlowRemoveNames(names []string, removed map[string]int) []string {
	if len(removed) == 0 {
		return names
	}
	kept := make([]string, 0, len(names))
	for _, name := range names {
		base := storeFlowBaseName(name)
		if removed[base] > 0 {
			removed[base]--
			continue
		}
		kept = append(kept, name)
	}
	return kept
}

// storeFlowViewBinding returns the call and binding name of a `name: sview = call(...)`
// declaration whose callee is declared to return a view too.
func (a *Analyzer) storeFlowViewBinding(stmt reflect.Value) (*ast.CallExpr, string) {
	vd, ok := stmt.Interface().(*ast.VarDeclStmt)
	if !ok || vd == nil || vd.Owner != nil || !storeFlowViewTypeExpr(vd.Type) {
		return nil, ""
	}
	call, ok := returnBorrowStripParens(vd.Value).(*ast.CallExpr)
	if !ok || call == nil {
		return nil, ""
	}
	callee := a.storeFlowCallee(call)
	if callee == nil || !storeFlowViewTypeExpr(callee.ReturnType) {
		return nil, ""
	}
	return call, a.storeFlowRenamed(vd.Name)
}

// storeFlowViewTarget returns the renamed local a statement binds or rebinds when that local
// is a plain view in every declaration of the name: a `v: sview = …` declaration, or a
// `v <- …` / `v = …` rebind of such a (non-parameter) local.
func (a *Analyzer) storeFlowViewTarget(stmt reflect.Value) string {
	switch st := stmt.Interface().(type) {
	case *ast.VarDeclStmt:
		if st != nil && st.Owner == nil && st.Value != nil && storeFlowViewTypeExpr(st.Type) && a.storeFlowViews[st.Name] {
			return a.storeFlowRenamed(st.Name)
		}
	case *ast.AssignStmt:
		if st == nil || st.Optional {
			return ""
		}
		if id, ok := st.Target.(*ast.Ident); ok && id != nil && a.storeFlowViews[id.Name] && !a.storeFlowParams[id.Name] {
			return a.storeFlowRenamed(id.Name)
		}
	}
	return ""
}

// storeFlowViewTypeExpr reports a plain immutable byte view type (`sview`, possibly mutable as
// a binding: rebinding a view never writes through it), or a tuple whose fields are such views
// or scalars. A region qualifier (`sview @r`) bounds where the view points, not whether it can
// be written through, so it is a view all the same.
func storeFlowViewTypeExpr(t ast.TypeExpr) bool {
	if mt, ok := t.(*ast.MutableType); ok && mt != nil {
		t = mt.Elem
	}
	switch tt := t.(type) {
	case *ast.NamedType:
		return tt != nil && tt.Name == "sview"
	case *ast.BuiltinTypeExpr:
		return tt != nil && len(tt.TypeArgs) == 0 && len(tt.ValueArgs) == 0 && tt.Name == "sview"
	case *ast.TupleTypeExpr:
		if tt == nil || len(tt.Fields) == 0 {
			return false
		}
		views := 0
		for _, field := range tt.Fields {
			switch {
			case storeFlowViewTypeExpr(field.Type):
				views++
			case !storeFlowScalarTypeExpr(field.Type):
				return false
			}
		}
		return views > 0
	}
	return false
}

// storeFlowCondOnly reports whether only the calls in a statement's header can move values: an
// if/while condition merely tests, so the names it reads must not be linked.
func (a *Analyzer) storeFlowScalarDecl(stmt reflect.Value) bool {
	vd, ok := stmt.Interface().(*ast.VarDeclStmt)
	if !ok || vd == nil || vd.Owner != nil {
		return false
	}
	return storeFlowScalarTypeExpr(vd.Type) || a.storeFlowPointerFreeTypeExpr(vd.Type, map[Type]bool{})
}

func (a *Analyzer) storeFlowCondOnly(stmt reflect.Value) bool {
	switch stmt.Interface().(type) {
	case *ast.IfStmt, *ast.WhileStmt:
		return true
	}
	return false
}

// storeFlowBindingExprs are expressions that bind a name to part of a value they inspect.
var storeFlowBindingExprs = map[string]bool{
	"IsPatternExpr": true, "IsAliasExpr": true, "OptionalBindExpr": true, "MatchExpr": true,
	"CatchExpr": true, "UnwrapElseExpr": true, "TryExpr": true, "GetExpr": true, "QueryExpr": true,
	"FoldExpr": true, "LambdaExpr": true, "VariantTestExpr": true, "StructTestExpr": true,
}

// storeFlowRenamed maps a name through the binder scope of the statement being processed.
func (a *Analyzer) storeFlowRenamed(name string) string {
	if renamed, ok := a.storeFlowEnv[name]; ok {
		return renamed
	}
	return name
}

func (a *Analyzer) storeFlowRenamedAll(names []string) []string {
	for i, name := range names {
		names[i] = a.storeFlowRenamed(name)
	}
	return names
}

// storeFlowPatternBinders lists the variable names a match pattern mentions: its binders. A
// pattern names no outer variable it could alias, so each is scoped to its arm.
func storeFlowPatternBinders(pattern ast.MatchPattern) []string {
	var binders []string
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.String:
			if storeFlowVariableName(v.String()) {
				if binders == nil {
					binders = make([]string, 0, 4)
				}
				binders = append(binders, v.String())
			}
		case reflect.Struct:
			for _, i := range storeFlowTypeInfoOf(v.Type()).fields {
				walk(v.Field(i))
			}
		}
	}
	walk(reflect.ValueOf(pattern))
	return binders
}

// storeFlowReadOnlyMethods are builtin methods that neither mutate their receiver nor move a
// value into it.
var storeFlowReadOnlyMethods = map[string]bool{
	"count": true, "len": true, "is_empty": true, "contains": true, "starts_with": true,
	"ends_with": true, "get": true, "index_of": true, "find": true, "first": true, "last": true,
	"capacity": true, "as_sview": true, "view": true,
}

// storeFlowMentions lists the variable names a statement mentions outside nested statements
// (returned through nested) and reports its calls. A call in handled contributes only the
// names its summary lets reach the result, and its subtree is skipped.
func (a *Analyzer) storeFlowMentions(root reflect.Value, self uintptr, handled map[*ast.CallExpr][]string, nested *[]reflect.Value, calls *[]*ast.CallExpr, callsOnly bool) []string {
	return a.storeFlowCollectMentions(root, self, handled, nested, calls, callsOnly, true)
}

func (a *Analyzer) storeFlowCollectMentions(root reflect.Value, self uintptr, handled map[*ast.CallExpr][]string, nested *[]reflect.Value, calls *[]*ast.CallExpr, callsOnly, collectNames bool) []string {
	return a.storeFlowCollectMentionsWithQuery(root, self, handled, nested, calls, callsOnly, collectNames, nil)
}

// Skipped call subtrees force the original query walk; otherwise the first
// walk collects exactly the same query pairs as that walk.
type storeFlowQueryScan struct {
	incomplete bool
	pairs      [][]string
}

// A declared call's identifier cannot contain a query. Its arguments are
// already walked; avoid a second walk when all other query-bearing fields are
// empty. Guard the schema so new AST fields retain the conservative fallback.
var storeFlowDeclaredCallQueryFast = func() bool {
	var queryFree func(reflect.Type) bool
	queryFree = func(t reflect.Type) bool {
		switch t.Kind() {
		case reflect.Map: // the reference walker does not traverse maps
			return true
		case reflect.Array, reflect.Slice:
			return queryFree(t.Elem())
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				if t.Field(i).IsExported() && !queryFree(t.Field(i).Type) {
					return false
				}
			}
			return true
		case reflect.Interface, reflect.Pointer:
			return false
		default:
			return true
		}
	}
	t := reflect.TypeOf(ast.CallExpr{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		switch f.Name {
		case "Func", "Args", "ResolvedArgs", "SafeReceiver", "ResolvedImplicitArgs":
			continue
		}
		if f.IsExported() && !queryFree(f.Type) {
			return false
		}
	}
	return true
}()
