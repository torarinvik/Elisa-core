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
		typ, _ := stripRefForBounds(a.exprType(literal)).(*StructType)
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
// and immutable scalar/value-record bindings can safely be translated again after construction.
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
	return immutableProjectionRecord(sym.Type, 0)
}

// Immutable records made entirely of immutable scalar fields have no interior
// aliases a later call could mutate. References, containers and mutable fields
// deliberately stay outside this proof, even behind an immutable root binding.
func immutableProjectionRecord(t Type, depth int) bool {
	if depth > 32 || t == nil {
		return false
	}
	if _, reference := t.(*RefType); reference {
		return false
	}
	if _, _, integer := smtIntWidthSign(t); integer {
		return true
	}
	if builtin, ok := t.(*BuiltinType); ok {
		return builtin.Name == "bool"
	}
	record, ok := t.(*StructType)
	if !ok || record == nil || record.Decl == nil {
		return false
	}
	for _, field := range record.Fields {
		if field.Mutable || !immutableProjectionRecord(field.Type, depth+1) {
			return false
		}
	}
	return true
}

// rerootLiteralProjection rewrites `L.f.g` whose object `L.f` projects a constructor literal to the
// component itself, `V.g` (e.g. `(false, fact).next.reachable` -> `fact.reachable`). The component
// was accepted by reevaluableLiteralComponent (re-evaluable at this point), so reading its field here
// equals reading the constructed value's field. The rewritten node carries n's analyzed type.
func (a *Analyzer) rerootLiteralProjection(n *ast.FieldExpr) (*ast.FieldExpr, bool) {
	inner, ok := stripOptimizationParens(n.Object).(*ast.FieldExpr)
	if !ok || inner == nil {
		return nil, false
	}
	value, found := a.reevaluableLiteralComponent(inner, 0)
	if !found || value == nil {
		return nil, false
	}
	rerooted := &ast.FieldExpr{Position: n.Position, Object: value, Field: n.Field}
	if t := a.exprType(n); t != nil {
		a.exprTypes[rerooted] = t
	}
	return rerooted, true
}

// reevaluableLiteralComponent resolves `L.f` (possibly through nested projections) to the literal's
// component, only when that component may be re-evaluated in place of the constructed value: the
// tuple and struct-literal field maps apply the literal re-evaluation rules (no later component
// with side effects), unlike nestedLiteralFieldValue, which serves constant components only.
func (a *Analyzer) reevaluableLiteralComponent(field *ast.FieldExpr, depth int) (ast.Expr, bool) {
	if field == nil || depth > 32 {
		return nil, false
	}
	object := stripOptimizationParens(field.Object)
	if inner, ok := object.(*ast.FieldExpr); ok {
		value, found := a.reevaluableLiteralComponent(inner, depth+1)
		if !found {
			return nil, false
		}
		object = stripOptimizationParens(value)
		if _, tuple := object.(*ast.TupleExpr); !tuple {
			if _, record := object.(*ast.StructLitExpr); !record {
				// The selected literal component can itself be a parameter or another
				// non-literal value. Keep walking the projection path from that value;
				// the recursive call has already established that substituting it is
				// safe with respect to evaluation order.
				projected := &ast.FieldExpr{Position: field.Position, Object: value, Field: field.Field}
				if t := a.exprType(field); t != nil {
					a.exprTypes[projected] = t
				}
				return projected, true
			}
		}
	}
	switch literal := object.(type) {
	case *ast.TupleExpr:
		return a.tupleLiteralFieldValue(literal, field.Field)
	case *ast.StructLitExpr:
		value, found := a.structLitFieldMap(literal)[field.Field]
		return value, found && value != nil
	}
	return nil, false
}
