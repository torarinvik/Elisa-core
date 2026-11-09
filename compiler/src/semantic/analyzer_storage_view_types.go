package semantic

import (
	"elisacore/src/ast"
)

// isEnumVariantConstructorCall reports whether CALL is `Enum.Variant(...)` for a visible
// enum type with that variant. Side-effect free (enumVariantExprType reports errors).
func (a *Analyzer) isEnumVariantConstructorCall(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Object == nil {
		return false
	}
	baseName, ok := qualifiedTypePathFromExpr(field.Object)
	if !ok {
		return false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return false
	}
	enumType, ok := base.(*EnumType)
	if !ok || enumType == nil {
		return false
	}
	_, ok = enumType.Variant(field.Field)
	return ok
}

// storageViewTypeOwnsRelocatable: t (by value) owns storage a mutation can relocate.
func (a *Analyzer) storageViewTypeOwnsRelocatable(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *DArrayType, *DictType, *SetType:
		return true
	case *BuiltinType:
		return isDStrType(tt)
	case *ArrayType:
		return a.storageViewTypeOwnsRelocatable(tt.Elem, seen)
	case *OptionalType:
		return a.storageViewTypeOwnsRelocatable(tt.Value, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if a.storageViewTypeOwnsRelocatable(field.Type, seen) {
				return true
			}
		}
	case *StructType:
		for _, field := range tt.Fields {
			if a.storageViewTypeOwnsRelocatable(field.Type, seen) {
				return true
			}
		}
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := make(map[string]Type, len(base.TypeParams))
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				if a.storageViewTypeOwnsRelocatable(a.substituteType(field.Type, bindings, nil, nil, nil), seen) {
					return true
				}
			}
		}
	}
	return false
}

// storageViewTailValue is the value expression a value-position body ends with.
func storageViewTailValue(body []ast.Stmt) ast.Expr {
	if len(body) == 0 {
		return nil
	}
	if stmt, ok := body[len(body)-1].(*ast.ExprStmt); ok && stmt != nil {
		return stmt.Expr
	}
	return nil
}

// storageViewTopLevelBorrowType: the value IS a borrow (a view, a reference, or an optional
// of one), not an aggregate that merely contains some. A helper returning a freshly built
// aggregate (`semantic_loop_view(&file) -> Ast::File`) is not tracked as a view of its args.
func storageViewTopLevelBorrowType(t Type) bool {
	if opt, ok := t.(*OptionalType); ok && opt != nil {
		t = opt.Value
	}
	switch t.(type) {
	case *RefType, *ViewType, *SViewType, *CStrType:
		return true
	}
	return false
}
