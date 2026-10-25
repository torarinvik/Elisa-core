package semantic

import (
	"fmt"
	"os"
	"reflect"

	"elisacore/src/ast"
)

// Reassignment reclaim (docs/91 G1, first sound slice).
//
// `x <- f(..)` inside a loop on a local `darray[T]` builds a fresh buffer every iteration while the
// old one stays allocated until the region dies (a 2.2 GB peak in the Base64 benchmark). The old
// buffer may be handed back to its arena's free list at the assignment only when NOTHING else can
// still point into it. The arena never reclaims on its own because a darray header copy shares the
// buffer (arena_realloc's comment); this pass proves the no-copy condition for a narrow shape:
//
//   - x is a function-local `mutable darray[T]` with scalar elements, declared once, never shadowed;
//   - every value x ever holds is `[]` or the result of a FRESH-RETURN function (below), and the
//     right-hand side never mentions x;
//   - every other mention of x is `x.count` or an `&x` argument to a NON-STASHING callee through an
//     immutable reference parameter.
//
// A non-stashing function has only parameters, a return type and effects that cannot hold or
// publish a borrow (scalars, darrays of scalars, references to those; effects limited to Memory,
// Abort and Console; no extern or trusted code anywhere in its call closure), so no pointer into
// x's buffer can survive a call to it. A fresh-return function is non-stashing and returns only
// locals it built from `[]` with builtin growth/index operations, so its result shares no buffer.
//
// Everything else keeps today's behavior (no reclaim): the analysis only ever ADDS frees it can
// prove. The backend reclaims through arena_reclaim_allocation, which ignores a pointer outside
// the arena it is given.

// ELISA_RECLAIM_TRACE=1 prints why a candidate reassignment was not marked.
var reclaimTrace = os.Getenv("ELISA_RECLAIM_TRACE") != ""

func reclaimWhy(fn *ast.FuncDecl, name, why string) {
	if reclaimTrace {
		fmt.Fprintf(os.Stderr, "RECLAIM %s.%s: %s\n", fn.Name, name, why)
	}
}

var reclaimSafeEffects = map[string]bool{"Memory": true, "Abort": true, "Console": true}

// reclaimLocalMethods are the builtin darray operations a fresh-return local may be the receiver
// of: they grow, read or write the buffer in place and never copy its header.
var reclaimLocalMethods = map[string]bool{
	"count": true, "push": true, "resize": true, "reserve": true, "extend": true, "append": true,
	"clear": true, "truncate": true, "pop": true, "insert": true, "capacity": true,
}

type reclaimAnalysis struct {
	a          *Analyzer
	clean      map[*ast.FuncDecl]bool
	cleanBusy  map[*ast.FuncDecl]bool
	freshCache map[*ast.FuncDecl]bool
}

// computeReassignReclaims returns the assignments whose target's previous darray buffer is dead.
func (a *Analyzer) computeReassignReclaims(funcs []*ast.FuncDecl) map[*ast.AssignStmt]bool {
	r := &reclaimAnalysis{a: a, clean: map[*ast.FuncDecl]bool{}, cleanBusy: map[*ast.FuncDecl]bool{}, freshCache: map[*ast.FuncDecl]bool{}}
	out := map[*ast.AssignStmt]bool{}
	for _, fn := range funcs {
		if fn == nil || fn.Body == nil || len(fn.TypeParams) > 0 {
			continue
		}
		for _, assign := range r.functionReclaims(fn) {
			out[assign] = true
		}
	}
	return out
}

// scalarOnlyType reports whether a value of type t can hold no pointer at all except into its own
// darray buffers (scalars, fixed arrays and darrays of scalars, structs of those).
func scalarOnlyType(t Type, seen map[Type]bool) bool {
	switch tt := StripAggregateStateType(t).(type) {
	case *BuiltinType:
		switch tt.Name {
		case "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "usize", "isize", "f32", "f64", "bool", "char", "void", "int", "uint", "byte", "rune":
			return true
		}
		return false
	case *BitIntType:
		return true
	case *ArrayType:
		return scalarOnlyType(tt.Elem, seen)
	case *DArrayType:
		return scalarOnlyType(tt.Elem, seen)
	case *StructType:
		if tt == nil || tt.Store {
			return false
		}
		if seen[tt] {
			return true
		}
		seen[tt] = true
		for _, field := range tt.Fields {
			if !scalarOnlyType(field.Type, seen) {
				return false
			}
		}
		return true
	}
	return false
}

