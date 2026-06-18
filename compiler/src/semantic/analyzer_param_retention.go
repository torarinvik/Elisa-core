package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// Interprocedural argument-retention summaries (docs/91, the step the G0 validation showed is the
// real prerequisite). For each function, paramRetained[fn][i] reports whether the i-th (expanded)
// parameter's storage may be RETAINED by the callee — i.e. it can outlive the call (returned, stored
// into longer-lived storage, address-taken, or re-passed to a callee that retains it). A parameter
// that is only READ (indexed, iterated, a method receiver, passed to a non-retaining callee) is NOT
// retained, so a caller passing a value there does not lose it to the callee.
//
// SOUND-CONSERVATIVE direction (principle 2): a parameter is RETAINED unless provably read-only. Any
// ambiguous use, and any call to an unresolved/external callee, conservatively marks RETAINED. So the
// death analysis only ever downgrades an arg-escape to an in-function death when the callee provably
// does not retain it — never freeing something a callee might keep. Monotone fixpoint over the call
// graph (retained only grows), so it terminates; whole-program (docs/91 §8.2).

// retainingMethods are container-mutation methods that store a passed ARGUMENT into the receiver, so
// passing a value there retains it.
var retainingMethods = map[string]bool{
	"push": true, "extend": true, "insert": true, "put": true, "add": true, "append": true,
	"push_front": true, "push_back": true,
}

// resolveCalleeFuncDecl resolves a direct free-function call to its declaration via the GLOBAL scope,
// which (unlike resolveDirectCallFuncDecl's currentScope) persists into this post-analysis pass.
// Unqualified free functions only; method/qualified calls return false (callRetainsArg then treats
// them conservatively as retaining).
func (a *Analyzer) resolveCalleeFuncDecl(call *ast.CallExpr) (*ast.FuncDecl, bool) {
	if call == nil || a.globalScope == nil {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil {
		return nil, false
	}
	sym, ok := a.globalScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil {
		return nil, false
	}
	return decl, true
}

// paramStorageType returns the i-th expanded parameter's RESOLVED type from the function's FuncType
// (computed during analysis), avoiding a side-effecting re-resolve in this post-pass. nil if absent.
func (a *Analyzer) paramStorageType(fn *ast.FuncDecl, i int) Type {
	ft := a.funcTypeForRegionPoly(fn)
	if ft == nil || i < 0 || i >= len(ft.Params) {
		return nil
	}
	return ft.Params[i]
}

// computeParamRetention runs the monotone fixpoint and returns per-function retention bit-vectors
// over expanded parameters.
func (a *Analyzer) computeParamRetention(funcs []*ast.FuncDecl) map[*ast.FuncDecl][]bool {
	retained := map[*ast.FuncDecl][]bool{}
	params := map[*ast.FuncDecl][]ast.ParamDecl{}
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		p := a.expandedFuncDeclParams(fn)
		params[fn] = p
		retained[fn] = make([]bool, len(p))
	}
	for {
		changed := false
		for _, fn := range funcs {
			ps := params[fn]
			for i := range ps {
				if retained[fn][i] || !typeCarriesRegionStorage(a.paramStorageType(fn, i)) {
					continue
				}
				if a.paramRetainedInBody(fn, ps[i].Name, retained) {
					retained[fn][i] = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return retained
}

// callRetainsArg reports whether a direct call retains the storage argument whose base identifier is
// `name`. Unresolved/external callees, and container-mutation method arguments, conservatively retain.
func (a *Analyzer) callRetainsArg(call *ast.CallExpr, name string, retained map[*ast.FuncDecl][]bool) bool {
	// A container-mutation method (`c.push(name)`) stores name into the receiver.
	if fe, ok := call.Func.(*ast.FieldExpr); ok && fe != nil && retainingMethods[fe.Field] {
		for _, arg := range call.Args {
			if base, ok := aliasSourceName(arg); ok && base == name && typeCarriesRegionStorage(a.exprTypes[arg]) {
				return true
			}
		}
	}
	// Find name's storage-carrying argument position.
	argPos := -1
	for j, arg := range call.Args {
		if base, ok := aliasSourceName(arg); ok && base == name && typeCarriesRegionStorage(a.exprTypes[arg]) {
			argPos = j
			break
		}
	}
	if argPos < 0 {
		return false // name is not a direct storage argument of this call
	}
	callee, ok := a.resolveCalleeFuncDecl(call)
	if !ok || callee == nil {
		return true // unresolved / external: conservatively retained
	}
	rv, ok := retained[callee]
	if !ok || argPos >= len(rv) {
		return true // can't map the position: conservative
	}
	return rv[argPos]
}

// paramRetainedInBody reports whether parameter `name` may be retained by fn's body, using the
// current retention summaries for callee positions.
func (a *Analyzer) paramRetainedInBody(fn *ast.FuncDecl, name string, retained map[*ast.FuncDecl][]bool) bool {
	found := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.ReturnStmt:
				// name escapes via return only if the RETURN VALUE carries storage (it could embed
				// name) AND name is mentioned in it. A scalar return (`return xs[0]`, `return total`,
				// `return xs.count`) cannot carry name's storage, so it does not retain — sound and the
				// common reader pattern.
				if n.Value != nil && typeCarriesRegionStorage(a.exprTypes[n.Value]) {
					names := map[string]bool{}
					collectIdentNamesInto(n.Value, names)
					if names[name] {
						found = true
						return
					}
				}
			case *ast.VarDeclStmt:
				// `w = name` (alias out) — w may outlive; conservative retain. (w == name impossible.)
				if base, ok := aliasSourceName(n.Value); ok && base == name && typeCarriesRegionStorage(a.exprTypes[n.Value]) {
					found = true
					return
				}
			case *ast.AssignStmt:
				if tgt, ok := stripParenExpr(n.Target).(*ast.Ident); !ok || tgt == nil || tgt.Name != name {
					if base, ok := aliasSourceName(n.Value); ok && base == name && typeCarriesRegionStorage(a.exprTypes[n.Value]) {
						found = true
						return
					}
				}
			case *ast.AddrOfExpr:
				if base, ok := aliasSourceName(n.Operand); ok && base == name {
					found = true
					return
				}
			case *ast.CallExpr:
				if a.callRetainsArg(n, name, retained) {
					found = true
					return
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).CanInterface() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	for _, s := range fn.Body {
		walk(reflect.ValueOf(s))
		if found {
			break
		}
	}
	return found
}
