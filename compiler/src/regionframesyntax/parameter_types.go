package regionframesyntax

import "elisacore/src/ast"

func LocalMayOwnContainer(v *ast.VarDeclStmt, unwrap func(ast.Expr) ast.Expr) bool {
	typ := v.Type
	for {
		if m, ok := typ.(*ast.MutableType); ok && m != nil {
			typ = m.Elem
			continue
		}
		break
	}
	switch t := typ.(type) {
	case *ast.BuiltinTypeExpr:
		return t != nil && (t.Name == "darray" || t.Name == "dstr" || t.Name == "dict" || t.Name == "set")
	case *ast.NamedType:
		if t == nil {
			return false
		}
		switch t.Name {
		case "dstr":
			return true
		case "bool", "u8", "u16", "u32", "u64", "usize", "i8", "i16", "i32", "i64", "isize", "f32", "f64", "char", "sview", "cstr", "uintptr", "void":
			return false
		}
		return true
	case *ast.GenericType, *ast.TupleTypeExpr:
		return true
	case nil:
		switch unwrap(v.Value).(type) {
		case *ast.StructLitExpr, *ast.ListLitExpr, *ast.ListComprehensionExpr, *ast.CallExpr:
			return true
		}
	}
	return false
}

// paramIsWritableStructRef: a `mutable T&` / `lmut T` / `T mutable&` parameter (a place the body
// can store into).
func ParamIsWritableStructRef(p *ast.ParamDecl) bool {
	typ := p.Type
	writable := p.Mutable
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			writable = true
			typ = t.Elem
			continue
		case *ast.OwnedType:
			typ = t.Elem
			continue
		case *ast.RefType:
			_, mutableElem := t.Elem.(*ast.MutableType)
			return writable || mutableElem
		}
		return false
	}
}
