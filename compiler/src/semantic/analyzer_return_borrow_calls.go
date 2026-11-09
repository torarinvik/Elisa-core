package semantic

import (
	"elisacore/src/ast"
)

// returnBorrowCallOtherArgsFlow is what a call may store through its writable argument skip (-1: a
// builtin's receiver): every other argument's flow, where a reference argument contributes its
// ADDRESS when the writable parameter can hold a borrow into the referent and only what the
// referent holds otherwise.
func (a *Analyzer) returnBorrowCallOtherArgsFlow(args []ast.Expr, skip int, params []Type, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	return a.returnBorrowCallOtherArgsFlowWhere(args, skip, params, nil, aliases, active, localBindings)
}

// returnBorrowCallOtherArgsFlowWhere is returnBorrowCallOtherArgsFlow restricted to the arguments
// keep admits (nil admits all).
func (a *Analyzer) returnBorrowCallOtherArgsFlowWhere(args []ast.Expr, skip int, params []Type, keep func(int) bool, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	return a.returnBorrowCallOtherArgsFlowWhereKinds(args, skip, params, keep, nil, aliases, active, localBindings)
}

func (a *Analyzer) returnBorrowCallOtherArgsFlowWhereKinds(args []ast.Expr, skip int, params []Type, keep func(int) bool, contentsOnly func(int) bool, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	flow := returnBorrowFlow{}
	for other, arg := range args {
		if other == skip || (keep != nil && !keep(other)) {
			continue
		}
		if params != nil && other < len(params) {
			if ref, isRef := params[other].(*RefType); isRef {
				if outlivesFrameStorage(ref.Storage) {
					// A `heap`/`static` parameter only accepts frame-outliving storage.
					continue
				}
				if contentsOnly != nil && contentsOnly(other) {
					flow = mergeReturnBorrowFlow(flow, a.returnBorrowReferentContentsFlow(arg, aliases, active, localBindings))
					continue
				}
				var writable Type
				if skip >= 0 && skip < len(params) {
					writable = params[skip]
				}
				if writable == nil || a.returnBorrowMayTarget(writable, ref.Elem) {
					flow = mergeReturnBorrowFlow(flow, a.returnBorrowAddressFlow(arg, aliases, active, localBindings))
				} else {
					// The writable parameter cannot hold a borrow INTO the referent: only what
					// the referent itself holds can reach it.
					flow = mergeReturnBorrowFlow(flow, a.returnBorrowReferentContentsFlow(arg, aliases, active, localBindings))
				}
				continue
			}
		}
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(arg, aliases, active, localBindings))
	}
	return flow
}

// returnBorrowReferentContentsFlow is what the referent of a reference argument holds. For `&x`
// that is x's own flow. A reference parameter's alias names its ADDRESS as Params[i]; what its
// pointee holds is Contents[i] (plus anything the body stored through it since).
func (a *Analyzer) returnBorrowReferentContentsFlow(arg ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	operand := returnBorrowStripAddr(arg)
	flow := a.returnBorrowFlowForExpr(operand, aliases, active, localBindings)
	root := returnBorrowPlaceRootName(operand)
	if root == "" || a.currentScope == nil || len(flow.Params) == 0 {
		return flow
	}
	sym, ok := a.currentScope.Lookup(root)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return flow
	}
	if _, isRef := sym.Type.(*RefType); !isRef || !flow.Params[sym.ParamIndex] {
		return flow
	}
	converted := returnBorrowFlow{Local: flow.Local, Params: map[int]bool{}, Contents: map[int]bool{sym.ParamIndex: true}}
	for index := range flow.Params {
		if index != sym.ParamIndex {
			converted.Params[index] = true
		}
	}
	for index := range flow.Contents {
		converted.Contents[index] = true
	}
	return converted
}

// returnBorrowReadOnlyMethods are builtin methods that never store an argument into the receiver.
var returnBorrowReadOnlyMethods = map[string]bool{
	"contains": true, "index_of": true, "find": true, "get": true, "count": true, "len": true,
	"is_empty": true, "first": true, "last": true, "starts_with": true, "ends_with": true,
	"eq": true, "equals": true, "compare": true, "cmp": true, "hash": true, "has": true,
	"has_key": true, "contains_key": true, "at": true, "peek": true, "slice": true,
	"clone": true, "copy": true, "to_string": true, "keys": true, "values": true, "iter": true,
}

