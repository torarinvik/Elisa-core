package regionframesyntax

import "elisacore/src/ast"

func HasArenaParam(fn *ast.FuncDecl) bool {
	for i := range fn.Params {
		if TypeExprNamedIs(fn.Params[i].Type, "Arena") {
			return true
		}
	}
	return false
}

// TypeExprNamedIs reports whether a type expression names `name` after stripping `mutable`/`&`.
func TypeExprNamedIs(typ ast.TypeExpr, name string) bool {
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

// TypeExprIsRegionless reports whether a container local's declared type carries NO explicit `@r`
// region annotation — only a region-less container literal lands in the inferred (adoptable) region.
// A nil (untyped) declaration is conservatively NOT treated as region-less build-local-return: the
// typed `out: darray[T] = []` form is the Stage 1 canonical shape (and matches join/make signatures).
func TypeExprIsRegionless(typ ast.TypeExpr) bool {
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

// ReturnTypeIsOwnedContainer reports whether a function's return-type annotation is a growable owned
// container — `darray`/`dict`/`set` (BuiltinTypeExpr) or `dstr` (NamedType, the u8 darray). Views
// (`sview`/`cstr`) and `array` (fixed) are excluded: they are not region-owned growables.
func ReturnTypeIsOwnedContainer(typ ast.TypeExpr) bool {
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
