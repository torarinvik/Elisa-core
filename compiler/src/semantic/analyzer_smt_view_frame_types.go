package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (g *sviewFrameGate) cloneTypes() map[string]Type {
	copy := make(map[string]Type, len(g.types))
	for name, typ := range g.types {
		copy[name] = typ
	}
	return copy
}

// Whole-body inspection can precede operator lowering. Resolve receiver types
// without analyzing expressions or borrowing a same-named outer binding for a
// local declared in the inspected body. Unknown types decline overloadable ops.
func (g *sviewFrameGate) receiverType(expr ast.Expr, depth int) Type {
	if expr == nil || depth > 32 {
		return nil
	}
	expr = stripOptimizationParens(expr)
	if id, ok := expr.(*ast.Ident); ok {
		if typ, local := g.types[id.Name]; local {
			return typ
		}
	}
	if typ := g.a.exprType(expr); typ != nil {
		return typ
	}
	if id, ok := expr.(*ast.Ident); ok && g.decl != nil {
		// Resolve only the inspected function's own immutable constant. Its
		// declaration symbol, not the caller scope, identifies the namespace.
		if fn := g.a.funcDeclSymbols[g.decl]; fn != nil && fn.Node == g.decl && g.a.globalScope != nil {
			name := joinQualifiedName(privateOwnerNamespace(fn.Name), id.Name)
			if sym, found := g.a.globalScope.Lookup(name); found && sym != nil && sym.Kind == SymbolConst && !sym.Mutable && shortCircuitPrimitiveType(sym.Type) {
				return sym.Type
			}
		}
		return nil
	}

	switch n := expr.(type) {
	case *ast.IntLit:
		if n.Suffix != "" {
			return g.a.namedTypes[n.Suffix]
		}
		return g.a.namedTypes["int"]
	case *ast.BoolLit:
		return g.a.namedTypes["bool"]
	case *ast.StringLit:
		// Its contextual runtime type is not necessarily a view. Wait for the
		// analyzed type rather than inventing an operator receiver.
		return nil
	case *ast.CharLit:
		return g.a.namedTypes["char"]
	case *ast.CastExpr:
		if named, ok := n.Target.(*ast.NamedType); ok && named != nil {
			typ, _, _ := g.a.lookupVisibleType(named.Name)
			return typ
		}
	case *ast.StructLitExpr:
		typ, _, _ := g.a.lookupVisibleType(n.Name)
		return typ
	case *ast.FieldExpr:
		typ := stripRefForBounds(g.receiverType(n.Object, depth+1))
		// Frame inspection can precede typing of a tuple-result projection.
		// Its declared field type is already known; waiting for exprTypes here
		// cached a false write-free result for read-only fold invariants.
		if tuple, ok := StripAggregateStateType(typ).(*TupleType); ok && tuple != nil {
			for _, field := range tuple.Fields {
				if field.Name == n.Field {
					// A generic callee's declared return can still contain its
					// own parameters before call specialization. Such a field
					// cannot establish that an operator has no implementation.
					if containsTypeParam(field.Type) {
						return nil
					}
					return field.Type
				}
			}
			return nil
		}
		if st, ok := StripAggregateStateType(typ).(*StructType); ok && st != nil {
			if field, found := st.Fields[n.Field]; found {
				return field.Type
			}
		}
		if _, view := typ.(*SViewType); view {
			if n.Field == "len" {
				return g.a.namedTypes["i64"]
			}
			if n.Field == "data" {
				return &RefType{Elem: g.a.namedTypes["u8"]}
			}
		}
		if array, ok := typ.(*DArrayType); ok {
			if n.Field == "count" || n.Field == "capacity" {
				return g.a.namedTypes["usize"]
			}
			if n.Field == "items" {
				return &RefType{Elem: array.Elem}
			}
		}
	case *ast.SliceExpr:
		if _, view := stripRefForBounds(g.receiverType(n.Object, depth+1)).(*SViewType); view {
			return &SViewType{}
		}
	case *ast.IndexExpr:
		typ := g.receiverType(n.Object, depth+1)
		switch t := stripRefForBounds(typ).(type) {
		case *ArrayType:
			return t.Elem
		case *DArrayType:
			return t.Elem
		case *SViewType:
			return g.a.namedTypes["u8"]
		}
		if ref, ok := typ.(*RefType); ok && ref != nil {
			return ref.Elem
		}
	case *ast.CallExpr:
		if decl, ok := g.a.classifierFrameCallDecl(n); ok {
			if sym, found := g.a.symbolForFuncDecl(decl); found && sym != nil {
				if fn, ok := sym.Type.(*FuncType); ok && fn != nil {
					return fn.Return
				}
			}
		}
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			return g.a.namedTypes["bool"]
		}
		return g.receiverType(n.Operand, depth+1)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR, lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ,
			lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_IS, lexer.TOKEN_IN:
			return g.a.namedTypes["bool"]
		}
		return g.receiverType(n.Left, depth+1)
	case *ast.TernaryExpr:
		return g.receiverType(n.Value, depth+1)
	}
	return g.a.smtOperandType(expr)
}