// returnBorrowWritableParam reports a parameter through which a callee can store a borrow into the
// caller's argument.
func (a *Analyzer) returnBorrowWritableParam(paramType Type) bool {
	switch pt := paramType.(type) {
	case *RefType:
		return pt != nil && pt.Mutable && (pt.Elem == nil || containsTypeParam(pt.Elem) || a.typeCarriesBorrowedStorage(pt.Elem, map[Type]bool{}))
	case *ViewType:
		return pt != nil && pt.Mutable && (pt.Elem == nil || containsTypeParam(pt.Elem) || a.typeCarriesBorrowedStorage(pt.Elem, map[Type]bool{}))
	}
	return false
}

// returnBorrowCalleeSignature resolves the signature of a call to a declared function, an extern,
// a function-valued binding or a function-typed field. Builtin methods have none.
func (a *Analyzer) returnBorrowCalleeSignature(call *ast.CallExpr) *FuncType {
	if decl, ok := a.resolveDirectCallFuncDecl(call); ok && decl != nil {
		if sym := a.funcDeclSymbols[decl]; sym != nil {
			if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
				return fnType
			}
		}
	}
	switch callee := call.Func.(type) {
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(callee.Name); ok && sym != nil {
				if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
					return fnType
				}
			}
		}
		if sym, _, visible := a.lookupVisibleGlobal(callee.Name); visible && sym != nil {
			if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
				return fnType
			}
		}
	case *ast.FieldExpr:
		if a.returnBorrowBuiltinMethodCall(call) {
			return nil
		}
	}
	if fnType, ok := a.exprType(call.Func).(*FuncType); ok && fnType != nil {
		return fnType
	}
	return nil
}

// returnBorrowBuiltinMethodCall reports `recv.method(...)` that is not a call of a function-typed
// struct field (UFCS and extension calls were rewritten to plain calls during analysis).
func (a *Analyzer) returnBorrowBuiltinMethodCall(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || a.returnBorrowTypeNameObject(field.Object) {
		// `E.Ident(x)` constructs a variant: only its arguments flow into the result.
		return false
	}
	objType := a.exprType(field.Object)
	if ref, isRef := objType.(*RefType); isRef && ref != nil {
		objType = ref.Elem
	}
	if st, isStruct := objType.(*StructType); isStruct && st != nil {
		for _, f := range st.Fields {
			if f.Name == field.Field {
				return false
			}
		}
	}
	return true
}

// returnBorrowAddressFlow is the flow of the ADDRESS of a place: what a reference parameter or a
// reference loop binding receives.
func (a *Analyzer) returnBorrowAddressFlow(arg ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	inner := returnBorrowStripParens(arg)
	if _, isAddr := inner.(*ast.AddrOfExpr); isAddr {
		return a.returnBorrowFlowForExpr(inner, aliases, active, localBindings)
	}
	root := inner
	for {
		switch n := root.(type) {
		case *ast.ParenExpr:
			root = n.Inner
			continue
		case *ast.MoveExpr:
			root = n.Operand
			continue
		case *ast.FieldExpr:
			root = n.Object
			continue
		case *ast.IndexExpr:
			root = n.Object
			continue
		case *ast.SliceExpr:
			root = n.Object
			continue
		}
		break
	}
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil {
		return a.returnBorrowFlowForExpr(arg, aliases, active, localBindings)
	}
	if a.currentScope == nil {
		return returnBorrowFlow{Local: true}
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return returnBorrowFlow{Local: true}
	}
	switch sym.Kind {
	case SymbolParam:
		switch sym.Type.(type) {
		case *RefType, *DArrayType, *ViewType, *SViewType:
			return returnBorrowFlow{Params: map[int]bool{sym.ParamIndex: true}}
		}
		return returnBorrowFlow{Local: true}
	case SymbolLocal:
		switch sym.Type.(type) {
		case *RefType, *ViewType, *SViewType:
			return a.returnBorrowFlowForExpr(ident, aliases, active, localBindings)
		}
		return returnBorrowFlow{Local: true}
	}
	return returnBorrowFlow{}
}

