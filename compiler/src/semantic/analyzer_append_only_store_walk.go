package semantic

// The body walk of the `@append_only` store check (rules in analyzer_append_only_store.go).

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func aoNil(n interface{}) bool {
	if n == nil {
		return true
	}
	v := reflect.ValueOf(n)
	return (v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface) && v.IsNil()
}

// The store a place expression denotes (a store-typed local/param or struct field), or "".
func (f *aoFacts) placeStore(env *aoEnv, e ast.Expr) string {
	switch ee := e.(type) {
	case *ast.Ident:
		return env.locals[ee.Name]
	case *ast.FieldExpr:
		return f.fieldStore[ee.Field]
	case *ast.ParenExpr:
		return f.placeStore(env, ee.Inner)
	}
	return ""
}

func aoFieldObject(e ast.Expr) ast.Expr {
	if fe, ok := e.(*ast.FieldExpr); ok {
		return fe.Object
	}
	return e
}

func (f *aoFacts) isCurrent(env *aoEnv, e ast.Expr) bool {
	fe, ok := e.(*ast.FieldExpr)
	return ok && env.store != "" && f.stores[env.store].current == fe.Field
}

func (f *aoFacts) isRetired(env *aoEnv, e ast.Expr) bool {
	fe, ok := e.(*ast.FieldExpr)
	return ok && env.store != "" && f.stores[env.store].retired == fe.Field
}

func (f *aoFacts) isBuffer(env *aoEnv, field string) bool {
	if env.store == "" {
		return false
	}
	s := f.stores[env.store]
	return s.current == field || s.retired == field
}

func aoIsEmptyArray(e ast.Expr) bool {
	switch ee := e.(type) {
	case *ast.ListLitExpr:
		return len(ee.Elems) == 0 && aoNil(ee.Owner)
	case *ast.ParenExpr:
		return aoIsEmptyArray(ee.Inner)
	}
	return false
}

// The function a callee names and whether it belongs to the store's own module: a bare name
// is the enclosing module's, `M::f` (Ident "M.f") names module M (matched on its trailing
// namespace segments, like stage1's last-segment rule).
func (f *aoFacts) calleeIsOwn(env *aoEnv, callee ast.Expr) (string, bool) {
	id, ok := callee.(*ast.Ident)
	if !ok {
		return "", false
	}
	name := aoLast(id.Name)
	module := env.module
	if len(name) < len(id.Name) {
		prefix := id.Name[:len(id.Name)-len(name)-1]
		module = ""
		for ns := range f.ownerModules() {
			if ns == prefix || (len(ns) > len(prefix) && ns[len(ns)-len(prefix)-1:] == "."+prefix) {
				module = ns
			}
		}
	}
	return name, module != "" && f.moduleFuncs[module+"\x00"+name]
}

func (f *aoFacts) ownerModules() map[string]bool {
	out := map[string]bool{}
	for _, s := range f.stores {
		out[s.module] = true
	}
	return out
}

func (a *Analyzer) aoNode(f *aoFacts, env *aoEnv, n interface{}, slot int) {
	switch nn := n.(type) {
	case ast.Expr:
		a.aoExpr(f, env, nn, slot)
	case ast.Stmt:
		a.aoStmt(f, env, nn)
	case ast.TypeExpr:
		// Types are checked by the declaration/binding rules, not as values.
	default:
		a.aoPattern(f, n)
		aoEachChild(reflect.ValueOf(n), func(c interface{}) { a.aoNode(f, env, c, 0) })
	}
}

func (a *Analyzer) aoPattern(f *aoFacts, n interface{}) {
	name := ""
	var pos lexer.Pos
	switch p := n.(type) {
	case *ast.MatchStructPattern:
		name, pos = aoLast(p.TypeName), p.Position
	case *ast.MoveBindStructPattern:
		name, pos = aoLast(p.TypeName), p.Position
	}
	if _, ok := f.stores[name]; ok && name != "" {
		a.aoReport(pos, name, "may not be destructured by a pattern")
	}
}

func (a *Analyzer) aoChildren(f *aoFacts, env *aoEnv, n interface{}) {
	aoEachChild(reflect.ValueOf(n), func(c interface{}) { a.aoNode(f, env, c, 0) })
}