// scalarOnlyParamType additionally admits a reference to a scalar-only referent: the reference
// itself is the parameter and cannot be stored anywhere the callee can reach.
func scalarOnlyParamType(t Type) bool {
	if ref, ok := t.(*RefType); ok && ref != nil {
		return scalarOnlyType(ref.Elem, map[Type]bool{})
	}
	return scalarOnlyType(t, map[Type]bool{})
}

func scalarElemDArray(t Type) bool {
	d, ok := StripAggregateStateType(t).(*DArrayType)
	return ok && d != nil && scalarOnlyType(d.Elem, map[Type]bool{})
}

// nonStashing reports whether fn (and everything it can call) can retain no borrow past its return.
func (r *reclaimAnalysis) nonStashing(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Body == nil || len(fn.TypeParams) > 0 {
		return false
	}
	if done, ok := r.clean[fn]; ok {
		return done
	}
	if r.cleanBusy[fn] {
		return true // recursion adds no new construct; the outer frame decides
	}
	r.cleanBusy[fn] = true
	ok := r.nonStashingUncached(fn)
	delete(r.cleanBusy, fn)
	r.clean[fn] = ok
	return ok
}

func (r *reclaimAnalysis) nonStashingUncached(fn *ast.FuncDecl) bool {
	ft := r.a.funcTypeForRegionPoly(fn)
	if ft == nil || len(ft.TypeParams) > 0 {
		return false
	}
	for i, p := range ft.Params {
		if i >= len(fn.Params) && isImplicitArenaParam(p) {
			// The hidden region-polymorphic arena (`__region_auto`) only receives allocations.
			continue
		}
		if !scalarOnlyParamType(p) {
			return false
		}
	}
	if ft.Return != nil && !scalarOnlyType(ft.Return, map[Type]bool{}) {
		return false
	}
	for _, ref := range ft.PermissionRefs {
		if !reclaimSafeEffects[ref.Name] {
			return false
		}
	}
	for _, ref := range ft.DeclaredPermissionRefs {
		if !reclaimSafeEffects[ref.Name] {
			return false
		}
	}
	ok := true
	walkStoreScanNodes(fn.Body, func(node any) {
		if !ok {
			return
		}
		switch n := node.(type) {
		case *ast.CanStmt:
			for _, ref := range n.Permissions {
				if !reclaimSafeEffects[ref.Name] {
					ok = false
				}
			}
		case *ast.LambdaExpr:
			ok = false
		case *ast.CallExpr:
			if !r.callIsClean(n) {
				ok = false
			}
		}
	})
	return ok
}

// callIsClean: a call to a user function must itself be non-stashing; a method-style call that
// resolves to no user function is a builtin operating on its receiver.
func (r *reclaimAnalysis) callIsClean(call *ast.CallExpr) bool {
	switch f := call.Func.(type) {
	case *ast.Ident:
		if callee, ok := r.a.resolveCalleeFuncDecl(call); ok {
			return r.nonStashing(callee)
		}
		// Not a user function: a builtin conversion / constructor. Refuse anything that names a
		// global symbol we cannot see through (an extern, a function value).
		if sym, ok := r.a.globalScope.Lookup(f.Name); ok && sym != nil {
			if _, isFunc := sym.Type.(*FuncType); isFunc {
				return false
			}
		}
		return true
	case *ast.FieldExpr:
		if f == nil {
			return false
		}
		if reclaimLocalMethods[f.Field] {
			if _, isDArray := StripAggregateStateType(peelRefType(r.a.exprType(f.Object))).(*DArrayType); isDArray {
				return true // builtin in-place container operation on a scalar-only receiver
			}
		}
		if sym, ok := r.a.globalScope.Lookup(f.Field); ok && sym != nil {
			if decl, ok := sym.Node.(*ast.FuncDecl); ok && decl != nil {
				return r.nonStashing(decl)
			}
			if _, isFunc := sym.Type.(*FuncType); isFunc {
				return false
			}
		}
		return true
	}
	return false
}

// freshReturn reports whether every value fn returns is a darray it built itself from `[]`.
func (r *reclaimAnalysis) freshReturn(fn *ast.FuncDecl) bool {
	if done, ok := r.freshCache[fn]; ok {
		return done
	}
	r.freshCache[fn] = false
	ok := r.freshReturnUncached(fn)
	r.freshCache[fn] = ok
	return ok
}

