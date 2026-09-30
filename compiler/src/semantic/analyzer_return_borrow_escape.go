package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
)

// returnBorrowFlow is a may-flow summary for references contained in a returned
// value. Params names the formal parameters whose storage may be retained by the
// result; Local means the value may retain the current function's stack storage.
// It is intentionally separate from borrowedOwnerRefState: that state tracks
// affine-owner aliases, while this one protects ordinary references in aggregates.
type returnBorrowFlow struct {
	Params map[int]bool
	Local  bool
	// Contents names reference parameters whose POINTEE was read for a reference/view it holds
	// (`return a.owner` with `a: A&`, `owner: sview`). The result aliases what the caller stored
	// in that pointee, not the pointee itself, so a call site maps it to the argument's contents:
	// `name_of(local)` passes `&local` but the result points wherever local.owner points.
	Contents map[int]bool
}

func (f returnBorrowFlow) empty() bool {
	return !f.Local && len(f.Params) == 0 && len(f.Contents) == 0
}

func mergeReturnBorrowFlow(left, right returnBorrowFlow) returnBorrowFlow {
	merged := returnBorrowFlow{Local: left.Local || right.Local}
	merged.Params = mergeReturnBorrowIndexSets(left.Params, right.Params)
	merged.Contents = mergeReturnBorrowIndexSets(left.Contents, right.Contents)
	return merged
}

func mergeReturnBorrowIndexSets(left, right map[int]bool) map[int]bool {
	if len(left)+len(right) == 0 {
		return nil
	}
	merged := make(map[int]bool, len(left)+len(right))
	for index := range left {
		merged[index] = true
	}
	for index := range right {
		merged[index] = true
	}
	return merged
}

func (a *Analyzer) typeCarriesBorrowedStorage(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *RefType, *ViewType, *SViewType, *CStrType:
		return true
	case *ArrayType:
		return a.typeCarriesBorrowedStorage(tt.Elem, seen)
	case *DArrayType:
		return a.typeCarriesBorrowedStorage(tt.Elem, seen)
	case *OptionalType:
		return a.typeCarriesBorrowedStorage(tt.Value, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if a.typeCarriesBorrowedStorage(field.Type, seen) {
				return true
			}
		}
	case *DictType:
		return a.typeCarriesBorrowedStorage(tt.Key, seen) || a.typeCarriesBorrowedStorage(tt.Value, seen)
	case *SetType:
		return a.typeCarriesBorrowedStorage(tt.Elem, seen)
	case *StructType:
		for _, field := range tt.Fields {
			if a.typeCarriesBorrowedStorage(field.Type, seen) {
				return true
			}
		}
	case *EnumType:
		for _, field := range tt.Common {
			if a.typeCarriesBorrowedStorage(field.Type, seen) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if a.typeCarriesBorrowedStorage(payload, seen) {
					return true
				}
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
				fieldType := a.substituteType(field.Type, bindings, nil, nil, nil)
				if a.typeCarriesBorrowedStorage(fieldType, seen) {
					return true
				}
			}
		}
	}
	return false
}

// typeMayHoldFrameBorrow is typeCarriesBorrowedStorage restricted to borrows of a function
// frame: a `heap`/`static` reference and a `tail` field's own storage cannot hold one.
func (a *Analyzer) typeMayHoldFrameBorrow(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *RefType:
		// A `heap`/`static` reference cannot point into a frame: storing `&local` or a forwarded
		// stack reference into one is rejected.
		return !outlivesFrameStorage(tt.Storage)
	case *ViewType, *SViewType, *CStrType:
		return true
	case *ArrayType:
		return a.typeMayHoldFrameBorrow(tt.Elem, seen)
	case *DArrayType:
		return a.typeMayHoldFrameBorrow(tt.Elem, seen)
	case *OptionalType:
		return a.typeMayHoldFrameBorrow(tt.Value, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if a.typeMayHoldFrameBorrow(field.Type, seen) {
				return true
			}
		}
	case *DictType:
		return a.typeMayHoldFrameBorrow(tt.Key, seen) || a.typeMayHoldFrameBorrow(tt.Value, seen)
	case *SetType:
		return a.typeMayHoldFrameBorrow(tt.Elem, seen)
	case *StructType:
		for _, field := range tt.Fields {
			fieldType := field.Type
			// A `tail` field is the struct's own trailing storage, typed as a reference to it.
			if ref, isRef := fieldType.(*RefType); isRef && ref != nil && field.IsTail {
				fieldType = ref.Elem
			}
			if a.typeMayHoldFrameBorrow(fieldType, seen) {
				return true
			}
		}
	case *EnumType:
		for _, field := range tt.Common {
			if a.typeMayHoldFrameBorrow(field.Type, seen) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if a.typeMayHoldFrameBorrow(payload, seen) {
					return true
				}
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
				fieldType := a.substituteType(field.Type, bindings, nil, nil, nil)
				if a.typeMayHoldFrameBorrow(fieldType, seen) {
					return true
				}
			}
		}
	}
	return false
}

func (a *Analyzer) returnBorrowFlowForExpr(expr ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	flow := a.returnBorrowFlowForExprInner(expr, aliases, active, localBindings)
	if !flow.empty() && expr != nil {
		// A value whose type holds no reference, view, closure or generic part cannot carry a
		// borrow of anything: `ValueType{kind, bits}` computed from reference arguments is a copy.
		if t, ok := a.exprTypes[expr]; ok && a.typeIsBorrowFree(t, map[Type]bool{}) {
			return returnBorrowFlow{}
		}
	}
	return flow
}

// typeIsBorrowFree is a positive whitelist: scalars, extern handles and by-value aggregates or
// containers built only from them. Anything else (references, views, strings, closures, type
// parameters, generics, unresolved types) may carry a borrow.
func (a *Analyzer) typeIsBorrowFree(t Type, seen map[Type]bool) bool {
	if t == nil {
		return false
	}
	if seen[t] {
		return true
	}
	seen[t] = true
	switch tt := t.(type) {
	case *BuiltinType:
		return tt.Name != "void" && tt.Name != "any"
	case *BitIntType, *ConstEnumType, *BitGroupType, *OpaqueType:
		return true
	case *ArrayType:
		return a.typeIsBorrowFree(tt.Elem, seen)
	case *DArrayType:
		return a.typeIsBorrowFree(tt.Elem, seen)
	case *OptionalType:
		return a.typeIsBorrowFree(tt.Value, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if !a.typeIsBorrowFree(field.Type, seen) {
				return false
			}
		}
		return true
	case *StructType:
		if len(tt.TypeParams) > 0 {
			return false
		}
		for _, field := range tt.Fields {
			if field.IsTail || !a.typeIsBorrowFree(field.Type, seen) {
				return false
			}
		}
		return true
	case *EnumType:
		for _, field := range tt.Common {
			if !a.typeIsBorrowFree(field.Type, seen) {
				return false
			}
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if !a.typeIsBorrowFree(payload, seen) {
					return false
				}
			}
		}
		return true
	}
	return false
}

