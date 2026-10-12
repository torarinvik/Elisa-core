package semantic

import "elisacore/src/ast"

// Resolve only constructor projections, never mutable/local variable snapshots.
// The depth bound also declines malformed cyclic or excessively nested ASTs.
func (a *Analyzer) nestedLiteralFieldValue(field *ast.FieldExpr, depth int) (ast.Expr, bool) {
	if field == nil || depth > 32 {
		return nil, false
	}
	object := stripOptimizationParens(field.Object)
	if inner, ok := object.(*ast.FieldExpr); ok {
		value, found := a.nestedLiteralFieldValue(inner, depth+1)
		if !found {
			return nil, false
		}
		object = stripOptimizationParens(value)
	}
	switch literal := object.(type) {
	case *ast.TupleExpr:
		return a.tupleLiteralFieldValue(literal, field.Field)
	case *ast.StructLitExpr:
		if !literal.ResolvedArgsValid && (literal.CopyBase != nil || len(literal.Spreads) != 0) {
			return nil, false
		}
		args := literal.LoweredArgs()
		typ, _ := stripRefForBounds(a.exprTypes[literal]).(*StructType)
		var result ast.Expr
		for index, arg := range args {
			name := literal.ArgName(index)
			if name == "" {
				if typ == nil || typ.Decl == nil || index >= len(typ.Decl.Fields) {
					return nil, false
				}
				name = typ.Decl.Fields[index].Name
			}
			if name == field.Field {
				if result != nil || arg == nil {
					return nil, false
				}
				result = arg
			}
		}
		return result, result != nil
	}
	return nil, false
}