func (r *reclaimAnalysis) freshReturnUncached(fn *ast.FuncDecl) bool {
	if !r.nonStashing(fn) {
		return false
	}
	ft := r.a.funcTypeForRegionPoly(fn)
	if ft == nil || !scalarElemDArray(ft.Return) {
		return false
	}
	returned := map[string]bool{}
	ok := true
	walkStoreScanNodes(fn.Body, func(node any) {
		if ret, isRet := node.(*ast.ReturnStmt); isRet && ret != nil {
			id, isIdent := unwrapReclaimExpr(ret.Value).(*ast.Ident)
			if !isIdent || id == nil {
				ok = false
				return
			}
			returned[id.Name] = true
		}
	})
	if !ok || len(returned) == 0 {
		return false
	}
	for name := range returned {
		if !r.localOnlyGrownInPlace(fn.Body, name, nil) {
			return false
		}
	}
	return true
}

func unwrapReclaimExpr(e ast.Expr) ast.Expr {
	for {
		switch n := e.(type) {
		case *ast.ParenExpr:
			e = n.Inner
		case *ast.MoveExpr:
			e = n.Operand
		default:
			return e
		}
	}
}

func isEmptyListLit(e ast.Expr) bool {
	lit, ok := unwrapReclaimExpr(e).(*ast.ListLitExpr)
	return ok && lit != nil && !lit.Brace && len(lit.Elems) == 0
}

// localOnlyGrownInPlace: `name` is declared exactly once from `[]` and every mention is a builtin
// in-place operation receiver, an index target/read, or a returned value. (extra, if set, admits
// additional mention identifiers the caller has vetted.)
func (r *reclaimAnalysis) localOnlyGrownInPlace(body []ast.Stmt, name string, extra map[*ast.Ident]bool) bool {
	decls := 0
	allowed := map[*ast.Ident]bool{}
	for id := range extra {
		allowed[id] = true
	}
	ok := true
	walkStoreScanNodes(body, func(node any) {
		switch n := node.(type) {
		case *ast.VarDeclStmt:
			if n.Name == name {
				decls++
				if !isEmptyListLit(n.Value) {
					ok = false
				}
			}
		case *ast.ReturnStmt:
			if id, isIdent := unwrapReclaimExpr(n.Value).(*ast.Ident); isIdent && id != nil && id.Name == name {
				allowed[id] = true
			}
		case *ast.FieldExpr:
			if id, isIdent := n.Object.(*ast.Ident); isIdent && id != nil && id.Name == name && reclaimLocalMethods[n.Field] {
				allowed[id] = true
			}
		case *ast.IndexExpr:
			if id, isIdent := n.Object.(*ast.Ident); isIdent && id != nil && id.Name == name {
				allowed[id] = true
			}
		case *ast.AddrOfExpr:
			// `&name[i]` is an interior reference into the buffer.
			if ix, isIndex := unwrapReclaimExpr(n.Operand).(*ast.IndexExpr); isIndex && ix != nil {
				if id, isIdent := ix.Object.(*ast.Ident); isIdent && id != nil && id.Name == name {
					ok = false
				}
			}
		}
	})
	if !ok || decls != 1 {
		return false
	}
	return reclaimAllMentionsAllowed(body, name, allowed)
}

// reclaimAllMentionsAllowed checks every identifier spelled `name` is in allowed and that no other
// binder (pattern, parameter, lambda parameter, ...) reuses the name.
func reclaimAllMentionsAllowed(body []ast.Stmt, name string, allowed map[*ast.Ident]bool) bool {
	ok := true
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value, field string)
	rec = func(v reflect.Value, field string) {
		if !ok || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			switch n := v.Interface().(type) {
			case *ast.Ident:
				if n.Name == name && !allowed[n] {
					ok = false
				}
				return
			case *ast.VarDeclStmt:
				// The single declaration was counted by the caller; its Name is not a mention.
				if n.Value != nil {
					rec(reflect.ValueOf(&n.Value).Elem(), "Value")
				}
				return
			case *ast.FieldExpr:
				rec(reflect.ValueOf(&n.Object).Elem(), "Object")
				return
			}
			rec(v.Elem(), field)
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem(), field)
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			t := v.Type()
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i), t.Field(i).Name)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i), field)
			}
		case reflect.String:
			// Capture lists name the binding without using it; any other string spelling the name
			// may be a binder (pattern, lambda parameter) that shadows it.
			if field != "Captures" && v.String() == name {
				ok = false
			}
		}
	}
	rec(reflect.ValueOf(body), "")
	return ok
}