func (a *Analyzer) returnBorrowFlowForExprInner(expr ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	if expr == nil {
		return returnBorrowFlow{}
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.returnBorrowFlowForExpr(n.Inner, aliases, active, localBindings)
	case *ast.MoveExpr:
		return a.returnBorrowFlowForExpr(n.Operand, aliases, active, localBindings)
	case *ast.CastExpr:
		return a.returnBorrowFlowForExpr(n.Operand, aliases, active, localBindings)
	case *ast.SpecializeExpr:
		return a.returnBorrowFlowForExpr(n.Operand, aliases, active, localBindings)
	case *ast.OptionalBindExpr:
		return a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
	case *ast.Ident:
		if a.returnBorrowRecordingActive() {
			return a.returnBorrowRecordingIdentFlow(n, aliases)
		}
		if flow, ok := aliases[n.Name]; ok {
			return flow
		}
		if a.currentScope == nil {
			return returnBorrowFlow{}
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil || sym.Kind != SymbolLocal || localBindings[sym] {
			return returnBorrowFlow{}
		}
		if a.returnBorrowEnvActive() {
			if flow, ok := a.returnBorrowEnvSymbolFlow(sym); ok {
				return flow
			}
			if source, ok := a.iterBindingSources[sym]; ok && source != nil {
				localBindings[sym] = true
				flow := a.returnBorrowFlowForIterSource(sym, source, aliases, active, localBindings)
				delete(localBindings, sym)
				return flow
			}
			// A mutable local whose stores the environment could not trust may hold anything
			// stored into it since its declaration, not just its initial value.
			if decl, isDecl := sym.Node.(*ast.VarDeclStmt); isDecl && decl != nil && (sym.Mutable || decl.Mutable) {
				if _, isRef := sym.Type.(*RefType); !isRef && (sym.Type == nil || a.typeCarriesBorrowedStorage(sym.Type, map[Type]bool{})) {
					return returnBorrowFlow{Local: true}
				}
			}
		}
		if value, ok := a.currentValueBindings[sym]; ok && value != nil {
			localBindings[sym] = true
			flow := a.returnBorrowFlowForExpr(value, aliases, active, localBindings)
			delete(localBindings, sym)
			return flow
		}
		if source, ok := a.iterBindingSources[sym]; ok && source != nil {
			localBindings[sym] = true
			flow := a.returnBorrowFlowForIterSource(sym, source, aliases, active, localBindings)
			delete(localBindings, sym)
			return flow
		}
		// A rebindable reference local (`r: mutable T& = &y`) keeps no value binding: it holds its
		// initializer's address until a rebind (`r <- &x`) may point it anywhere, a local included.
		if decl, isDecl := sym.Node.(*ast.VarDeclStmt); isDecl && decl != nil && decl.Value != nil {
			if _, isRef := sym.Type.(*RefType); isRef {
				if a.returnBorrowLocalRebound(sym.Name) {
					return returnBorrowFlow{Local: true}
				}
				localBindings[sym] = true
				flow := a.returnBorrowFlowForExpr(decl.Value, aliases, active, localBindings)
				delete(localBindings, sym)
				return flow
			}
		}
		return returnBorrowFlow{}
	case *ast.SliceExpr:
		if a.returnBorrowRecordingActive() || a.returnBorrowEnvActive() {
			return a.returnBorrowFlowForSlice(n, aliases, active, localBindings)
		}
		// A slice of an inline fixed array (`buf[0:2]` with `buf: u8[4]`) views the FRAME itself: the
		// bytes live in the function's stack slot, not in an arena that could be adopted from the
		// caller, so the summary must carry the local borrow even though it does not trace slices.
		if a.sliceViewsFrameArray(n) {
			return returnBorrowFlow{Local: true}
		}
	case *ast.AddrOfExpr:
		if param := a.borrowedContainerParamRoot(n.Operand); param != nil {
			return returnBorrowFlow{Params: map[int]bool{param.ParamIndex: true}}
		}
		if storage, known := a.borrowProvenanceStorage(n); known {
			return returnBorrowFlow{Local: storage == RefStorageStack}
		}
		return returnBorrowFlow{}
	case *ast.StructLitExpr:
		flow := returnBorrowFlow{}
		for _, arg := range n.LoweredArgs() {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(arg, aliases, active, localBindings))
		}
		for _, spread := range n.Spreads {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(spread, aliases, active, localBindings))
		}
		return flow
	case *ast.RecordUpdateExpr:
		flow := a.returnBorrowFlowForExpr(n.Base, aliases, active, localBindings)
		for _, arg := range n.LoweredArgs() {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(arg, aliases, active, localBindings))
		}
		return flow
	case *ast.TupleExpr:
		flow := returnBorrowFlow{}
		tupleType, _ := a.exprTypes[n].(*TupleType)
		for index, elem := range n.Elems {
			var placeType Type
			if tupleType != nil && len(tupleType.Fields) == len(n.Elems) {
				placeType = tupleType.Fields[index].Type
			}
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForCopiedValue(elem, placeType, aliases, active, localBindings))
		}
		return flow
	case *ast.ListLitExpr:
		flow := returnBorrowFlow{}
		var elemType Type
		switch listType := a.exprTypes[n].(type) {
		case *DArrayType:
			elemType = listType.Elem
		case *ArrayType:
			elemType = listType.Elem
		}
		for _, elem := range n.Elems {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForCopiedValue(elem, elemType, aliases, active, localBindings))
		}
		for _, key := range n.Keys {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(key, aliases, active, localBindings))
		}
		return flow
	case *ast.FieldExpr:
		if a.returnBorrowTypeNameObject(n.Object) {
			// `E.Invalid`: a variant (or other member) of a type name holds no borrow.
			return returnBorrowFlow{}
		}
		fieldType := a.exprTypes[n]
		if fieldType == nil || a.typeCarriesBorrowedStorage(fieldType, map[Type]bool{}) {
			if index, ok := a.returnBorrowContentReadParam(n, fieldType, aliases); ok {
				return returnBorrowPointeeReadFlow(n, index, aliases)
			}
			return a.returnBorrowFlowForExpr(n.Object, aliases, active, localBindings)
		}
	case *ast.IndexExpr:
		indexType := a.exprTypes[n]
		if indexType == nil || a.typeCarriesBorrowedStorage(indexType, map[Type]bool{}) {
			if index, ok := a.returnBorrowContentReadParam(n, indexType, aliases); ok {
				return returnBorrowPointeeReadFlow(n, index, aliases)
			}
			return a.returnBorrowFlowForExpr(n.Object, aliases, active, localBindings)
		}
	case *ast.TernaryExpr:
		alt := a.returnBorrowFlowForExpr(n.Alt, aliases, active, localBindings)
		binders := map[ast.Node]bool{}
		returnBorrowMarkConditionBinders(n.Cond, binders)
		if len(binders) == 0 {
			return mergeReturnBorrowFlow(a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings), alt)
		}
		// The condition's unwraps are in scope in the taken branch.
		savedScope := a.currentScope
		a.currentScope = NewScope(savedScope)
		valueAliases := cloneReturnBorrowAliases(aliases)
		a.defineReturnBorrowConditionBindings(n.Cond, valueAliases, active, localBindings)
		value := a.returnBorrowFlowForExpr(n.Value, valueAliases, active, localBindings)
		a.currentScope = savedScope
		return mergeReturnBorrowFlow(value, alt)
	case *ast.ExprBlock:
		savedScope := a.currentScope
		a.currentScope = NewScope(savedScope)
		blockAliases := cloneReturnBorrowAliases(aliases)
		flow := a.returnBorrowFlowForStatements(n.Stmts, blockAliases, active, localBindings, false)
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(n.Value, blockAliases, active, localBindings))
		a.currentScope = savedScope
		return flow
	case *ast.CallExpr:
		return a.returnBorrowFlowForCall(n, aliases, active, localBindings)
	case *ast.ListComprehensionExpr:
		return a.returnBorrowFlowForComprehension(n, aliases, active, localBindings)
	case *ast.MatchExpr:
		return a.returnBorrowFlowForMatchExpr(n, aliases, active, localBindings)
	case *ast.QueryExpr:
		if n.Pattern == nil {
			return a.returnBorrowFlowForQuery(n, aliases, active, localBindings)
		}
	case *ast.GetExpr:
		return a.returnBorrowFlowForUnwrap(expr, n.Value, n.Fallback, n.Recovery, aliases, active, localBindings)
	case *ast.TryExpr:
		return a.returnBorrowFlowForUnwrap(expr, n.Value, n.Fallback, n.Recovery, aliases, active, localBindings)
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
		return returnBorrowFlow{}
	}
	if a.returnBorrowRecordingActive() {
		// The recorded unions are trusted: an expression kind the walk does not trace holds a
		// frame borrow whenever its type can.
		return a.returnBorrowConservativeFlow(expr, nil)
	}
	return returnBorrowFlow{}
}

func (a *Analyzer) returnBorrowFlowForCall(call *ast.CallExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok {
		if ident, isIdent := call.Func.(*ast.Ident); isIdent && ident != nil {
			shadowed := false
			if a.currentScope != nil {
				_, shadowed = a.currentScope.Lookup(ident.Name)
			}
			if !shadowed {
				if sym, _, visible := a.lookupVisibleGlobal(ident.Name); visible && sym != nil {
					decl, ok = sym.Node.(*ast.FuncDecl)
				}
			}
		}
	}
	args := call.Args
	if call.ResolvedArgsValid {
		args = call.ResolvedArgs
	}
	if ok && decl != nil {
		summary := a.returnBorrowFlowForFunc(decl, active)
		flow := returnBorrowFlow{Local: summary.Local}
		var calleeType *FuncType
		if sym := a.funcDeclSymbols[decl]; sym != nil {
			calleeType, _ = sym.Type.(*FuncType)
		}
		for index := range summary.Params {
			if index >= len(args) {
				continue
			}
			// A by-value argument whose binding is tracked hands the callee its VALUE: the result
			// can hold only what that value holds, never the local's address. (`close(token, items)`
			// with `items: darray[T]` returning `Expr.Array(items)`.) A reference parameter still
			// borrows the local itself.
			if calleeType != nil && index < len(calleeType.Params) && calleeType.Params[index] != nil {
				if _, isRef := calleeType.Params[index].(*RefType); !isRef && a.returnBorrowTrackedLocalArg(args[index], aliases) {
					flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(args[index], aliases, active, localBindings))
					continue
				}
				// A result that cannot point at the referent (`snap(n: darray[sview]&) -> darray[sview]`
				// called on `&local`) carries only what the referent holds, never its address.
				if ref, isRef := calleeType.Params[index].(*RefType); isRef && ref != nil && ref.Elem != nil && calleeType.Return != nil && !a.returnBorrowResultMayTarget(calleeType.Return, ref.Elem) {
					flow = mergeReturnBorrowFlow(flow, a.returnBorrowReferentContentsFlow(args[index], aliases, active, localBindings))
					continue
				}
			}
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForArgument(args[index], aliases, active, localBindings))
		}
		for index := range summary.Contents {
			if index >= len(args) {
				continue
			}
			if calleeType != nil && index < len(calleeType.Params) && calleeType.Params[index] != nil {
				if _, isRef := calleeType.Params[index].(*RefType); !isRef {
					// A by-value parameter's contents are the passed value's own flow.
					flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(args[index], aliases, active, localBindings))
					continue
				}
			}
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForArgumentContents(args[index], aliases, active, localBindings))
		}
		return flow
	}
	// Indirect and external calls without a source summary are unknown. Preserve
	// every argument's local-borrow taint rather than silently treating the result
	// as fresh; this can conservatively reject a safe call but cannot bless a leak.
	flow := returnBorrowFlow{}
	payload := a.returnBorrowVariantPayloadTypes(call, args)
	for index, arg := range args {
		if payload != nil {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForCopiedValue(arg, payload[index], aliases, active, localBindings))
			continue
		}
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(arg, aliases, active, localBindings))
	}
	if a.returnBorrowRecordingActive() || a.returnBorrowEnvActive() {
		// A builtin method's result (`xs.pop()`, `m.get(k)`) can be anything its receiver holds.
		if field, isField := call.Func.(*ast.FieldExpr); isField && field != nil && a.returnBorrowBuiltinMethodCall(call) {
			if resultType := a.exprTypes[call]; resultType == nil || a.typeCarriesBorrowedStorage(resultType, map[Type]bool{}) {
				flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(field.Object, aliases, active, localBindings))
			}
		}
	}
	return flow
}

// returnBorrowVariantPayloadTypes is the payload types, one per argument, of a positional enum
// variant construction (`E.V(a, b)`); nil for any other call.
func (a *Analyzer) returnBorrowVariantPayloadTypes(call *ast.CallExpr, args []ast.Expr) []Type {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || len(call.ArgNames) != 0 || !a.returnBorrowTypeNameObject(field.Object) {
		return nil
	}
	enumType, ok := a.exprTypes[call].(*EnumType)
	if !ok || enumType == nil {
		return nil
	}
	variant := enumType.VariantMap[field.Field]
	if variant == nil || len(variant.Payload) != len(args) {
		return nil
	}
	return variant.Payload
}

