package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// warnOnByValueGrownContainerParams flags the by-value-grown-container footgun: a function that
// GROWS (push/extend/insert/…) a growable container parameter passed BY VALUE — `def fill(out:
// mutable darray[u8]): out.push(...)`, with no `&`. Growth reallocates the container's header
// (data pointer + count), but a by-value param holds the caller's header by COPY, so every
// appended element lands in the callee's copy and is invisible to the caller — the classic
// "passed the out-param by value" mistake. The by-reference form (`mutable darray[u8]& @r`,
// region inferred — see inferRegionParamsForGrownContainerParams) makes the growth visible.
//
// This is a non-fatal WARNING, not an error: the code is well-typed and the value-copy semantics
// are legal, just almost never what the author intended for a grown container. To stay
// false-positive-free it is suppressed for the two legitimate by-value-growth idioms:
//   - build-and-return (`def build(seed: darray[u8]) -> darray[u8]: seed.push(..); return seed`):
//     the grown copy reaches the caller via the RETURN value, so nothing is lost.
//   - `owned` move-in (`out: owned darray[u8]`): the caller relinquished the container, so it has
//     no header to be surprised about. (Handled in byValueGrownContainerName by excluding OwnedType.)
//
// A `&` reference param is correct usage (growth IS visible) and is likewise excluded.
func (a *Analyzer) warnOnByValueGrownContainerParams(decls []scopedDecl) {
	for _, fn := range collectRegionPolyCandidateFuncs(decls) {
		if fn == nil || len(fn.Body) == 0 {
			continue
		}
		for i := range fn.Params {
			p := &fn.Params[i]
			name, ok := byValueGrownContainerName(p.Type)
			if !ok {
				continue
			}
			if !paramContainerIsGrown(fn.Body, p.Name) {
				continue
			}
			if paramReturnedBare(fn.Body, p.Name) {
				continue // build-and-return idiom: growth reaches the caller via the result
			}
			a.warnf(p.Position, "by-value %s parameter %q is grown but the growth is not visible to the caller (a by-value container copies its header); pass it by reference (`mutable %s&`, region inferred) or return it", name, p.Name, name)
		}
	}
}

// byValueGrownContainerName reports whether a parameter type is a growable container passed BY
// VALUE — and if so its surface name (darray/dict/set/dstr). `mutable` wrappers are peeled (a
// by-value container must be mutable to be grown at all). A `&` reference (growth visible to the
// caller) and an `owned` move-in (caller relinquished it) are both excluded — those are not the
// footgun.
func byValueGrownContainerName(typ ast.TypeExpr) (string, bool) {
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.OwnedType:
			return "", false
		case *ast.RefType:
			return "", false
		case *ast.BuiltinTypeExpr:
			switch t.Name {
			case "darray", "dict", "set":
				return t.Name, true
			}
			return "", false
		case *ast.NamedType:
			if t.Name == "dstr" {
				return "dstr", true
			}
			return "", false
		default:
			return "", false
		}
	}
}

// paramReturnedBare reports whether the body has a `return param` returning the named parameter as
// a bare identifier — the build-and-return idiom that makes by-value growth observable. Conservative:
// only a bare Ident return counts (a return of a larger expression that happens to embed the param
// is not treated as returning it), so a false negative just re-flags a genuine footgun, never the
// reverse. Scans structurally via reflection, mirroring paramContainerIsGrown.
func paramReturnedBare(stmts []ast.Stmt, name string) bool {
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
				if id, ok := ret.Value.(*ast.Ident); ok && id != nil && id.Name == name {
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