func (g *sviewFrameGate) binaryHasNoOverload(n *ast.BinaryExpr) bool {
	method := ""
	switch n.Op {
	case lexer.TOKEN_IN:
		_, rangeOnly := stripOptimizationParens(n.Right).(*ast.MembershipRangeExpr)
		return rangeOnly
	case lexer.TOKEN_PLUS:
		method = "__add__"
	case lexer.TOKEN_MINUS:
		method = "__sub__"
	case lexer.TOKEN_STAR:
		method = "__mul__"
	case lexer.TOKEN_SLASH:
		method = "__div__"
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		method = "__eq__"
	case lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ:
		method = "__cmp__"
	default:
		return true
	}
	typ := g.receiverType(n.Left, 0)
	if isNumericComparisonOperator(n.Op) && isUnsuffixedIntegerValueExpr(n.Left) && !isUnsuffixedIntegerValueExpr(n.Right) {
		if other := valueContextOperandType(g.receiverType(n.Right, 0)); IsIntegralStorageType(other) {
			typ = other
		}
	}
	if typ == nil {
		return false
	}
	_, overloaded := g.a.buildOverloadCall(n, method, typ)
	return !overloaded
}

func (g *sviewFrameGate) unaryHasNoOverload(n *ast.UnaryExpr) bool {
	typ := g.receiverType(n.Operand, 0)
	if typ == nil {
		return false
	}
	if impl, ok := g.a.staticImplMethodForReceiver(typ, "__neg__", nil); ok && impl != nil {
		return false
	}
	if _, generic := StripAggregateStateType(unwrapReceiverRef(typ)).(*TypeParamType); generic {
		return false
	}
	return true
}

func (g *sviewFrameGate) builtinIndex(object ast.Expr) bool {
	typ := g.receiverType(object, 0)
	switch stripRefForBounds(typ).(type) {
	case *ArrayType, *DArrayType, *SViewType:
		return true
	}
	if ref, ok := typ.(*RefType); ok && ref != nil {
		_, _, integer := smtIntWidthSign(ref.Elem)
		return integer
	}
	return false
}

func (g *sviewFrameGate) quantifier(n *ast.QuantifierExpr, depth int) bool {
	if n.In != nil && !g.expr(n.In, depth+1) {
		return false
	}
	var typ Type = g.a.namedTypes["int"]
	if n.In != nil {
		typ = nil
		if bounds, ok := stripOptimizationParens(n.In).(*ast.MembershipRangeExpr); ok {
			typ = g.receiverType(bounds.Start, 0)
		} else {
			switch collection := stripRefForBounds(g.receiverType(n.In, 0)).(type) {
			case *ArrayType:
				typ = collection.Elem
			case *DArrayType:
				typ = collection.Elem
			case *SViewType:
				typ = g.a.namedTypes["u8"]
			}
		}
	}
	savedTypes := g.cloneTypes()
	savedBindings := g.rebindable
	g.rebindable = make(map[string]bool, len(savedBindings)+len(n.Vars))
	for name, binding := range savedBindings {
		g.rebindable[name] = binding
	}
	defer func() { g.types = savedTypes; g.rebindable = savedBindings }()
	for _, name := range n.Vars {
		g.types[name] = typ
		g.rebindable[name] = false
	}
	return g.expr(n.Body, depth+1)
}

// A borrowed view header copies two fields; it neither allocates nor writes the
// pointed-to bytes. Exclude resources, destructors and owning pointer fields.
func sviewFrameHeaderType(typ Type) bool {
	st, ok := StripAggregateStateType(typ).(*StructType)
	if !ok || st == nil || st.Name != "StringView" || st.Affine || st.Resource || st.DropHook != "" || len(st.Fields) != 2 {
		return false
	}
	data, dataOK := st.Fields["data"]
	length, lengthOK := st.Fields["len"]
	ref, refOK := data.Type.(*RefType)
	if !dataOK || !lengthOK || !refOK || ref == nil || ref.Mutable || ref.Storage != RefStorageAny {
		return false
	}
	byteType, byteOK := ref.Elem.(*BuiltinType)
	signed, bits, integer := smtIntWidthSign(length.Type)
	return byteOK && byteType.Name == "u8" && integer && signed && bits == 64
}