// returnBorrowTrackedLocalArg reports an argument that is a plain local identifier whose flow the
// summary tracks (declared in this body, so every store into it merged into its alias).
func (a *Analyzer) returnBorrowTrackedLocalArg(arg ast.Expr, aliases map[string]returnBorrowFlow) bool {
	for {
		switch n := arg.(type) {
		case *ast.ParenExpr:
			arg = n.Inner
			continue
		case *ast.MoveExpr:
			arg = n.Operand
			continue
		}
		break
	}
	ident, ok := arg.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return false
	}
	if a.returnBorrowEnvActive() {
		if root, _, trusted := a.returnBorrowEnvTrustedRoot(ident); trusted && root == ident {
			return true
		}
	}
	if _, tracked := aliases[ident.Name]; !tracked {
		return false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	return ok && sym != nil && sym.Kind == SymbolLocal
}

func (a *Analyzer) returnBorrowFlowForArgument(arg ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	argFlow := a.returnBorrowFlowForExpr(arg, aliases, active, localBindings)
	if argFlow.empty() {
		argFlow = a.returnBorrowFlowForBorrowedArgument(arg, aliases, active, localBindings)
	}
	return argFlow
}

// returnBorrowFlowForArgumentContents maps a callee's Contents[i] to the call site: the borrows
// stored in the argument's referent. For `&x` (explicit, or the implicit borrow of a `T&`
// parameter) that is x's own flow -- what was stored in x -- rather than x's address. Any other
// argument falls back to the argument's own flow, which over-approximates its contents.
func (a *Analyzer) returnBorrowFlowForArgumentContents(arg ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	inner := arg
	for {
		paren, ok := inner.(*ast.ParenExpr)
		if !ok || paren == nil {
			break
		}
		inner = paren.Inner
	}
	operand := inner
	if addr, ok := inner.(*ast.AddrOfExpr); ok && addr != nil {
		operand = addr.Operand
	}
	if a.returnBorrowEnvActive() {
		if ident, flow, trusted := a.returnBorrowEnvTrustedRoot(operand); trusted && ident == returnBorrowStripParens(operand) {
			return flow
		}
	}
	// `&local` lends a tracked local: its referent holds exactly the local's tracked value (every
	// store through a writable argument merges into it), never the local's own address.
	if a.returnBorrowTrackedLocalArg(operand, aliases) {
		return a.returnBorrowFlowForExpr(operand, aliases, active, localBindings)
	}
	// A reference parameter passed on (`f(names)` with `names: T&`) lends its referent, whose
	// contents are the caller's Contents[i], not the parameter's address.
	if flow := a.returnBorrowReferentContentsFlow(operand, aliases, active, localBindings); !flow.empty() {
		return flow
	}
	return a.returnBorrowFlowForArgument(operand, aliases, active, localBindings)
}

// returnBorrowContentReadParam reports a read, inside a return-borrow summary, of a reference or
// view VALUE held in a reference parameter's pointee (`a.owner`, `a.inner.r`, `items[i].name`
// with `a: A&`, `items: darray[A]&`). Only a read whose own type is reference-like qualifies: an
// aggregate element (`xs[0]` returned as `A&`) may be an address into the pointee itself. The
// parameter's alias must still be its entry flow, so a rebound `a <- &local` is not mistaken for
// caller contents.
func (a *Analyzer) returnBorrowContentReadParam(read ast.Expr, readType Type, aliases map[string]returnBorrowFlow) (int, bool) {
	if readType == nil || !isBorrowLikeType(readType) {
		return 0, false
	}
	return a.returnBorrowPointeeReadParam(read, aliases)
}

// returnBorrowPointeeReadParam reports the reference parameter whose pointee read reads from.
func (a *Analyzer) returnBorrowPointeeReadParam(read ast.Expr, aliases map[string]returnBorrowFlow) (int, bool) {
	if !a.returnBorrowContentReads || aliases == nil || a.currentScope == nil {
		return 0, false
	}
	root := read
	for {
		switch n := root.(type) {
		case *ast.ParenExpr:
			root = n.Inner
			continue
		case *ast.FieldExpr:
			root = n.Object
			continue
		case *ast.IndexExpr:
			root = n.Object
			continue
		}
		break
	}
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil || ident == read {
		return 0, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return 0, false
	}
	if _, isRef := sym.Type.(*RefType); !isRef {
		return 0, false
	}
	flow, ok := aliases[ident.Name]
	// Stored CONTENTS (`p <- p.step()` writing a copy of the pointee back through `p`) keep the
	// alias an address of the caller's referent; a stored local or other address does not.
	if !ok || flow.Local || len(flow.Params) != 1 || !flow.Params[sym.ParamIndex] {
		return 0, false
	}
	return sym.ParamIndex, true
}

// returnBorrowPointeeReadFlow is the flow of a read from reference parameter index's pointee:
// the caller's contents plus whatever the body stored through the parameter since.
func returnBorrowPointeeReadFlow(read ast.Expr, index int, aliases map[string]returnBorrowFlow) returnBorrowFlow {
	flow := returnBorrowFlow{Contents: map[int]bool{index: true}}
	if root := returnBorrowPlaceRootName(read); root != "" {
		for stored := range aliases[root].Contents {
			flow.Contents[stored] = true
		}
	}
	return flow
}

// returnBorrowFlowForCopiedValue is the flow of an operand copied by value into a place whose
// type is not a reference (a returned value, an enum payload, a tuple or list element). A copy of
// a field or element of a reference parameter's pointee holds what the pointee holds -- the
// caller's contents -- never the pointee's address, whatever the copied type is.
func (a *Analyzer) returnBorrowFlowForCopiedValue(expr ast.Expr, placeType Type, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	if placeType != nil && !isBorrowLikeType(placeType) {
		inner := returnBorrowStripParens(expr)
		switch inner.(type) {
		case *ast.FieldExpr, *ast.IndexExpr:
			if readType := a.exprTypes[inner]; readType != nil && !isBorrowLikeType(readType) {
				if index, ok := a.returnBorrowPointeeReadParam(inner, aliases); ok {
					return returnBorrowPointeeReadFlow(inner, index, aliases)
				}
			}
		}
	}
	return a.returnBorrowFlowForExpr(expr, aliases, active, localBindings)
}

func (a *Analyzer) returnBorrowFlowForBorrowedArgument(arg ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	root := arg
	for {
		switch n := root.(type) {
		case *ast.ParenExpr:
			root = n.Inner
		case *ast.CastExpr:
			root = n.Operand
		case *ast.MoveExpr:
			root = n.Operand
		case *ast.FieldExpr:
			root = n.Object
		case *ast.IndexExpr:
			root = n.Object
		default:
			goto rooted
		}
	}
rooted:
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return returnBorrowFlow{}
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return returnBorrowFlow{}
	}
	switch sym.Type.(type) {
	case *DArrayType:
		if sym.Kind == SymbolParam {
			return returnBorrowFlow{Params: map[int]bool{sym.ParamIndex: true}}
		}
		if sym.Kind == SymbolLocal {
			return returnBorrowFlow{Local: true}
		}
	}
	if sym.Kind == SymbolParam && a.typeCarriesBorrowedStorage(sym.Type, map[Type]bool{}) {
		return returnBorrowFlow{Params: map[int]bool{sym.ParamIndex: true}}
	}
	if sym.Kind == SymbolLocal {
		if flow, exists := aliases[ident.Name]; exists {
			return flow
		}
		if _, iterBinding := a.iterBindingSources[sym]; iterBinding && !localBindings[sym] {
			localBindings[sym] = true
			flow := a.returnBorrowFlowForArgument(a.iterBindingSources[sym], aliases, active, localBindings)
			delete(localBindings, sym)
			return flow
		}
		if _, knownBinding := a.currentValueBindings[sym]; !knownBinding && a.typeCarriesBorrowedStorage(sym.Type, map[Type]bool{}) {
			return returnBorrowFlow{Local: true}
		}
	}
	return returnBorrowFlow{}
}

func (a *Analyzer) borrowedContainerParamRoot(expr ast.Expr) *Symbol {
	root := expr
	for {
		switch n := root.(type) {
		case *ast.ParenExpr:
			root = n.Inner
		case *ast.CastExpr:
			root = n.Operand
		case *ast.FieldExpr:
			root = n.Object
		case *ast.IndexExpr:
			root = n.Object
		default:
			goto rooted
		}
	}
rooted:
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return nil
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return nil
	}
	switch sym.Type.(type) {
	case *DArrayType:
	case *RefType, *ViewType:
		// A place reached through a reference or view parameter (`&a[0]` with `a: u8[4]&`) lives
		// in the caller's referent: its address flows from that parameter.
	default:
		return nil
	}
	return sym
}

func (a *Analyzer) beginReturnBorrowSummary(fn *ast.FuncDecl) {
	if fn == nil {
		return
	}
	delete(a.returnBorrowFuncAnalyzed, fn)
	delete(a.returnBorrowFuncSummaries, fn)
	a.pushReturnBorrowDeferFrame(fn)
	if a.currentScope != nil && a.currentScope != a.globalScope {
		a.currentScope.defineHook = a.noteReturnBorrowBinder
	}
}

func (a *Analyzer) finishReturnBorrowSummary(fn *ast.FuncDecl) {
	if fn == nil {
		return
	}
	if a.returnBorrowFuncAnalyzed == nil {
		a.returnBorrowFuncAnalyzed = map[*ast.FuncDecl]bool{}
	}
	a.returnBorrowFuncAnalyzed[fn] = true
	a.popReturnBorrowDeferFrame(fn)
}

// returnBorrowQueryEntry is a summary memoized within one top-level query. A summary cut short by
// recursion depends on which callers were active: it is reused only while every function it was
// cut on is still active (the recomputation would cut at the same places). A summary cut by the
// depth limit is never reused -- a shallower call would have computed it exactly.
type returnBorrowQueryEntry struct {
	flow     returnBorrowFlow
	cutOn    []*ast.FuncDecl
	depthCut bool
}

// returnBorrowFlowForComprehension is the flow of `[v for x in src]`: the elements hold what the
// value (and a dict key) holds, with the binder holding what the source's elements hold, as a
// value `for` binding does. A range binder is an integer and holds nothing.
func (a *Analyzer) returnBorrowFlowForComprehension(n *ast.ListComprehensionExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	var binderFlow returnBorrowFlow
	switch {
	case n.RangeEnd != nil:
	default:
		binderFlow = a.returnBorrowIterValueFlow(n.Source, aliases, active, localBindings)
	}
	for _, outer := range []ast.Expr{n.Source, n.RangeEnd, n.RangeStep, n.Owner} {
		a.noteReturnBorrowCallArgumentStores(outer, aliases, active, localBindings)
	}
	env := cloneReturnBorrowAliases(aliases)
	outerScope := a.currentScope
	a.currentScope = NewScope(outerScope)
	a.defineReturnBorrowBinding(env, n.Name, n, nil, binderFlow)
	a.defineReturnBorrowBinding(env, n.SecondName, n, nil, binderFlow)
	for _, inner := range []ast.Expr{n.Filter, n.Value, n.Key} {
		a.noteReturnBorrowCallArgumentStores(inner, env, active, localBindings)
	}
	flow := a.returnBorrowFlowForExpr(n.Value, env, active, localBindings)
	flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(n.Key, env, active, localBindings))
	a.currentScope = outerScope
	return flow
}

