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
		typ, _ := stripRefForBounds(a.exprTypes[literal]).(*StructType)
		if literal.ResolvedArgsValid {
			// Resolved arguments use declaration order; ArgNames keeps source order.
			if typ == nil || typ.Decl == nil || len(literal.ResolvedArgs) != len(typ.Decl.Fields) {
				return nil, false
			}
			var result ast.Expr
			for index, declared := range typ.Decl.Fields {
				if declared.Name == field.Field {
					if result != nil || literal.ResolvedArgs[index] == nil {
						return nil, false
					}
					result = literal.ResolvedArgs[index]
				}
			}
			return result, result != nil
		}
		args := literal.Args
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

// Constructor fields capture values before later arguments run. Only constants
// and immutable scalar bindings can safely be translated again after construction.
func (a *Analyzer) constructorProjectionStable(value ast.Expr) bool {
	if _, ok := a.evalConstExpr(value); ok {
		return true
	}
	if unary, ok := stripOptimizationParens(value).(*ast.UnaryExpr); ok {
		if unary.LoweredCall != nil {
			return false
		}
		if _, ident := stripOptimizationParens(unary.Operand).(*ast.Ident); !ident {
			return false
		}
		return a.constructorProjectionStable(unary.Operand)
	}
	id, ok := stripOptimizationParens(value).(*ast.Ident)
	if !ok || a.currentScope == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(id.Name)
	if !found || sym == nil || symbolRootMutable(sym) {
		return false
	}
	if _, _, integer := smtIntWidthSign(sym.Type); integer {
		return true
	}
	if builtin, ok := sym.Type.(*BuiltinType); ok && builtin.Name == "bool" {
		return true
	}
	return false
}