func (a *Analyzer) aoExpr(f *aoFacts, env *aoEnv, e ast.Expr, slot int) {
	if aoNil(e) {
		return
	}
	if store := f.placeStore(env, e); store != "" {
		if slot != 1 {
			a.aoReport(e.Pos(), store, "may only be passed by reference to its own module's functions")
		}
		if fe, ok := e.(*ast.FieldExpr); ok {
			a.aoExpr(f, env, fe.Object, 1)
		}
		return
	}
	switch ee := e.(type) {
	case *ast.FieldExpr:
		if f.isBuffer(env, ee.Field) {
			detail := "buffer fields may only be read for .count/.capacity, pushed by append, or retired by make_room"
			if env.kind == 0 {
				detail = "only make_room and append may touch the buffer fields"
			} else if env.kind == 1 {
				detail = "make_room may only retire the buffer: retired.push(current), then current <- [], then optionally current.reserve(n)"
			}
			a.aoReport(ee.Position, env.store, detail)
			a.aoExpr(f, env, ee.Object, 1)
		} else if (ee.Field == "count" || ee.Field == "capacity") && env.kind != 0 && (f.isCurrent(env, ee.Object) || f.isRetired(env, ee.Object)) {
			a.aoExpr(f, env, aoFieldObject(ee.Object), 1)
		} else {
			a.aoExpr(f, env, ee.Object, 1)
		}
	case *ast.CallExpr:
		a.aoCall(f, env, ee, slot)
	case *ast.AddrOfExpr:
		a.aoExpr(f, env, ee.Operand, 1)
	case *ast.ParenExpr:
		a.aoExpr(f, env, ee.Inner, slot)
	case *ast.TernaryExpr:
		a.aoExpr(f, env, ee.Cond, 0)
		a.aoExpr(f, env, ee.Value, slot)
		a.aoExpr(f, env, ee.Alt, slot)
	case *ast.StructLitExpr:
		_, isStore := f.stores[aoLast(ee.Name)]
		for _, arg := range ee.Args {
			if isStore && !aoIsEmptyArray(arg) {
				a.aoReport(ee.Position, aoLast(ee.Name), "a store literal takes only empty buffers")
			}
			a.aoExpr(f, env, arg, 2)
		}
		for _, spread := range ee.Spreads {
			if isStore {
				a.aoReport(ee.Position, aoLast(ee.Name), "a store literal takes only empty buffers")
			}
			a.aoExpr(f, env, spread, 0)
		}
	case *ast.RecordUpdateExpr:
		a.aoExpr(f, env, ee.Base, 0)
		for _, arg := range ee.Args {
			a.aoExpr(f, env, arg, 2)
		}
	default:
		a.aoChildren(f, env, e)
	}
}

func (a *Analyzer) aoCall(f *aoFacts, env *aoEnv, call *ast.CallExpr, slot int) {
	if id, ok := call.Func.(*ast.Ident); ok && slot != 2 {
		if _, returns := f.returning[aoLast(id.Name)]; returns {
			a.aoReport(call.Position, aoLast(id.Name), "a fresh store must be bound, stored in a struct literal, or returned")
		}
	}
	a.aoExpr(f, env, call.SafeReceiver, 0)
	if fe, ok := call.Func.(*ast.FieldExpr); ok {
		if store := f.placeStore(env, fe.Object); store != "" {
			a.aoReport(fe.Position, store, "has no methods; pass it to its own module's functions")
			a.aoExpr(f, env, aoFieldObject(fe.Object), 1)
		} else if env.kind == 2 && fe.Field == "push" && f.isCurrent(env, fe.Object) {
			a.aoExpr(f, env, aoFieldObject(fe.Object), 1)
		} else {
			a.aoExpr(f, env, fe.Object, 1)
		}
		for _, arg := range call.Args {
			a.aoExpr(f, env, arg, 0)
		}
		return
	}
	name, own := f.calleeIsOwn(env, call.Func)
	if env.kind == 2 && name == "bytes_view_range" && len(call.Args) == 3 && f.isCurrent(env, call.Args[0]) {
		a.aoExpr(f, env, aoFieldObject(call.Args[0]), 1)
		a.aoExpr(f, env, call.Args[1], 0)
		a.aoExpr(f, env, call.Args[2], 0)
		return
	}
	if name == "" {
		a.aoExpr(f, env, call.Func, 0)
	}
	argSlot := 0
	if own {
		argSlot = 1
	}
	for _, arg := range call.Args {
		a.aoExpr(f, env, arg, argSlot)
	}
}