// returnBorrowFlowForQuery is the flow of a single-binder query `first/each x in src [where p]
// [-> proj]`: the binder holds what the source's elements hold, as a value `for` binding does, and
// the result holds the projection (or the binder). The other kinds yield scalars. The filter's and
// projection's stores are noted with the binder in scope.
func (a *Analyzer) returnBorrowFlowForQuery(n *ast.QueryExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	a.noteReturnBorrowCallArgumentStores(n.Source, aliases, active, localBindings)
	a.noteReturnBorrowCallArgumentStores(n.Owner, aliases, active, localBindings)
	env := cloneReturnBorrowAliases(aliases)
	outerScope := a.currentScope
	a.currentScope = NewScope(outerScope)
	defer func() { a.currentScope = outerScope }()
	a.defineReturnBorrowBinding(env, n.Name, n, nil, a.returnBorrowIterValueFlow(n.Source, aliases, active, localBindings))
	a.noteReturnBorrowCallArgumentStores(n.Filter, env, active, localBindings)
	a.noteReturnBorrowCallArgumentStores(n.Projection, env, active, localBindings)
	switch n.Kind {
	case ast.QueryExprFirst, ast.QueryExprEach:
		if n.Projection != nil {
			return a.returnBorrowFlowForExpr(n.Projection, env, active, localBindings)
		}
		return a.returnBorrowFlowForExpr(&ast.Ident{Position: n.Pos(), Name: n.Name}, env, active, localBindings)
	}
	return returnBorrowFlow{}
}

// noteReturnBorrowConditionStores notes the stores of a condition, with each and-chain operand's
// unwraps in scope for the operands after it.
func (a *Analyzer) noteReturnBorrowConditionStores(cond ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	binders := map[ast.Node]bool{}
	returnBorrowMarkConditionBinders(cond, binders)
	if len(binders) == 0 {
		a.noteReturnBorrowCallArgumentStores(cond, aliases, active, localBindings)
		return
	}
	env := cloneReturnBorrowAliases(aliases)
	outerScope := a.currentScope
	a.currentScope = NewScope(outerScope)
	a.noteReturnBorrowAndChainStores(cond, env, active, localBindings)
	a.currentScope = outerScope
	mergeReturnBorrowAliasMaps(aliases, env)
}

func (a *Analyzer) noteReturnBorrowAndChainStores(cond ast.Expr, env map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		a.noteReturnBorrowAndChainStores(n.Inner, env, active, localBindings)
		return
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			a.noteReturnBorrowAndChainStores(n.Left, env, active, localBindings)
			a.defineReturnBorrowConditionBindings(n.Left, env, active, localBindings)
			a.noteReturnBorrowAndChainStores(n.Right, env, active, localBindings)
			return
		}
	}
	a.noteReturnBorrowCallArgumentStores(cond, env, active, localBindings)
}

// returnBorrowFlowForMatchExpr is the flow of a match expression: each arm's binders hold what the
// subject holds (a view binder also its storage), as in a match statement, and the value is the join
// of the arms' final expressions. The arms' stores are noted as they are walked.
func (a *Analyzer) returnBorrowFlowForMatchExpr(n *ast.MatchExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
	a.noteReturnBorrowCallArgumentStores(n.Store, aliases, active, localBindings)
	subjectFlow := a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
	var flow returnBorrowFlow
	armEnvs := make([]map[string]returnBorrowFlow, 0, len(n.Arms))
	for _, arm := range n.Arms {
		armEnv := cloneReturnBorrowAliases(aliases)
		outerScope := a.currentScope
		a.currentScope = NewScope(outerScope)
		for _, binder := range returnBorrowMatchPatternBinders(arm.Pattern, nil) {
			binderFlow := subjectFlow
			if binder.view {
				binderFlow = mergeReturnBorrowFlow(binderFlow, a.returnBorrowAddressFlow(n.Value, aliases, active, localBindings))
			}
			a.defineReturnBorrowBinding(armEnv, binder.name, binder.node, nil, binderFlow)
		}
		a.noteReturnBorrowCallArgumentStores(arm.Guard, armEnv, active, localBindings)
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForStatements(arm.Body, armEnv, active, localBindings, true))
		a.currentScope = outerScope
		armEnvs = append(armEnvs, armEnv)
	}
	// Each arm starts from the environment before the match; their writes join afterwards.
	for _, env := range armEnvs {
		mergeReturnBorrowAliasMaps(aliases, env)
	}
	return flow
}

// returnBorrowFlowForUnwrap is the flow of `get v` / `try v`: the unwrapped payload holds what v
// holds (absence or an error propagates out instead), and an `else` supplies its fallback value.
// A recovery block (or a value reading the error binding) is not walked, so its value is
// conservative.
func (a *Analyzer) returnBorrowFlowForUnwrap(expr ast.Expr, value, fallback ast.Expr, recovery *ast.RecoveryClause, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	flow := a.returnBorrowFlowForExpr(value, aliases, active, localBindings)
	flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(fallback, aliases, active, localBindings))
	if recovery == nil {
		return flow
	}
	switch {
	case recovery.Kind == ast.RecoveryBlock || len(recovery.Body) > 0 || (recovery.Kind == ast.RecoveryValue && recovery.Binding != ""):
		return mergeReturnBorrowFlow(flow, a.returnBorrowConservativeFlow(expr, nil))
	case recovery.Kind == ast.RecoveryValue:
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(recovery.Value, aliases, active, localBindings))
	}
	// `else return` / `else raise` leave the function instead of producing a value.
	return flow
}

// returnBorrowFlowForFunc summarizes which parameters (and whether frame-local storage) can flow
// into fn's result. Every callee summary is memoized -- without it the walk re-enters each callee
// once per call path, which is exponential in call depth. A cut-free summary is cached across
// queries once fn's body was analyzed; otherwise it is memoized for the current top-level query
// under the dependency rule on returnBorrowQueryEntry.
func (a *Analyzer) returnBorrowFlowForFunc(fn *ast.FuncDecl, active map[*ast.FuncDecl]bool) returnBorrowFlow {
	if fn == nil {
		return returnBorrowFlow{}
	}
	if summary, ok := a.returnBorrowFuncSummaries[fn]; ok {
		return summary
	}
	if len(active) == 0 {
		savedMemo, savedLog := a.returnBorrowQueryMemo, a.returnBorrowCutLog
		a.returnBorrowQueryMemo = a.returnBorrowRootMemo(fn)
		a.returnBorrowCutLog = nil
		defer func() { a.returnBorrowQueryMemo, a.returnBorrowCutLog = savedMemo, savedLog }()
	} else if entry, ok := a.returnBorrowQueryMemo[fn]; ok && a.returnBorrowQueryEntryValid(entry, active) {
		a.returnBorrowCutLog = append(a.returnBorrowCutLog, entry.cutOn...)
		return entry.flow
	}
	if active[fn] {
		a.returnBorrowCutLog = append(a.returnBorrowCutLog, fn)
		if assumed, ok := a.returnBorrowCycleAssume[fn]; ok {
			return assumed
		}
		return a.allBorrowedParamFlow(fn)
	}
	if len(active) >= semanticTraversalDepthLimit {
		// nil marks a depth-limit cut.
		a.returnBorrowCutLog = append(a.returnBorrowCutLog, nil)
		return a.allBorrowedParamFlow(fn)
	}
	logStart := len(a.returnBorrowCutLog)
	summary := a.computeReturnBorrowFlowForFunc(fn, active)
	entry := returnBorrowQueryEntry{flow: summary}
	seen := map[*ast.FuncDecl]bool{fn: true} // a cut on fn itself is resolved inside this summary
	for _, cut := range a.returnBorrowCutLog[logStart:] {
		switch {
		case cut == nil:
			entry.depthCut = true
		case !seen[cut]:
			seen[cut] = true
			entry.cutOn = append(entry.cutOn, cut)
		}
	}
	// Callers inherit the cuts this summary still depends on; fn's own cut is resolved here.
	a.returnBorrowCutLog = append(a.returnBorrowCutLog[:logStart], entry.cutOn...)
	if entry.depthCut {
		a.returnBorrowCutLog = append(a.returnBorrowCutLog, nil)
	}
	if !entry.depthCut && len(entry.cutOn) == 0 && a.returnBorrowFuncAnalyzed[fn] && fn != a.currentFuncDecl {
		if a.returnBorrowFuncSummaries == nil {
			a.returnBorrowFuncSummaries = map[*ast.FuncDecl]returnBorrowFlow{}
		}
		a.returnBorrowFuncSummaries[fn] = summary
	} else if a.returnBorrowQueryMemo != nil {
		a.returnBorrowQueryMemo[fn] = entry
	}
	return summary
}

// returnBorrowWalkReturnType is the declared return type of the function being summarized.
func (a *Analyzer) returnBorrowWalkReturnType() Type {
	if a.returnBorrowWalkFn == nil {
		return nil
	}
	if sym := a.funcDeclSymbols[a.returnBorrowWalkFn]; sym != nil {
		if fnType, ok := sym.Type.(*FuncType); ok {
			return fnType.Return
		}
	}
	return nil
}

