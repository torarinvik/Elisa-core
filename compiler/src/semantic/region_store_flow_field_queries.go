package semantic

import (
	"reflect"
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// For a read-only call on a plain identifier, the skipped receiver cannot
// contain queries. Require the known field schema and no other skipped
// expressions; all explicit arguments are still visited in order.
var storeFlowSimpleFieldQueryFast = func() bool {
	t := reflect.TypeOf(ast.FieldExpr{})
	if t.NumField() != 4 {
		return false
	}
	return t.Field(0).Name == "Position" && t.Field(0).Type == reflect.TypeOf(lexer.Pos{}) &&
		t.Field(1).Name == "Object" && t.Field(1).Type == reflect.TypeOf((*ast.Expr)(nil)).Elem() &&
		t.Field(2).Name == "Field" && t.Field(2).Type.Kind() == reflect.String &&
		t.Field(3).Name == "Safe" && t.Field(3).Type.Kind() == reflect.Bool && storeFlowIdentFast
}()

func (q *storeFlowQueryScan) result(a *Analyzer, stmt reflect.Value) [][]string {
	if q.incomplete {
		return a.storeFlowQueryBinderPairs(stmt)
	}
	return q.pairs
}

func (a *Analyzer) storeFlowCollectMentionsWithQuery(root reflect.Value, self uintptr, handled map[*ast.CallExpr][]string, nested *[]reflect.Value, calls *[]*ast.CallExpr, callsOnly, collectNames bool, queryScan *storeFlowQueryScan) []string {
	return a.storeFlowCollectMentionsWithQueryInto(root, self, handled, nested, calls, callsOnly, collectNames, queryScan, nil)
}

func (a *Analyzer) storeFlowCollectMentionsWithQueryInto(root reflect.Value, self uintptr, handled map[*ast.CallExpr][]string, nested *[]reflect.Value, calls *[]*ast.CallExpr, callsOnly, collectNames bool, queryScan *storeFlowQueryScan, names []string) []string {
	callDepth := 0
	condDepth := 0 // inside a ternary condition: a bool, so its names reach no value
	a.storeFlowBinds = false
	// Scope rename maps are immutable snapshots. Borrow the enclosing environment and
	// clone only when a comprehension or match arm adds binders.
	rename := a.storeFlowEnv
	var walk func(v reflect.Value)
	walkStructFields := func(v reflect.Value) {
		for _, i := range storeFlowTypeInfoOf(v.Type()).fields {
			walk(v.Field(i))
		}
	}
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.String:
			if collectNames && condDepth == 0 && (!callsOnly || callDepth > 0) && storeFlowVariableName(v.String()) {
				name := v.String()
				if renamed, ok := rename[name]; ok {
					name = renamed
				}
				names = append(names, name)
			}
		case reflect.Pointer:
			if v.IsNil() || !v.CanInterface() {
				return
			}
			node := v.Interface()
			if storeFlowIndexFast {
				if index, ok := node.(*ast.IndexExpr); ok {
					if !storeFlowPositionFast || (collectNames && condDepth == 0 && (!callsOnly || callDepth > 0)) {
						walk(reflect.ValueOf(&index.Position).Elem())
					}
					walk(reflect.ValueOf(index.Object))
					walk(reflect.ValueOf(index.Index))
					walk(reflect.ValueOf(index.Index2))
					walk(reflect.ValueOf(index.Fallback))
					if index.AsSpecialize != nil {
						walk(reflect.ValueOf(index.AsSpecialize))
					}
					if index.AsOverlayCall != nil {
						walk(reflect.ValueOf(index.AsOverlayCall))
					}
					if index.AsBuiltinDictGet != nil {
						walk(reflect.ValueOf(index.AsBuiltinDictGet))
					}
					return
				}
			}
			if storeFlowParenFast {
				if paren, ok := node.(*ast.ParenExpr); ok {
					if !storeFlowPositionFast || (collectNames && condDepth == 0 && (!callsOnly || callDepth > 0)) {
						walk(reflect.ValueOf(&paren.Position).Elem())
					}
					walk(reflect.ValueOf(paren.Inner))
					return
				}
			}
			if storeFlowSimpleFieldQueryFast {
				if field, ok := node.(*ast.FieldExpr); ok {
					if !storeFlowPositionFast || (collectNames && condDepth == 0 && (!callsOnly || callDepth > 0)) {
						walk(reflect.ValueOf(&field.Position).Elem())
					}
					walk(reflect.ValueOf(field.Object))
					return
				}
			}
			if storeFlowBinaryFast {
				if binary, ok := node.(*ast.BinaryExpr); ok {
					if !storeFlowPositionFast || (collectNames && condDepth == 0 && (!callsOnly || callDepth > 0)) {
						walk(reflect.ValueOf(&binary.Position).Elem())
					}
					walk(reflect.ValueOf(binary.Left))
					walk(reflect.ValueOf(binary.Right))
					if binary.LoweredCall != nil {
						walk(reflect.ValueOf(binary.LoweredCall))
					}
					return
				}
			}
			if storeFlowIntLiteralFast && storeFlowIdentFast && storeFlowPositionFast {
				if lit, ok := node.(*ast.IntLit); ok {
					if collectNames && condDepth == 0 && (!callsOnly || callDepth > 0) && storeFlowVariableName(lit.Position.File) {
						name := lit.Position.File
						if renamed, ok := rename[name]; ok {
							name = renamed
						}
						names = append(names, name)
					}
					return
				}
			}
			if storeFlowIdentFast {
				if ident, ok := node.(*ast.Ident); ok {
					if collectNames && condDepth == 0 && (!callsOnly || callDepth > 0) {
						for _, name := range [...]string{ident.Position.File, ident.Name} {
							if storeFlowVariableName(name) {
								if renamed, ok := rename[name]; ok {
									name = renamed
								}
								names = append(names, name)
							}
						}
					}
					return
				}
			}
			info := storeFlowTypeInfoOf(v.Type())
			if info.isStmt && v.Pointer() != self {
				if nested != nil {
					*nested = append(*nested, v)
				}
				if a.storeFlowStmtEnv != nil {
					if len(rename) == 0 {
						delete(a.storeFlowStmtEnv, v.Pointer())
					} else {
						a.storeFlowStmtEnv[v.Pointer()] = rename
					}
				}
				return
			}
			if info.isTypeExpr {
				return // type syntax names types, not values
			}
			if comp, ok := node.(*ast.ListComprehensionExpr); ok {
				// A comprehension binder is scoped to it: distinct comprehensions reusing a name
				// must not be unified through it.
				savedRename := rename
				if comp.Name != "" || comp.SecondName != "" {
					scoped := make(map[string]string, len(rename)+2)
					for name, renamed := range rename {
						scoped[name] = renamed
					}
					var buf = [17]byte{'#'}
					suffix := strconv.AppendUint(buf[:1], uint64(v.Pointer()), 16)
					for _, binder := range []string{comp.Name, comp.SecondName} {
						if binder != "" {
							scoped[binder] = binder + string(suffix)
						}
					}
					rename = scoped
				}
				walk(v.Elem())
				rename = savedRename
				return
			}
			if tern, ok := node.(*ast.TernaryExpr); ok && tern != nil && !storeFlowHasBindingExpr(reflect.ValueOf(&tern.Cond).Elem()) {
				// `value if cond else alt`: only the branches become the value. The condition is
				// still walked so its calls (and the statements nested in it) are recorded.
				walk(reflect.ValueOf(&tern.Value).Elem())
				condDepth++
				walk(reflect.ValueOf(&tern.Cond).Elem())
				condDepth--
				walk(reflect.ValueOf(&tern.Alt).Elem())
				return
			}
			if q, ok := node.(*ast.QueryExpr); ok && q != nil && storeFlowScalarQuery(q) {
				if queryScan != nil {
					roots, _ := storeFlowChainRoots(q.Source)
					queryScan.pairs = append(queryScan.pairs, append([]string{q.Name}, a.storeFlowRenamedAll(roots)...))
				}
				// `any/all/count x in src where p`: a bool or a count. Its binder is scoped to the
				// query and its value holds nothing, so its names are tested, not moved; calls in
				// it are still recorded.
				condDepth++
				walk(v.Elem())
				condDepth--
				return
			}
			if info.binding {
				a.storeFlowBinds = true
			}
			if call, ok := node.(*ast.CallExpr); ok {
				if callsOnly {
					if fe, isField := call.Func.(*ast.FieldExpr); isField && fe != nil && storeFlowReadOnlyMethods[fe.Field] && !a.isDeclaredFuncName(fe.Field) {
						_, plainReceiver := fe.Object.(*ast.Ident)
						if queryScan != nil && (!storeFlowDeclaredCallQueryFast || !storeFlowSimpleFieldQueryFast || !plainReceiver || call.SafeReceiver != nil || len(call.ResolvedArgs) != 0 || len(call.ResolvedImplicitArgs) != 0) {
							queryScan.incomplete = true
						}
						for _, arg := range call.Args {
							walk(reflect.ValueOf(arg))
						}
						return
					}
				}
				if callsOnly {
					callDepth++
					defer func() { callDepth-- }()
				}
				if calls != nil {
					*calls = append(*calls, call)
				}
				if retNames, isHandled := handled[call]; isHandled {
					if queryScan != nil {
						queryScan.incomplete = true
					}
					if collectNames && condDepth == 0 {
						names = append(names, retNames...)
					}
					return
				}
				// A declared function's own name is not a variable.
				if ident, ok := call.Func.(*ast.Ident); ok && ident != nil && a.isDeclaredFuncName(ident.Name) && !a.storeFlowParams[ident.Name] {
					if queryScan != nil && (!storeFlowDeclaredCallQueryFast || call.SafeReceiver != nil || len(call.ResolvedImplicitArgs) != 0 || (!call.ResolvedArgsValid && len(call.ResolvedArgs) != 0)) {
						queryScan.incomplete = true
					}
					for _, arg := range call.Args {
						walk(reflect.ValueOf(arg))
					}
					if call.ResolvedArgsValid {
						for _, arg := range call.ResolvedArgs {
							walk(reflect.ValueOf(arg))
						}
					}
					return
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			// Positions contain only one string, File; all other fields are ints.
			// Preserve its name contribution without recursively reflecting over
			// every coordinate on every syntax node.
			if storeFlowIdentFast && storeFlowPositionFast && v.Type() == lexerPosType {
				if collectNames && condDepth == 0 && (!callsOnly || callDepth > 0) {
					name := v.Field(0).String()
					if storeFlowVariableName(name) {
						if renamed, ok := rename[name]; ok {
							name = renamed
						}
						names = append(names, name)
					}
				}
				return
			}
			// Test the type before boxing: Interface() on a struct value copies it to the heap.
			if v.Type() != astMatchArmType || !v.CanAddr() {
				walkStructFields(v)
				return
			}
			// The arm is addressable: borrow it instead of boxing the entire
			// struct on every traversal of a store-flow summary.
			if arm, ok := v.Addr().Interface().(*ast.MatchArm); ok {
				// A match arm's pattern binders are scoped to that arm (its guard and body):
				// distinct arms and matches reusing a binder name must not be unified through it.
				binders := storeFlowPatternBinders(arm.Pattern)
				savedRename := rename
				if len(binders) != 0 {
					scoped := make(map[string]string, len(rename)+len(binders))
					for name, renamed := range rename {
						scoped[name] = renamed
					}
					var buf = [17]byte{'#'}
					suffix := strconv.AppendUint(buf[:1], uint64(v.Addr().Pointer()), 16)
					for _, binder := range binders {
						scoped[binder] = binder + string(suffix)
					}
					rename = scoped
				}
				walkStructFields(v)
				rename = savedRename
				return
			}
			walkStructFields(v)
		}
	}
	walk(root)
	return names
}

// storeFlowHasBindingExpr: a condition that binds (`v is Some(r)`) hands its binders to the
// branches, so its names must stay connected to the value.
func storeFlowHasBindingExpr(v reflect.Value) bool {
	found := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if q, ok := v.Interface().(*ast.QueryExpr); ok && q != nil && storeFlowScalarQuery(q) {
				return
			}
			if storeFlowBindingExprs[v.Type().Elem().Name()] {
				found = true
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for _, i := range fillMayAdoptExportedFields(v.Type()) {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(v)
	return found
}

func (a *Analyzer) isDeclaredFuncName(name string) bool {
	if a.declaredFuncNames == nil {
		a.declaredFuncNames = map[string]bool{}
		for decl := range a.funcDeclSymbols {
			if decl != nil {
				a.declaredFuncNames[fillMayAdoptLastSegment(decl.Name)] = true
			}
		}
	}
	return a.declaredFuncNames[fillMayAdoptLastSegment(name)]
}

// storeFlowArgNames is storeFlowExprNames with the already summarized calls inside the
// expression standing for what reaches their results.
func (a *Analyzer) storeFlowArgNames(expr ast.Expr, handled map[*ast.CallExpr][]string) []string {
	if names, ok := a.storeFlowSimpleExprNames(expr); ok {
		return names
	}
	return a.storeFlowMentions(reflect.ValueOf(expr), 0, handled, nil, nil, false)
}

func (a *Analyzer) storeFlowExprNames(expr ast.Expr) []string {
	if names, ok := a.storeFlowSimpleExprNames(expr); ok {
		return names
	}
	return a.storeFlowMentions(reflect.ValueOf(expr), 0, nil, nil, nil, false)
}

// Identifier arguments are especially common in call summaries. Match the
// reflective walk's ordering and position-file contribution, including rename.
func (a *Analyzer) storeFlowSimpleExprNames(expr ast.Expr) ([]string, bool) {
	if !storeFlowIdentFast {
		return nil, false
	}
	id, ok := expr.(*ast.Ident)
	if !ok || id == nil {
		return nil, false
	}
	a.storeFlowBinds = false
	var names []string
	for _, name := range [...]string{id.Position.File, id.Name} {
		if storeFlowVariableName(name) {
			if renamed, ok := a.storeFlowEnv[name]; ok {
				name = renamed
			}
			names = append(names, name)
		}
	}
	return names, true
}

// storeFlowCallee resolves a call to the single declared function it can only mean, or nil.
// A summary can cover omitted trailing default parameters only when each is scalar: those
// omitted slots cannot carry a borrow or contribute argument flows to the call.
func storeFlowCallFitsParams(decl *ast.FuncDecl, call *ast.CallExpr) bool {
	if decl == nil || call == nil {
		return false
	}
	args := returnBorrowCallArgs(call)
	if len(args) > len(decl.Params) {
		return false
	}
	if call.ResolvedArgsValid {
		return len(args) == len(decl.Params)
	}
	if call.HasArgForward || call.NamedArgCount() != 0 {
		return false
	}
	for _, param := range decl.Params[len(args):] {
		if !storeFlowDefaultCallArgIsIndependent(param) {
			return false
		}
	}
	return true
}

// storeFlowDefaultCallArgIsIndependent reports defaults that cannot introduce a borrow from
// this call's arguments. Scalar defaults are always independent. A string literal used for an
// unregioned sview is also independent: it refers to static literal storage, while an explicitly
// region-bound view remains conservative. So is an empty list literal.
func storeFlowDefaultCallArgIsIndependent(param ast.ParamDecl) bool {
	if param.DefaultValue == nil {
		return false
	}
	if storeFlowScalarTypeExpr(param.Type) {
		return true
	}
	// An empty list literal is fresh storage that holds nothing of the caller's.
	if list, ok := param.DefaultValue.(*ast.ListLitExpr); ok && list != nil && len(list.Elems) == 0 {
		return true
	}
	typ, ok := param.Type.(*ast.NamedType)
	if !ok || typ == nil || typ.Name != "sview" || typ.Region != "" {
		return false
	}
	_, ok = param.DefaultValue.(*ast.StringLit)
	return ok
}

func (a *Analyzer) storeFlowCallee(call *ast.CallExpr) *ast.FuncDecl {
	// A call binding only region parameters (`f[r](...)`) runs the same body for every
	// region instantiation, so it shares the callee's summary.
	if spec, ok := call.Func.(*ast.SpecializeExpr); ok && spec != nil {
		plain := *call
		plain.Func = spec.Operand
		decl := a.storeFlowCallee(&plain)
		if decl == nil || len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 || len(spec.TypeArgs) != len(decl.RegionParams) {
			return nil
		}
		return decl
	}
	ident, ok := call.Func.(*ast.Ident)
	// Locals are a function-wide conservative set. A possible local callable
	// must not borrow a same-named global function's precise store summary.
	if !ok || ident == nil || a.storeFlowParams[ident.Name] || a.storeFlowLocals[ident.Name] {
		return nil
	}
	// The resolver may have qualified the callee (`Module.name`): match by last segment, and
	// by the full name too when that segment is shared.
	short := fillMayAdoptLastSegment(ident.Name)
	a.callEdgesOf(nil) // builds callDeclsByName
	candidates := a.callDeclsByName[short]
	if len(candidates) > 1 && short != ident.Name {
		var exact []*ast.FuncDecl
		for _, decl := range candidates {
			if decl.Name == ident.Name {
				exact = append(exact, decl)
			}
		}
		candidates = exact
	}
	if len(candidates) != 1 || a.funcDeclSymbols[candidates[0]] == nil || len(a.ufcsFunctionsByName[short]) > 1 {
		return nil
	}
	if !storeFlowCallFitsParams(candidates[0], call) {
		return nil
	}
	return candidates[0]
}