func (a *Analyzer) aoStmt(f *aoFacts, env *aoEnv, s ast.Stmt) {
	if aoNil(s) {
		return
	}
	switch ss := s.(type) {
	case *ast.ExprStmt:
		a.aoExpr(f, env, ss.Expr, 0)
	case *ast.VarDeclStmt:
		a.aoBind(f, env, ss.Name, ss.Type, ss.Value, ss.Position)
		a.aoExpr(f, env, ss.Owner, 0)
	case *ast.AssignStmt:
		if id, ok := ss.Target.(*ast.Ident); ok && env.locals[id.Name] == "" {
			a.aoBind(f, env, id.Name, nil, ss.Value, ss.Position)
			return
		}
		if store := f.placeStore(env, ss.Target); store != "" {
			a.aoReport(ss.Position, store, "cannot be reassigned")
			if fe, ok := ss.Target.(*ast.FieldExpr); ok {
				a.aoExpr(f, env, fe.Object, 1)
			}
			a.aoExpr(f, env, ss.Value, 2)
			return
		}
		a.aoExpr(f, env, ss.Target, 1)
		a.aoExpr(f, env, ss.Value, 0)
	case *ast.ReturnStmt:
		a.aoExpr(f, env, ss.Value, 2)
	default:
		a.aoChildren(f, env, s)
	}
}

// A binding whose declared type heads at a store makes the name a store place.
func (a *Analyzer) aoBind(f *aoFacts, env *aoEnv, name string, t ast.TypeExpr, value ast.Expr, pos lexer.Pos) {
	head := ""
	isRef := false
	if !aoNil(t) {
		if f.typeNests(t, true) {
			a.aoReport(pos, name, "may not be nested inside another type")
		}
		head = f.typeHead(t)
		isRef = head != "" && aoTypeIsRef(t)
	}
	borrowed := ""
	if addr, ok := aoUnparen(value).(*ast.AddrOfExpr); ok {
		borrowed = f.placeStore(env, addr.Operand)
	}
	if head == "" {
		if call, ok := aoUnparen(value).(*ast.CallExpr); ok {
			if id, ok := call.Func.(*ast.Ident); ok {
				head = f.returning[aoLast(id.Name)]
			}
		}
	}
	if head == "" {
		head = borrowed
	}
	slot := 2
	if isRef || borrowed != "" {
		slot = 1
	}
	a.aoExpr(f, env, value, slot)
	if head != "" {
		env.locals[name] = head
	}
}

func aoUnparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.Inner
	}
}

// 1 = `X.retired.push(X.current)`, 2 = `X.current <- []`, 3 = `X.current.reserve(n)`, 0 else.
func (f *aoFacts) retireStep(env *aoEnv, s ast.Stmt) (int, ast.Expr) {
	switch ss := s.(type) {
	case *ast.ExprStmt:
		call, ok := ss.Expr.(*ast.CallExpr)
		if !ok {
			return 0, nil
		}
		fe, ok := call.Func.(*ast.FieldExpr)
		if !ok || len(call.Args) != 1 {
			return 0, nil
		}
		if fe.Field == "push" && f.isRetired(env, fe.Object) && f.isCurrent(env, call.Args[0]) && aoSameObject(fe.Object, call.Args[0]) {
			return 1, nil
		}
		if fe.Field == "reserve" && f.isCurrent(env, fe.Object) {
			return 3, call.Args[0]
		}
	case *ast.AssignStmt:
		if f.isCurrent(env, ss.Target) && aoIsEmptyArray(ss.Value) {
			return 2, nil
		}
	}
	return 0, nil
}

func aoSameObject(left, right ast.Expr) bool {
	l, lok := aoFieldObject(left).(*ast.Ident)
	r, rok := aoFieldObject(right).(*ast.Ident)
	return lok && rok && l.Name == r.Name
}

// make_room's top level: ordinary statements, and the retire sequence push -> `<- []` ->
// optional reserve. It never frees: no clear, pop, truncate or other buffer operation.
func (a *Analyzer) aoMakeRoom(f *aoFacts, env *aoEnv, body []ast.Stmt, pos lexer.Pos) {
	const detail = "make_room may only retire the buffer: retired.push(current), then current <- [], then optionally current.reserve(n)"
	state := 0
	for _, s := range body {
		step, reserveArg := f.retireStep(env, s)
		switch step {
		case 1:
			if state == 1 {
				a.aoReport(s.Pos(), env.store, detail)
			}
			state = 1
		case 2:
			if state != 1 {
				a.aoReport(s.Pos(), env.store, detail)
			}
			state = 2
		case 3:
			if state != 2 {
				a.aoReport(s.Pos(), env.store, detail)
			}
			a.aoExpr(f, env, reserveArg, 0)
			state = 0
		default:
			if state == 1 {
				a.aoReport(s.Pos(), env.store, detail)
			}
			state = 0
			a.aoStmt(f, env, s)
		}
	}
	if state == 1 {
		a.aoReport(pos, env.store, detail)
	}
}