// returnBorrowRootMemo is the query memo for top-level queries rooted at root. Every such query
// has the same active set at each callee it reaches, so an entry one query stored is exactly what
// the next would recompute -- while the analyzed bodies stay the same, which is why the memos are
// dropped once another function's analysis begins (a callee analyzed since then summarizes more
// precisely). Without it every deferred check and every recording pass re-walked the whole
// recursive cluster its function belongs to: ~130k summary walks for the parser.
func (a *Analyzer) returnBorrowRootMemo(root *ast.FuncDecl) map[*ast.FuncDecl]returnBorrowQueryEntry {
	if a.returnBorrowRootMemos == nil || a.returnBorrowRootMemosFor != a.currentFuncDecl {
		a.returnBorrowRootMemos = map[*ast.FuncDecl]map[*ast.FuncDecl]returnBorrowQueryEntry{}
		a.returnBorrowRootMemosFor = a.currentFuncDecl
	}
	memo := a.returnBorrowRootMemos[root]
	if memo == nil {
		memo = map[*ast.FuncDecl]returnBorrowQueryEntry{}
		a.returnBorrowRootMemos[root] = memo
	}
	return memo
}

func (a *Analyzer) returnBorrowQueryEntryValid(entry returnBorrowQueryEntry, active map[*ast.FuncDecl]bool) bool {
	if entry.depthCut {
		return false
	}
	for _, cut := range entry.cutOn {
		if !active[cut] {
			return false
		}
	}
	return true
}

// returnBorrowCycleIterationLimit bounds the fixpoint iteration of a recursive summary. The flow
// lattice is finite (Local plus two index sets over the parameters), so the iteration converges
// long before this; the limit only guards a non-monotone walk, which falls back to the old cut.
const returnBorrowCycleIterationLimit = 64

// computeReturnBorrowFlowForFunc summarizes fn. A recursive call back into fn is answered from the
// current approximation of fn's own summary, starting from the empty flow and growing it until
// the body's summary adds nothing: the least fixpoint of the (monotone) flow equations, so every
// finite recursion path is covered. Assuming "borrows every parameter" at the cycle instead made
// the whole parser's recursive expression cluster claim to return views of its Parser argument.
func (a *Analyzer) computeReturnBorrowFlowForFunc(fn *ast.FuncDecl, active map[*ast.FuncDecl]bool) returnBorrowFlow {
	if a.returnBorrowCycleAssume == nil {
		a.returnBorrowCycleAssume = map[*ast.FuncDecl]returnBorrowFlow{}
	}
	if _, nested := a.returnBorrowCycleAssume[fn]; nested {
		return a.computeReturnBorrowFlowForFuncOnce(fn, active)
	}
	assumed := returnBorrowFlow{}
	a.returnBorrowCycleAssume[fn] = assumed
	defer delete(a.returnBorrowCycleAssume, fn)
	for iteration := 0; ; iteration++ {
		logStart := len(a.returnBorrowCutLog)
		summary := a.computeReturnBorrowFlowForFuncOnce(fn, active)
		cutSelf := false
		for _, cut := range a.returnBorrowCutLog[logStart:] {
			if cut == fn {
				cutSelf = true
				break
			}
		}
		if !cutSelf {
			return summary
		}
		merged := mergeReturnBorrowFlow(assumed, summary)
		if !returnBorrowFlowGrew(assumed, merged) {
			return merged
		}
		if iteration+1 >= returnBorrowCycleIterationLimit {
			return mergeReturnBorrowFlow(merged, a.allBorrowedParamFlow(fn))
		}
		assumed = merged
		a.returnBorrowCycleAssume[fn] = assumed
		a.returnBorrowCutLog = a.returnBorrowCutLog[:logStart]
		// Memoized callee summaries that read the old approximation are stale.
		for callee, entry := range a.returnBorrowQueryMemo {
			for _, cut := range entry.cutOn {
				if cut == fn {
					delete(a.returnBorrowQueryMemo, callee)
					break
				}
			}
		}
	}
}

func (a *Analyzer) computeReturnBorrowFlowForFuncOnce(fn *ast.FuncDecl, active map[*ast.FuncDecl]bool) returnBorrowFlow {
	active[fn] = true
	defer delete(active, fn)
	savedWalkFn := a.returnBorrowWalkFn
	a.returnBorrowWalkFn = fn
	defer func() { a.returnBorrowWalkFn = savedWalkFn }()

	savedNamespace, savedUsings, savedScope := a.currentNamespace, a.currentUsings, a.currentScope
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		if index := lastDotIndex(sym.Name); index >= 0 {
			a.currentNamespace = sym.Name[:index]
		} else {
			a.currentNamespace = ""
		}
	}
	a.currentUsings = append([]string(nil), a.funcDeclUsings[fn]...)
	defer func() {
		a.currentNamespace, a.currentUsings, a.currentScope = savedNamespace, savedUsings, savedScope
	}()

	aliases := map[string]returnBorrowFlow{}
	params := a.expandedFuncDeclParams(fn)
	var fnType *FuncType
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		fnType, _ = sym.Type.(*FuncType)
	}
	// A function returning a reference TO a reference/view (`-> sview&`) hands out the address of
	// the field it reads, so its reads are not content reads.
	savedContentReads := a.returnBorrowContentReads
	a.returnBorrowContentReads = true
	if fnType != nil {
		if ref, ok := fnType.Return.(*RefType); ok && (ref.Elem == nil || isBorrowLikeType(ref.Elem)) {
			a.returnBorrowContentReads = false
		}
	} else {
		a.returnBorrowContentReads = false
	}
	defer func() { a.returnBorrowContentReads = savedContentReads }()
	a.currentScope = NewScope(a.globalScope)
	for index, param := range params {
		var paramType Type
		if fnType != nil && index < len(fnType.Params) {
			paramType = fnType.Params[index]
		}
		if paramType == nil {
			paramType = a.resolveType(param.Type)
		}
		a.currentScope.Define(&Symbol{Name: param.Name, Kind: SymbolParam, Type: paramType, ParamIndex: index, Node: fn})
		if a.typeCarriesBorrowedStorage(paramType, map[Type]bool{}) {
			aliases[param.Name] = returnBorrowFlow{Params: map[int]bool{index: true}}
		}
	}
	return a.returnBorrowFlowForStatements(fn.Body, aliases, active, map[*Symbol]bool{}, true)
}

// defineReturnBorrowConditionBindings binds the optional unwraps a true condition guarantees
// (`if let v = x`, `if x is v`, through `and` and parentheses) to the unwrapped value's flow: the
// payload is a copy of what x holds. Other condition bindings stay unbound, so a read of one
// keeps its conservative flow.
func (a *Analyzer) defineReturnBorrowConditionBindings(cond ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		a.defineReturnBorrowConditionBindings(n.Inner, aliases, active, localBindings)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.defineReturnBorrowConditionBindings(n.Left, aliases, active, localBindings)
			a.defineReturnBorrowConditionBindings(n.Right, aliases, active, localBindings)
		case lexer.TOKEN_IS:
			a.defineReturnBorrowPatternTestBindings(n, aliases, active, localBindings)
		case lexer.TOKEN_OR:
			// `x is P(b) or y is Q(b)`: the shared binder is the payload of whichever
			// alternative matched, so it holds the union of the alternatives' flows.
			var tests []*ast.BinaryExpr
			collectOrPatternTests(n, &tests)
			merged := map[string]returnBorrowFlow{}
			nodes := map[string]ast.Node{}
			var order []string
			for _, test := range tests {
				_, subject, pattern, ok := unwrapDirectConditionPattern(test)
				if !ok || pattern == nil {
					continue
				}
				subjectFlow := a.returnBorrowFlowForExpr(subject, aliases, active, localBindings)
				for _, binder := range returnBorrowMatchPatternBinders(pattern, nil) {
					flow := subjectFlow
					if binder.view {
						flow = mergeReturnBorrowFlow(flow, a.returnBorrowAddressFlow(subject, aliases, active, localBindings))
					}
					if _, seen := merged[binder.name]; !seen {
						order = append(order, binder.name)
						nodes[binder.name] = n
					}
					merged[binder.name] = mergeReturnBorrowFlow(merged[binder.name], flow)
				}
			}
			for _, name := range order {
				a.defineReturnBorrowBinding(aliases, name, nodes[name], nil, merged[name])
			}
		}
	case *ast.OptionalBindExpr:
		if n.Name == "" || n.Name == "_" {
			return
		}
		flow := a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
		a.defineReturnBorrowBinding(aliases, n.Name, n, nil, flow)
	}
}

// defineReturnBorrowPatternTestBindings defines the binders of `subject is Variant(binder, ...)`:
// they are the subject's payload, as in a match arm.
func (a *Analyzer) defineReturnBorrowPatternTestBindings(test *ast.BinaryExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	_, subject, pattern, ok := unwrapDirectConditionPattern(test)
	if !ok || pattern == nil {
		return
	}
	binders := returnBorrowMatchPatternBinders(pattern, nil)
	if len(binders) == 0 {
		return
	}
	subjectFlow := a.returnBorrowFlowForExpr(subject, aliases, active, localBindings)
	for _, binder := range binders {
		flow := subjectFlow
		if binder.view {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowAddressFlow(subject, aliases, active, localBindings))
		}
		a.defineReturnBorrowBinding(aliases, binder.name, binder.node, nil, flow)
	}
}

func (a *Analyzer) allBorrowedParamFlow(fn *ast.FuncDecl) returnBorrowFlow {
	flow := returnBorrowFlow{}
	if fn == nil {
		return flow
	}
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		if fnType, ok := sym.Type.(*FuncType); ok {
			for index, paramType := range fnType.Params {
				if a.typeCarriesBorrowedStorage(paramType, map[Type]bool{}) {
					if flow.Params == nil {
						flow.Params = map[int]bool{}
						flow.Contents = map[int]bool{}
					}
					// Contents is a reference parameter's POINTEE: a by-value parameter's value is
					// already its Params flow. The reference itself -- the caller's address -- can
					// reach the result only if the result can hold a borrow into the referent.
					if ref, isRef := paramType.(*RefType); isRef {
						flow.Contents[index] = true
						if ref.Elem != nil && fnType.Return != nil && !a.returnBorrowResultMayTarget(fnType.Return, ref.Elem) {
							continue
						}
					}
					flow.Params[index] = true
				}
			}
		}
	}
	return flow
}

