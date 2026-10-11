package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// containerHeadersStable proves that parameter headers cannot be replaced or
// retained through any reachable direct helper. Unknown targets fail closed.
// The visited set closes recursive call cycles without assuming an unchecked
// callee is safe: every reachable declaration is still inspected once.
func (a *Analyzer) containerHeadersStable(root *ast.FuncDecl) bool {
	checked := map[*ast.FuncDecl]bool{}
	var check func(*ast.FuncDecl) bool
	check = func(fn *ast.FuncDecl) bool {
		if fn == nil || len(fn.Body) == 0 || len(fn.TypeParams) != 0 {
			return false
		}
		if checked[fn] {
			return true
		}
		checked[fn] = true
		params := map[string]bool{}
		shadowed := map[string]bool{}
		ft := a.funcTypeForRegionPoly(fn)
		if ft == nil {
			return false
		}
		borrowedReturn := headerAliasingInput(ft.Return) || a.typeCarriesBorrowedStorage(ft.Return, map[Type]bool{})
		for i, p := range fn.Params {
			if i < len(ft.Params) && headerAliasingInput(ft.Params[i]) {
				params[p.Name] = true
			}
			shadowed[p.Name] = true
		}
		// Reject global-target lookup whenever a local declaration could shadow it.
		var locals func(reflect.Value)
		localSeen := map[any]bool{}
		locals = func(v reflect.Value) {
			if !v.IsValid() || !v.CanInterface() {
				return
			}
			switch v.Kind() {
			case reflect.Interface:
				if !v.IsNil() {
					locals(v.Elem())
				}
			case reflect.Pointer:
				if v.IsNil() || localSeen[v.Interface()] {
					return
				}
				localSeen[v.Interface()] = true
				if _, ok := v.Interface().(*ast.Ident); ok {
					return
				}
				if n, ok := v.Interface().(*ast.VarDeclStmt); ok {
					shadowed[n.Name] = true
				}
				locals(v.Elem())
			case reflect.Struct:
				for i := 0; i < v.NumField(); i++ {
					if (v.Type().Field(i).Name == "Name" || v.Type().Field(i).Name == "Binder") && v.Field(i).Kind() == reflect.String {
						shadowed[v.Field(i).String()] = true
					}
					locals(v.Field(i))
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					locals(v.Index(i))
				}
			}
		}
		locals(reflect.ValueOf(fn.Body))
		seen := map[any]bool{}
		var walk func(reflect.Value) bool
		var place func(ast.Expr) bool
		place = func(e ast.Expr) bool {
			e = stripOptimizationParens(e)
			if _, ok := e.(*ast.Ident); ok {
				return true
			}
			return walk(reflect.ValueOf(e))
		}
		walk = func(v reflect.Value) bool {
			if !v.IsValid() || !v.CanInterface() {
				return true
			}
			switch v.Kind() {
			case reflect.Interface:
				if v.IsNil() {
					return true
				}
				return walk(v.Elem())
			case reflect.Pointer:
				if v.IsNil() || seen[v.Interface()] {
					return true
				}
				seen[v.Interface()] = true
				switch n := v.Interface().(type) {
				case *ast.ReturnStmt:
					// Reference returns may take an element address implicitly.
					if borrowedReturn && headerReturnTouchesParam(n.Value, params) {
						return false
					}
				case *ast.AssignStmt:
					if params[containerHeaderRoot(n.Target)] {
						return false
					}
				case *ast.AugAssignStmt:
					if params[containerHeaderRoot(n.Target)] {
						return false
					}
				case *ast.AsRefAssignStmt:
					if params[containerHeaderRoot(n.Target)] {
						return false
					}
				case *ast.CastExpr:
					if _, reference := n.Target.(*ast.RefType); reference && params[containerBorrowRoot(n.Operand)] {
						return false
					}
				case *ast.AddrOfExpr:
					if params[containerBorrowRoot(n.Operand)] {
						return false
					}
				case *ast.Ident:
					return !params[n.Name]
				case *ast.FieldExpr:
					if n.Field == "items" && params[containerBorrowRoot(n.Object)] {
						return false
					}
					return place(n.Object)
				case *ast.IndexExpr:
					return place(n.Object) && walk(reflect.ValueOf(n.Index)) && walk(reflect.ValueOf(n.Fallback))
				case *ast.CallExpr:
					args := n.Args
					if n.ResolvedArgsValid {
						args = n.ResolvedArgs
					}
					args = append(append([]ast.Expr(nil), args...), n.ResolvedImplicitArgs...)
					if field, ok := n.Func.(*ast.FieldExpr); ok && !walk(reflect.ValueOf(field.Object)) {
						return false
					}
					if !walk(reflect.ValueOf(n.SafeReceiver)) {
						return false
					}
					forwarded := false
					for _, arg := range args {
						e := stripOptimizationParens(arg)
						if addr, ok := e.(*ast.AddrOfExpr); ok {
							e = stripOptimizationParens(addr.Operand)
						}
						if id, ok := e.(*ast.Ident); ok && params[id.Name] {
							forwarded = true
							continue
						}
						if !walk(reflect.ValueOf(arg)) {
							return false
						}
					}
					if forwarded {
						id, ok := n.Func.(*ast.Ident)
						if !ok || shadowed[id.Name] || a.globalScope == nil {
							return false
						}
						decl, ok := a.uniqueHeaderCallTarget(n, shadowed)
						if !ok || !check(decl) {
							return false
						}
					}
					return walk(reflect.ValueOf(n.Func))
				}
				return walk(v.Elem())
			case reflect.Struct:
				for i := 0; i < v.NumField(); i++ {
					if !walk(v.Field(i)) {
						return false
					}
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					if !walk(v.Index(i)) {
						return false
					}
				}
			}
			return true
		}
		return walk(reflect.ValueOf(fn.Body))
	}
	return check(root)
}