// functionReclaims finds the reclaimable reassignments in one function.
func (r *reclaimAnalysis) functionReclaims(fn *ast.FuncDecl) []*ast.AssignStmt {
	candidates := map[string]*ast.VarDeclStmt{}
	counts := map[string]int{}
	reassignedFromCall := map[string]bool{}
	walkStoreScanNodes(fn.Body, func(node any) {
		switch n := node.(type) {
		case *ast.VarDeclStmt:
			if n != nil && n.Name != "" {
				counts[n.Name]++
				candidates[n.Name] = n
			}
		case *ast.AssignStmt:
			if id, ok := n.Target.(*ast.Ident); ok && id != nil {
				if _, isCall := unwrapReclaimExpr(n.Value).(*ast.CallExpr); isCall {
					reassignedFromCall[id.Name] = true
				}
			}
		}
	})
	var out []*ast.AssignStmt
	for name, vd := range candidates {
		if counts[name] != 1 || !reassignedFromCall[name] || !scalarElemDArray(r.a.exprType(vd.Value)) {
			continue
		}
		for _, p := range fn.Params {
			if p.Name == name {
				vd = nil
			}
		}
		if vd == nil {
			continue
		}
		out = append(out, r.localReclaims(fn, vd)...)
	}
	return out
}

func (r *reclaimAnalysis) isFreshValue(e ast.Expr, name string) bool {
	if isEmptyListLit(e) {
		return true
	}
	call, ok := unwrapReclaimExpr(e).(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	if _, isIdent := call.Func.(*ast.Ident); !isIdent {
		return false
	}
	if storeScanMentions(call, map[string]bool{name: true}) {
		return false
	}
	callee, ok := r.a.resolveCalleeFuncDecl(call)
	return ok && r.freshReturn(callee)
}

func (r *reclaimAnalysis) localReclaims(fn *ast.FuncDecl, vd *ast.VarDeclStmt) []*ast.AssignStmt {
	name := vd.Name
	sym := r.a.exprType(vd.Value)
	if vd.Value == nil || !scalarElemDArray(sym) || !r.isFreshValue(vd.Value, name) {
		reclaimWhy(fn, name, "initial value is not a fresh scalar darray")
		return nil
	}
	allowed := map[*ast.Ident]bool{}
	var assigns []*ast.AssignStmt
	ok := true
	walkStoreScanNodes(fn.Body, func(node any) {
		if !ok {
			return
		}
		switch n := node.(type) {
		case *ast.AssignStmt:
			id, isIdent := n.Target.(*ast.Ident)
			if !isIdent || id == nil || id.Name != name {
				return
			}
			if n.Optional || n.WriteThrough || n.ArgManifest || n.AsOverlayCall != nil || !r.isFreshValue(n.Value, name) {
				reclaimWhy(fn, name, fmt.Sprintf("assignment at line %d is not a fresh value", n.Pos().Line))
				ok = false
				return
			}
			allowed[id] = true
			assigns = append(assigns, n)
		case *ast.FieldExpr:
			if id, isIdent := n.Object.(*ast.Ident); isIdent && id != nil && id.Name == name && n.Field == "count" {
				allowed[id] = true
			}
		case *ast.CallExpr:
			for j, arg := range n.Args {
				addr, isAddr := unwrapReclaimExpr(arg).(*ast.AddrOfExpr)
				if !isAddr || addr == nil {
					continue
				}
				id, isIdent := unwrapReclaimExpr(addr.Operand).(*ast.Ident)
				if !isIdent || id == nil || id.Name != name {
					continue
				}
				if r.immutableRefArgToNonStashing(n, j) {
					allowed[id] = true
				}
			}
		}
	})
	if !ok || len(assigns) == 0 {
		return nil
	}
	if !reclaimAllMentionsAllowed(fn.Body, name, allowed) {
		reclaimWhy(fn, name, "a mention may copy, view or retain the buffer")
		return nil
	}
	return assigns
}

func isImplicitArenaParam(t Type) bool {
	st, ok := StripAggregateStateType(peelRefType(t)).(*StructType)
	return ok && st != nil && st.Name == "Arena"
}

func peelRefType(t Type) Type {
	for {
		ref, ok := t.(*RefType)
		if !ok || ref == nil {
			return t
		}
		t = ref.Elem
	}
}

func (r *reclaimAnalysis) immutableRefArgToNonStashing(call *ast.CallExpr, index int) bool {
	callee, ok := r.a.resolveCalleeFuncDecl(call)
	if !ok || !r.nonStashing(callee) {
		return false
	}
	ft := r.a.funcTypeForRegionPoly(callee)
	if ft == nil || index >= len(ft.Params) || len(call.Args) != len(callee.Params) {
		return false
	}
	ref, isRef := ft.Params[index].(*RefType)
	return isRef && ref != nil && !ref.Mutable && !ref.Linear
}