// returnBorrowFlowForValueBlock walks a value-form loop (`for x in xs |acc| -> acc:`, desugared to
// an ExprBlock): its statements write the captured outer bindings, so the block's environment
// flows back out (the trust scan relies on this). It returns what the statements return and the
// block's value.
func (a *Analyzer) returnBorrowFlowForValueBlock(block *ast.ExprBlock, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) (returnBorrowFlow, returnBorrowFlow) {
	outerScope := a.currentScope
	a.currentScope = NewScope(outerScope)
	blockEnv := cloneReturnBorrowAliases(aliases)
	returned := a.returnBorrowFlowForStatements(block.Stmts, blockEnv, active, localBindings, false)
	var value returnBorrowFlow
	if inner, isBlock := block.Value.(*ast.ExprBlock); isBlock && inner != nil {
		// The lowered loop nests its accumulator block as the outer block's value.
		innerReturned, innerValue := a.returnBorrowFlowForValueBlock(inner, blockEnv, active, localBindings)
		returned = mergeReturnBorrowFlow(returned, innerReturned)
		value = innerValue
	} else {
		value = a.returnBorrowFlowForExpr(block.Value, blockEnv, active, localBindings)
	}
	a.currentScope = outerScope
	for name := range aliases {
		aliases[name] = mergeReturnBorrowFlow(aliases[name], blockEnv[name])
	}
	return returned, value
}

func (a *Analyzer) returnBorrowFlowForStatements(stmts []ast.Stmt, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool, tailValue bool) returnBorrowFlow {
	var returned returnBorrowFlow
	for index, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			if block, isBlock := n.Value.(*ast.ExprBlock); isBlock && block != nil {
				// `n: u32 = for c in xs |acc| -> acc:` is a value-form loop in value position.
				blockReturned, value := a.returnBorrowFlowForValueBlock(block, aliases, active, localBindings)
				returned = mergeReturnBorrowFlow(returned, blockReturned)
				typ := a.exprTypes[n.Value]
				if typ == nil && n.Type != nil {
					typ = a.returnBorrowDeclaredType(n.Type)
				}
				a.defineReturnBorrowBinding(aliases, n.Name, n, typ, value)
				continue
			}
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			flow := a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
			typ := a.exprTypes[n.Value]
			if typ == nil && n.Type != nil {
				typ = a.returnBorrowDeclaredType(n.Type)
			}
			if n.Type != nil && a.returnBorrowRefParamPlace(returnBorrowStripParens(n.Value)) && returnBorrowOwnedValueType(a.returnBorrowDeclaredType(n.Type), map[Type]bool{}) && returnBorrowOwnedValueType(typ, map[Type]bool{}) {
				// `pat: P = arm.payloads[i]` copies a value out of the reference parameter's pointee:
				// it holds what the pointee holds, not the pointee's address.
				flow = a.returnBorrowReferentContentsFlow(n.Value, aliases, active, localBindings)
			}
			a.defineReturnBorrowBinding(aliases, n.Name, n, typ, flow)
		case *ast.AssignStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Target, aliases, active, localBindings)
			if ident, ok := n.Target.(*ast.Ident); ok {
				a.setReturnBorrowAlias(aliases, ident.Name, a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings), true)
			} else if root := returnBorrowPlaceRootName(n.Target); root != "" {
				// A store into a field/element of a local (`h.r <- &x`, `xs[0] <- &x`) makes the
				// whole local carry the stored value's borrows.
				a.setReturnBorrowAlias(aliases, root, a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings), true)
			}
		case *ast.AugAssignStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Target, aliases, active, localBindings)
			if n.CollectionAppend != nil {
				a.noteReturnBorrowCallArgumentStores(n.CollectionAppend, aliases, active, localBindings)
			}
			if root := returnBorrowPlaceRootName(n.Target); root != "" {
				a.setReturnBorrowAlias(aliases, root, a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings), true)
			}
		case *ast.TupleBindStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			flow := a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
			for _, name := range n.Names {
				if n.Declare {
					a.defineReturnBorrowBinding(aliases, name.Name, n, nil, flow)
				} else if name.Name != "" && name.Name != "_" {
					a.setReturnBorrowAlias(aliases, name.Name, flow, true)
				}
			}
		case *ast.IterForStmt:
			a.noteReturnBorrowCallArgumentStores(n.Source, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.WhereFilter, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Filter, aliases, active, localBindings)
			// A reference binding holds an address into the iterated storage; a value binding
			// holds what the source's elements hold.
			var binderFlow returnBorrowFlow
			var binderType Type
			switch {
			case n.Mode != ast.IterBindValue:
				binderFlow = a.returnBorrowAddressFlow(n.Source, aliases, active, localBindings)
				binderType = &RefType{Mutable: n.Mode == ast.IterBindMutableRef}
			default:
				binderFlow = a.returnBorrowIterValueFlow(n.Source, aliases, active, localBindings)
			}
			loopEnv := cloneReturnBorrowAliases(aliases)
			outerScope := a.currentScope
			a.currentScope = NewScope(outerScope)
			names := returnBorrowIterPatternNames(n.Pattern)
			for _, name := range names {
				a.defineReturnBorrowBinding(loopEnv, name, n.Pattern, binderType, binderFlow)
			}
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Body, loopEnv, active, localBindings, false))
			a.currentScope = outerScope
			for _, name := range names {
				if outer, existed := aliases[name]; existed {
					loopEnv[name] = outer
				} else {
					delete(loopEnv, name)
				}
			}
			mergeReturnBorrowAliasMaps(aliases, loopEnv)
		case *ast.ReturnStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			returnType := a.returnBorrowWalkReturnType()
			if n.Value != nil && a.returnBorrowRefParamPlace(returnBorrowStripParens(n.Value)) && returnBorrowOwnedValueType(returnType, map[Type]bool{}) {
				// `return p` from `p: lmut P` / `p: P&` with `-> P` copies the pointee out: the
				// result holds what the referent holds (Contents), never the caller's address.
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowReferentContentsFlow(n.Value, aliases, active, localBindings))
				continue
			}
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForCopiedValue(n.Value, returnType, aliases, active, localBindings))
		case *ast.ExprStmt:
			if block, isBlock := n.Expr.(*ast.ExprBlock); isBlock && block != nil {
				blockReturned, value := a.returnBorrowFlowForValueBlock(block, aliases, active, localBindings)
				returned = mergeReturnBorrowFlow(returned, blockReturned)
				if tailValue && index+1 == len(stmts) {
					returned = mergeReturnBorrowFlow(returned, value)
				}
				continue
			}
			a.noteReturnBorrowCallArgumentStores(n.Expr, aliases, active, localBindings)
			if tailValue && index+1 == len(stmts) {
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForExpr(n.Expr, aliases, active, localBindings))
			}
		case *ast.DiscardStmt:
			if call := returnBorrowDiscardedCall(n); call != nil {
				a.noteReturnBorrowCallArgumentStores(call, aliases, active, localBindings)
			}
		case *ast.IfStmt:
			a.noteReturnBorrowConditionStores(n.Cond, aliases, active, localBindings)
			for _, clause := range n.Elifs {
				a.noteReturnBorrowConditionStores(clause.Cond, aliases, active, localBindings)
			}
			branchEnvs := make([]map[string]returnBorrowFlow, 0, len(n.Elifs)+2)
			outerScope := a.currentScope
			thenEnv := cloneReturnBorrowAliases(aliases)
			a.currentScope = NewScope(outerScope)
			a.defineReturnBorrowConditionBindings(n.Cond, thenEnv, active, localBindings)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Then, thenEnv, active, localBindings, false))
			branchEnvs = append(branchEnvs, thenEnv)
			for _, clause := range n.Elifs {
				elifEnv := cloneReturnBorrowAliases(aliases)
				a.currentScope = NewScope(outerScope)
				a.defineReturnBorrowConditionBindings(clause.Cond, elifEnv, active, localBindings)
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(clause.Body, elifEnv, active, localBindings, false))
				branchEnvs = append(branchEnvs, elifEnv)
			}
			elseEnv := cloneReturnBorrowAliases(aliases)
			a.currentScope = NewScope(outerScope)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Else, elseEnv, active, localBindings, false))
			a.currentScope = outerScope
			branchEnvs = append(branchEnvs, elseEnv)
			for _, env := range branchEnvs {
				mergeReturnBorrowAliasMaps(aliases, env)
			}
		case *ast.WhileStmt:
			a.noteReturnBorrowCallArgumentStores(n.Cond, aliases, active, localBindings)
			loopEnv := cloneReturnBorrowAliases(aliases)
			outerScope := a.currentScope
			a.currentScope = NewScope(outerScope)
			// `while current is v`: v is rebound from the subject on every iteration; a read of the
			// subject joins every store into it, so the binding covers each iteration's value.
			a.defineReturnBorrowConditionBindings(n.Cond, loopEnv, active, localBindings)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Body, loopEnv, active, localBindings, false))
			a.currentScope = outerScope
			mergeReturnBorrowAliasMaps(aliases, loopEnv)
		case *ast.ForStmt:
			a.noteReturnBorrowCallArgumentStores(n.Start, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.End, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Step, aliases, active, localBindings)
			loopEnv := cloneReturnBorrowAliases(aliases)
			outerScope := a.currentScope
			a.currentScope = NewScope(outerScope)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Body, loopEnv, active, localBindings, false))
			a.currentScope = outerScope
			mergeReturnBorrowAliasMaps(aliases, loopEnv)
		case *ast.CanStmt, *ast.ScopeStmt, *ast.RegionStmt, *ast.InStoreStmt, *ast.PoolStmt, *ast.LockStmt,
			*ast.CheckpointStmt, *ast.GroupedCheckpointStmt, *ast.StaticBlockStmt,
			*ast.ParallelForStmt, *ast.StaticIfStmt:
			// Block statements: a `return` nested in `can ...:`, `in r:`, a lock, an iterator loop,
			// etc. is a return of the function all the same. Without these arms every aggregate
			// return inside an effect block escaped the check (`can Abort.Panic: return H{r: &x}`).
			// A static-if's branches are alternatives: each starts from the environment before it.
			blockEnvs := []map[string]returnBorrowFlow{}
			for _, body := range returnBorrowChildBlocks(n) {
				blockEnv := cloneReturnBorrowAliases(aliases)
				outerScope := a.currentScope
				a.currentScope = NewScope(outerScope)
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(body, blockEnv, active, localBindings, false))
				a.currentScope = outerScope
				blockEnvs = append(blockEnvs, blockEnv)
			}
			for _, env := range blockEnvs {
				mergeReturnBorrowAliasMaps(aliases, env)
			}
		case *ast.MatchStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Store, aliases, active, localBindings)
			binders := make([][]returnBorrowMatchBinder, len(n.Arms))
			var subjectFlow, subjectViewFlow returnBorrowFlow
			for index, arm := range n.Arms {
				binders[index] = returnBorrowMatchPatternBinders(arm.Pattern, nil)
				for _, binder := range binders[index] {
					if binder.view {
						subjectViewFlow = a.returnBorrowAddressFlow(n.Value, aliases, active, localBindings)
					}
				}
			}
			if n.Value != nil {
				subjectFlow = a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
			}
			// Every arm starts from the environment before the match; their writes join afterwards.
			armEnvs := make([]map[string]returnBorrowFlow, 0, len(n.Arms))
			for index, arm := range n.Arms {
				armEnv := cloneReturnBorrowAliases(aliases)
				outerScope := a.currentScope
				a.currentScope = NewScope(outerScope)
				for _, binder := range binders[index] {
					flow := subjectFlow
					if binder.view {
						flow = mergeReturnBorrowFlow(flow, subjectViewFlow)
					}
					a.defineReturnBorrowBinding(armEnv, binder.name, binder.node, nil, flow)
				}
				a.noteReturnBorrowCallArgumentStores(arm.Guard, armEnv, active, localBindings)
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(arm.Body, armEnv, active, localBindings, false))
				a.currentScope = outerScope
				armEnvs = append(armEnvs, armEnv)
			}
			for _, env := range armEnvs {
				mergeReturnBorrowAliasMaps(aliases, env)
			}
		}
	}
	return returned
}