// returnBorrowFlowForSlice: a slice of an owned container borrows that container's storage and
// carries what its elements hold; a slice of a view or reference views what it views.
func (a *Analyzer) returnBorrowFlowForSlice(n *ast.SliceExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	flow := a.returnBorrowFlowForExpr(n.Object, aliases, active, localBindings)
	objType := a.exprType(n.Object)
	if objType != nil && isBorrowLikeType(objType) {
		return flow
	}
	return mergeReturnBorrowFlow(flow, a.returnBorrowAddressFlow(n.Object, aliases, active, localBindings))
}

// returnBorrowFlowForIterSource is a loop binding's flow read from its iterated source.
func (a *Analyzer) returnBorrowFlowForIterSource(sym *Symbol, source ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	if a.iterBindingByRef[sym] {
		return a.returnBorrowAddressFlow(source, aliases, active, localBindings)
	}
	if a.returnBorrowEnvActive() {
		if _, _, trusted := a.returnBorrowEnvTrustedRoot(source); trusted {
			return a.returnBorrowFlowForExpr(returnBorrowStripSlice(source), aliases, active, localBindings)
		}
	}
	return a.returnBorrowIterValueFlow(source, aliases, active, localBindings)
}

// returnBorrowIterValueFlow is what a VALUE loop binding (or comprehension binder) holds: a copy of
// a source element, so what the source's elements hold -- never the source's address. A source
// (or a slice of one) whose stores the walk tracks reads its tracked flow, even when that is
// empty; any other source falls back to the argument flow, which may be its address.
func (a *Analyzer) returnBorrowIterValueFlow(source ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	stripped := returnBorrowStripSlice(source)
	if a.returnBorrowRecordingActive() || a.returnBorrowTrackedLocalArg(stripped, aliases) {
		return a.returnBorrowFlowForExpr(stripped, aliases, active, localBindings)
	}
	if a.returnBorrowRefParamPlace(stripped) {
		// An element copied out of a reference parameter's pointee holds what the pointee holds.
		return a.returnBorrowReferentContentsFlow(stripped, aliases, active, localBindings)
	}
	return a.returnBorrowFlowForArgument(source, aliases, active, localBindings)
}

// returnBorrowOwnedValueType reports a type whose value is a copy of what it was read from: no
// reference, view or opaque handle anywhere at its top level that could be an address into the
// place read. Elements it OWNS (a struct's sview field) still carry their own borrows as flow.
func returnBorrowOwnedValueType(t Type, seen map[Type]bool) bool {
	switch tt := t.(type) {
	case *BuiltinType, *BitIntType, *ConstEnumType, *BitGroupType, *StructType, *EnumType, *DArrayType, *ArrayType, *DictType, *SetType:
		return true
	case *OptionalType:
		if tt == nil || seen[t] {
			return tt != nil
		}
		seen[t] = true
		return returnBorrowOwnedValueType(tt.Value, seen)
	case *TupleType:
		if tt == nil {
			return false
		}
		for _, field := range tt.Fields {
			if !returnBorrowOwnedValueType(field.Type, seen) {
				return false
			}
		}
		return true
	}
	return false
}

// returnBorrowRefParamPlace reports a place (`p`, `p.items`, `p.xs[i].body`) rooted at a
// reference parameter.
func (a *Analyzer) returnBorrowRefParamPlace(expr ast.Expr) bool {
	root := returnBorrowPlaceRootName(expr)
	if root == "" || a.currentScope == nil {
		return false
	}
	sym, ok := a.currentScope.Lookup(root)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return false
	}
	_, isRef := sym.Type.(*RefType)
	return isRef
}

func returnBorrowStripSlice(expr ast.Expr) ast.Expr {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
			continue
		case *ast.SliceExpr:
			expr = n.Object
			continue
		}
		return expr
	}
}

// returnBorrowIterPatternNames lists the names an iterator-loop pattern binds.
func returnBorrowIterPatternNames(pattern ast.MoveBindPattern) []string {
	var names []string
	add := func(name string) {
		if name != "" && name != "_" {
			names = append(names, name)
		}
	}
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		add(p.Name)
	case *ast.MoveBindStructPattern:
		for _, arg := range p.Args {
			add(arg.Name)
		}
	case *ast.MoveBindTuplePattern:
		for _, arg := range p.Args {
			add(arg.Name)
		}
	}
	return names
}