type headerFunctionNameIndex struct {
	declCount int
	counts    map[string]int
}

// uniqueHeaderCallTarget never uses the candidate call's local scope to resolve
// another AST node. Local binding names and duplicate global declarations miss.
func (a *Analyzer) uniqueHeaderCallTarget(call *ast.CallExpr, shadowed map[string]bool) (*ast.FuncDecl, bool) {
	if call == nil || a.globalScope == nil {
		return nil, false
	}
	id, ok := call.Func.(*ast.Ident)
	if !ok || shadowed[id.Name] {
		return nil, false
	}
	sym, ok := a.globalScope.Lookup(id.Name)
	if !ok || sym == nil {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		return nil, false
	}
	if a.headerFunctionNames.counts == nil || a.headerFunctionNames.declCount != len(a.funcDeclSymbols) {
		counts := make(map[string]int)
		for candidate := range a.funcDeclSymbols {
			if candidate != nil {
				counts[candidate.Name]++
			}
		}
		a.headerFunctionNames = headerFunctionNameIndex{declCount: len(a.funcDeclSymbols), counts: counts}
	}
	return decl, a.headerFunctionNames.counts[decl.Name] == 1
}

func headerLocalBindingNames(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	for _, p := range fn.Params {
		names[p.Name] = true
	}
	seen := map[any]bool{}
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || seen[v.Interface()] {
				return
			}
			seen[v.Interface()] = true
			if _, ok := v.Interface().(*ast.Ident); ok {
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if (v.Type().Field(i).Name == "Name" || v.Type().Field(i).Name == "Binder") && v.Field(i).Kind() == reflect.String {
					names[v.Field(i).String()] = true
				}
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(fn.Body))
	return names
}

// Indexing reaches an element, while field access on the header can expose or
// replace its backing pointer. Keep these places distinct.
func containerHeaderRoot(e ast.Expr) string {
	switch n := stripOptimizationParens(e).(type) {
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		return containerHeaderRoot(n.Object)
	case *ast.MoveExpr:
		return containerHeaderRoot(n.Operand)
	case *ast.CastExpr:
		return containerHeaderRoot(n.Operand)
	default:
		return ""
	}
}

// Header copies passed by value still share their element storage.
func headerAliasingInput(t Type) bool {
	if isContainerRefType(t) {
		return true
	}
	_, ok := StripAggregateStateType(t).(*DArrayType)
	return ok
}

// Taking an element address can retain the backing buffer even though writing
// the element does not replace the header.
func containerBorrowRoot(e ast.Expr) string {
	e = stripOptimizationParens(e)
	switch n := e.(type) {
	case *ast.IndexExpr:
		return containerBorrowRoot(n.Object)
	case *ast.FieldExpr:
		return containerBorrowRoot(n.Object)
	case *ast.MoveExpr:
		return containerBorrowRoot(n.Operand)
	case *ast.CastExpr:
		return containerBorrowRoot(n.Operand)
	}
	return containerHeaderRoot(e)
}

// Borrow-carrying returns can hide an implicit element address in a block,
// conditional, or aggregate. Inspect all source roots, not only a place chain.
func headerReturnTouchesParam(root ast.Expr, params map[string]bool) bool {
	seen := map[any]bool{}
	var walk func(reflect.Value) bool
	walk = func(v reflect.Value) bool {
		if !v.IsValid() || !v.CanInterface() {
			return false
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				return walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || seen[v.Interface()] {
				return false
			}
			seen[v.Interface()] = true
			if id, ok := v.Interface().(*ast.Ident); ok {
				return params[id.Name]
			}
			return walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if walk(v.Field(i)) {
					return true
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				if walk(v.Index(i)) {
					return true
				}
			}
		}
		return false
	}
	return walk(reflect.ValueOf(root))
}