func cloneReturnBorrowAliases(src map[string]returnBorrowFlow) map[string]returnBorrowFlow {
	cloned := make(map[string]returnBorrowFlow, len(src))
	for name, flow := range src {
		cloned[name] = flow
	}
	return cloned
}

func mergeReturnBorrowAliasMaps(dst, src map[string]returnBorrowFlow) {
	if dst == nil {
		// The caller keeps no alias environment (a value walked outside a body).
		return
	}
	for name, flow := range src {
		dst[name] = mergeReturnBorrowFlow(dst[name], flow)
	}
}

func lastDotIndex(value string) int {
	for index := len(value) - 1; index >= 0; index-- {
		if value[index] == '.' {
			return index
		}
	}
	return -1
}

// returnBorrowChildBlocks lists the nested bodies of the block statements that
// returnBorrowFlowForStatements does not special-case (If/While/For/Match have their own arms).
func returnBorrowChildBlocks(stmt ast.Stmt) [][]ast.Stmt {
	switch s := stmt.(type) {
	case *ast.CanStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.ScopeStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.RegionStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.InStoreStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.PoolStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.LockStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.CheckpointStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.GroupedCheckpointStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.StaticBlockStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.IterForStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.ParallelForStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.StaticIfStmt:
		out := [][]ast.Stmt{s.Then}
		for _, clause := range s.Elifs {
			out = append(out, clause.Body)
		}
		return append(out, s.Else)
	}
	return nil
}

// returnBorrowPlaceRootName returns the local identifier a field/index place is rooted at
// (`h.r` -> "h", `xs[i].r` -> "xs"), or "" for a non-place.
func returnBorrowPlaceRootName(expr ast.Expr) string {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.FieldExpr:
			expr = n.Object
		case *ast.IndexExpr:
			expr = n.Object
		case *ast.Ident:
			return n.Name
		default:
			return ""
		}
	}
}

// returnBorrowContainerInsertMethods are the container methods that retain their argument in the
// receiver's storage.
var returnBorrowContainerInsertMethods = map[string]bool{
	"push": true, "extend": true, "insert": true, "put": true, "add": true, "append": true,
	"push_back": true, "push_front": true, "set": true,
}

// noteReturnBorrowCallArgumentStores records the stores a call may make into its arguments:
//   - a call with a known signature may store its other arguments through each writable parameter
//     (`mutable T&`, `lmut`, `mutable view[T]`) whose referent can hold a borrow: `add(out, &x)`
//     with `xs: mutable darray[i64&]&` makes `out` carry `&x`'s borrows, as `out.push(&x)` does. A
//     reference parameter receives the argument's ADDRESS (`add(out, x)` with `r: i64&` borrows x);
//   - a builtin method may store its arguments into its receiver (`xs.push(&x)`) unless it only
//     reads;
//   - any other builtin may store its other arguments through an address or writable reference.
//
// Every call it visits is marked processed for the store environment's trust scan.
func (a *Analyzer) noteReturnBorrowCallArgumentStores(expr ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	if expr == nil {
		return
	}
	a.walkStaticExpr(expr, func(e ast.Expr) bool {
		// A binding expression is walked with its binders in scope.
		switch n := e.(type) {
		case *ast.MatchExpr:
			a.returnBorrowFlowForMatchExpr(n, aliases, active, localBindings)
			return true
		case *ast.ListComprehensionExpr:
			a.returnBorrowFlowForComprehension(n, aliases, active, localBindings)
			return true
		case *ast.QueryExpr:
			if n.Pattern == nil {
				a.returnBorrowFlowForQuery(n, aliases, active, localBindings)
				return true
			}
		case *ast.TernaryExpr:
			binders := map[ast.Node]bool{}
			returnBorrowMarkConditionBinders(n.Cond, binders)
			if len(binders) > 0 {
				env := cloneReturnBorrowAliases(aliases)
				outerScope := a.currentScope
				a.currentScope = NewScope(outerScope)
				a.noteReturnBorrowAndChainStores(n.Cond, env, active, localBindings)
				a.defineReturnBorrowConditionBindings(n.Cond, env, active, localBindings)
				a.noteReturnBorrowCallArgumentStores(n.Value, env, active, localBindings)
				a.currentScope = outerScope
				a.noteReturnBorrowCallArgumentStores(n.Alt, aliases, active, localBindings)
				return true
			}
		}
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		if a.returnBorrowRecordingActive() {
			a.returnBorrowDeclProcessedCalls[call] = true
		}
		args := returnBorrowCallArgs(call)
		otherArgsFlow := func(skip int, params []Type) returnBorrowFlow {
			return a.returnBorrowCallOtherArgsFlow(args, skip, params, aliases, active, localBindings)
		}
		if fnType := a.returnBorrowCalleeSignature(call); fnType != nil {
			for index, paramType := range fnType.Params {
				if index >= len(args) {
					break
				}
				if !a.returnBorrowWritableParam(paramType) {
					continue
				}
				root := returnBorrowPlaceRootName(returnBorrowStripAddr(args[index]))
				if root == "" {
					continue
				}
				if flow := otherArgsFlow(index, fnType.Params); !flow.empty() {
					a.setReturnBorrowAlias(aliases, root, flow, true)
				}
			}
			return false
		}
		if field, isField := call.Func.(*ast.FieldExpr); isField && field != nil && !returnBorrowReadOnlyMethods[field.Field] {
			if root := returnBorrowPlaceRootName(field.Object); root != "" {
				if flow := otherArgsFlow(-1, nil); !flow.empty() {
					a.setReturnBorrowAlias(aliases, root, flow, true)
				}
			}
		}
		for index, arg := range args {
			_, isAddr := returnBorrowStripParens(arg).(*ast.AddrOfExpr)
			ref, isRef := a.exprTypes[arg].(*RefType)
			if !isAddr && !(isRef && ref != nil && ref.Mutable) {
				continue
			}
			root := returnBorrowPlaceRootName(returnBorrowStripAddr(arg))
			if root == "" {
				continue
			}
			if flow := otherArgsFlow(index, nil); !flow.empty() {
				a.setReturnBorrowAlias(aliases, root, flow, true)
			}
		}
		return false
	})
}

// returnBorrowCallOtherArgsFlow is what a call may store through its writable argument skip (-1: a
// builtin's receiver): every other argument's flow, where a reference argument contributes its
// ADDRESS when the writable parameter can hold a borrow into the referent and only what the
// referent holds otherwise.
func (a *Analyzer) returnBorrowCallOtherArgsFlow(args []ast.Expr, skip int, params []Type, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	flow := returnBorrowFlow{}
	for other, arg := range args {
		if other == skip {
			continue
		}
		if params != nil && other < len(params) {
			if ref, isRef := params[other].(*RefType); isRef {
				if outlivesFrameStorage(ref.Storage) {
					// A `heap`/`static` parameter only accepts frame-outliving storage.
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
	if fnType, ok := a.exprTypes[call.Func].(*FuncType); ok && fnType != nil {
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
	objType := a.exprTypes[field.Object]
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
	objType := a.exprTypes[n.Object]
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

// returnBorrowMayTarget reports whether a value of type holder may hold a borrow into storage of
// type referent: some reference or view reachable in holder targets referent or one of its
// by-value components. Anything opaque (a type parameter, an unresolved generic, `void&`) may.
func (a *Analyzer) returnBorrowMayTarget(holder Type, referent Type) bool {
	key := [2]Type{holder, referent}
	if answer, ok := a.returnBorrowMayTargetMemo[key]; ok {
		return answer
	}
	answer := a.computeReturnBorrowMayTarget(holder, referent)
	if a.returnBorrowMayTargetMemo == nil {
		a.returnBorrowMayTargetMemo = map[[2]Type]bool{}
	}
	a.returnBorrowMayTargetMemo[key] = answer
	return answer
}

// returnBorrowResultMayTarget is returnBorrowMayTarget for a returned value: its own outer
// reference or view is a borrow it hands out, so it counts.
func (a *Analyzer) returnBorrowResultMayTarget(result Type, referent Type) bool {
	key := [2]Type{result, referent}
	if answer, ok := a.returnBorrowResultMayTargetMemo[key]; ok {
		return answer
	}
	answer := a.computeReturnBorrowTargetsReach(result, referent, true)
	if a.returnBorrowResultMayTargetMemo == nil {
		a.returnBorrowResultMayTargetMemo = map[[2]Type]bool{}
	}
	a.returnBorrowResultMayTargetMemo[key] = answer
	return answer
}

func (a *Analyzer) computeReturnBorrowMayTarget(holder Type, referent Type) bool {
	// The writable parameter's own reference is how the callee reaches the holder, not a stored
	// borrow: only what the holder stores counts.
	switch outer := holder.(type) {
	case *RefType:
		holder = outer.Elem
	case *ViewType:
		holder = outer.Elem
	}
	// A borrow stored into the holder at the call is attributed to the referent's ADDRESS only when
	// it can point at the referent's by-value storage; a darray, dict or set inside the referent keeps
	// its elements in region storage the region checks own, so a borrow of those elements is part
	// of what the referent holds, not of where it lives.
	return a.computeReturnBorrowTargetsReach(holder, referent, true)
}

func (a *Analyzer) computeReturnBorrowTargetsReach(holder Type, referent Type, inlineOnly bool) bool {
	targets, opaque := []Type{}, false
	a.collectReturnBorrowTargets(holder, &targets, &opaque, map[Type]bool{})
	if opaque {
		return true
	}
	// A holder with no reference or view inside can hold no borrow of anything.
	if len(targets) == 0 {
		return false
	}
	components, opaque := []Type{}, false
	collectReturnBorrowComponents(referent, inlineOnly, &components, &opaque, map[Type]bool{})
	if opaque {
		return true
	}
	for _, target := range targets {
		for _, component := range components {
			if SameType(target, component) {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) collectReturnBorrowTargets(t Type, out *[]Type, opaque *bool, seen map[Type]bool) {
	if t == nil || *opaque || seen[t] {
		return
	}
	seen[t] = true
	switch tt := t.(type) {
	case *RefType:
		if tt.Elem == nil {
			*opaque = true
			return
		}
		if builtin, ok := tt.Elem.(*BuiltinType); ok && builtin.Name == "void" {
			*opaque = true
			return
		}
		// A `heap`/`static` reference cannot point into a frame (storing `&local` or a forwarded
		// stack reference into one is rejected), so it is no target; what its pointee holds still is.
		if !outlivesFrameStorage(tt.Storage) {
			*out = append(*out, tt.Elem)
		}
		// A reference to a holder reaches whatever that holder holds.
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *ViewType:
		if tt.Elem == nil {
			*opaque = true
			return
		}
		*out = append(*out, tt.Elem)
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *SViewType, *CStrType:
		*out = append(*out, &BuiltinType{Name: "u8"}, &BuiltinType{Name: "i8"})
	case *BuiltinType, *BitIntType, *ConstEnumType, *BitGroupType:
	case *OpaqueType:
		// An `extern` handle type (LLVMValueRef, ...) is a foreign pointer the language cannot
		// dereference: it holds no Elisa reference or view, so it targets nothing.
	case *ArrayType:
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *DArrayType:
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *OptionalType:
		a.collectReturnBorrowTargets(tt.Value, out, opaque, seen)
	case *ErrorUnionType:
		a.collectReturnBorrowTargets(tt.Value, out, opaque, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			a.collectReturnBorrowTargets(field.Type, out, opaque, seen)
		}
	case *DictType:
		a.collectReturnBorrowTargets(tt.Key, out, opaque, seen)
		a.collectReturnBorrowTargets(tt.Value, out, opaque, seen)
	case *SetType:
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *StructType:
		if len(tt.TypeParams) > 0 {
			*opaque = true
			return
		}
		for _, field := range tt.Fields {
			// A `tail` field is the struct's own trailing storage, typed as a reference to it:
			// nothing can be stored in it but its elements.
			if ref, isRef := field.Type.(*RefType); isRef && ref != nil && field.IsTail {
				a.collectReturnBorrowTargets(ref.Elem, out, opaque, seen)
				continue
			}
			a.collectReturnBorrowTargets(field.Type, out, opaque, seen)
		}
	case *EnumType:
		for _, field := range tt.Common {
			a.collectReturnBorrowTargets(field.Type, out, opaque, seen)
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				a.collectReturnBorrowTargets(payload, out, opaque, seen)
			}
		}
	default:
		*opaque = true
	}
}

// collectReturnBorrowComponents collects a type and every type stored BY VALUE inside it: the
// storage a borrow into a value of that type can point at. inlineOnly stops at the element buffers of
// darrays, dicts and sets, keeping only storage laid out inside the value itself.
func collectReturnBorrowComponents(t Type, inlineOnly bool, out *[]Type, opaque *bool, seen map[Type]bool) {
	if t == nil || *opaque || seen[t] {
		return
	}
	seen[t] = true
	*out = append(*out, t)
	switch tt := t.(type) {
	case *RefType, *ViewType, *SViewType, *CStrType, *BuiltinType, *BitIntType, *ConstEnumType, *BitGroupType, *OpaqueType:
	case *ArrayType:
		collectReturnBorrowComponents(tt.Elem, inlineOnly, out, opaque, seen)
	case *DArrayType:
		if !inlineOnly {
			collectReturnBorrowComponents(tt.Elem, inlineOnly, out, opaque, seen)
		}
	case *OptionalType:
		collectReturnBorrowComponents(tt.Value, inlineOnly, out, opaque, seen)
	case *ErrorUnionType:
		collectReturnBorrowComponents(tt.Value, inlineOnly, out, opaque, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			collectReturnBorrowComponents(field.Type, inlineOnly, out, opaque, seen)
		}
	case *DictType:
		if !inlineOnly {
			collectReturnBorrowComponents(tt.Key, inlineOnly, out, opaque, seen)
			collectReturnBorrowComponents(tt.Value, inlineOnly, out, opaque, seen)
		}
	case *SetType:
		if !inlineOnly {
			collectReturnBorrowComponents(tt.Elem, inlineOnly, out, opaque, seen)
		}
	case *StructType:
		if len(tt.TypeParams) > 0 {
			*opaque = true
			return
		}
		for _, field := range tt.Fields {
			collectReturnBorrowComponents(field.Type, inlineOnly, out, opaque, seen)
		}
	case *EnumType:
		for _, field := range tt.Common {
			collectReturnBorrowComponents(field.Type, inlineOnly, out, opaque, seen)
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				collectReturnBorrowComponents(payload, inlineOnly, out, opaque, seen)
			}
		}
	default:
		*opaque = true
	}
}

// returnBorrowTypeNameObject reports a member-access object that names a visible type rather than
// a value (`E` in `E.Invalid` or `E.Ident(x)`). A local of the same name shadows the type.
func (a *Analyzer) returnBorrowTypeNameObject(object ast.Expr) bool {
	path, ok := qualifiedTypePathFromExpr(object)
	if !ok {
		return false
	}
	if ident, isIdent := object.(*ast.Ident); isIdent && ident != nil && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(ident.Name); found && sym != nil && (sym.Kind == SymbolLocal || sym.Kind == SymbolParam || sym.Kind == SymbolGlobal) {
			return false
		}
	}
	_, _, visible := a.lookupVisibleType(path)
	return visible
}

// returnBorrowDeclaredType resolves a local's declared type without reporting: a summary may be
// computed for a generic callee outside its type-parameter scope, where `K` is unknown. An
// unresolvable type comes back invalid, which returnBorrowOwnedValueType treats as opaque.
func (a *Analyzer) returnBorrowDeclaredType(expr ast.TypeExpr) Type {
	saved := a.suppressDiagnostics
	a.suppressDiagnostics = true
	defer func() { a.suppressDiagnostics = saved }()
	return a.resolveType(expr)
}

// returnBorrowLocalRebound reports whether the current function assigns the named local directly
// (`r <- v`, `(r, s) <- t`) anywhere. Name-based, so a shadowing binding counts too.
func (a *Analyzer) returnBorrowLocalRebound(name string) bool {
	fn := a.currentFuncDecl
	if fn == nil {
		fn = a.returnBorrowWalkFn
	}
	if fn == nil {
		return true
	}
	if a.returnBorrowReboundNames == nil {
		a.returnBorrowReboundNames = map[*ast.FuncDecl]map[string]bool{}
	}
	rebound, ok := a.returnBorrowReboundNames[fn]
	if !ok {
		rebound = map[string]bool{}
		var walk func(v reflect.Value)
		walk = func(v reflect.Value) {
			if !v.IsValid() {
				return
			}
			switch v.Kind() {
			case reflect.Pointer, reflect.Interface:
				if v.IsNil() {
					return
				}
				switch n := v.Interface().(type) {
				case *ast.AssignStmt:
					if ident, isIdent := returnBorrowStripParens(n.Target).(*ast.Ident); isIdent && ident != nil {
						rebound[ident.Name] = true
					}
				case *ast.TupleBindStmt:
					if !n.Declare {
						for _, target := range n.Names {
							rebound[target.Name] = true
						}
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
		walk(reflect.ValueOf(fn.Body))
		a.returnBorrowReboundNames[fn] = rebound
	}
	return rebound[name]
}

// sliceViewsFrameArray reports a slice whose bytes are an inline fixed array held by value in a local
// or by-value parameter of the current function (`buf[0:2]`, `s.arr[1:]`, `grid[2][0:4]`). Every step
// from the root to the sliced array must be a by-value field or fixed-array index, so a reference, a
// view, a container element or a global anywhere on the path keeps the slice out of this rule.
func (a *Analyzer) sliceViewsFrameArray(n *ast.SliceExpr) bool {
	if a == nil || n == nil || a.currentScope == nil {
		return false
	}
	if _, ok := a.exprTypes[stripParenExpr(n.Object)].(*ArrayType); !ok {
		return false
	}
	root := stripParenExpr(n.Object)
	for {
		switch step := root.(type) {
		case *ast.FieldExpr:
			if _, ok := a.exprTypes[stripParenExpr(step.Object)].(*StructType); !ok {
				return false
			}
			root = stripParenExpr(step.Object)
			continue
		case *ast.IndexExpr:
			if _, ok := a.exprTypes[stripParenExpr(step.Object)].(*ArrayType); !ok {
				return false
			}
			root = stripParenExpr(step.Object)
			continue
		}
		break
	}
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(ident.Name)
	if !found || sym == nil || (sym.Kind != SymbolLocal && sym.Kind != SymbolParam) {
		return false
	}
	switch sym.Type.(type) {
	case *ArrayType, *StructType:
		return true
	}
	return false
}

// collectOrPatternTests gathers the `is` tests of an or-chain of pattern conditions.
func collectOrPatternTests(expr ast.Expr, out *[]*ast.BinaryExpr) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		collectOrPatternTests(n.Inner, out)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_OR:
			collectOrPatternTests(n.Left, out)
			collectOrPatternTests(n.Right, out)
		case lexer.TOKEN_IS:
			*out = append(*out, n)
		}
	}
}
