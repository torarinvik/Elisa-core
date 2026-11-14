package semantic

import (
	"elisacore/src/ast"
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
	// Index sets are never mutated once a flow is built, so a side that already holds the union
	// is returned as is. Merging a cloned environment back into its origin (every branch join)
	// merges each flow with itself; allocating a copy there was ~13% of all analyzer allocation.
	if returnBorrowIndexSetContains(left, right) {
		return left
	}
	if returnBorrowIndexSetContains(right, left) {
		return right
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

// returnBorrowIndexSetContains reports whether every index of sub is in super.
func returnBorrowIndexSetContains(super, sub map[int]bool) bool {
	if len(sub) > len(super) {
		return false
	}
	for index := range sub {
		if !super[index] {
			return false
		}
	}
	return true
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
	case *ViewType:
		return !a.isProgramRegion(tt.Region)
	case *SViewType:
		return !a.isProgramRegion(tt.Region)
	case *CStrType:
		return !a.isProgramRegion(tt.Region)
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
		if t, ok := a.lookupExprType(expr); ok {
			if a.typeIsBorrowFree(t, map[Type]bool{}) {
				return returnBorrowFlow{}
			}
			// A copied value may contain views into a program-lived store
			// even when its container was allocated in the current frame.
			if flow.Local && len(a.programRegions) != 0 && a.typeViewsProgramLived(t, map[Type]bool{}) {
				flow.Local = false
			}
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
	case *ast.CanExpr:
		return a.returnBorrowFlowForExpr(n.Expr, aliases, active, localBindings)
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
			if source, ok := a.iterBindingSource(sym); ok && source != nil {
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
		if source, ok := a.iterBindingSource(sym); ok && source != nil {
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
		tupleType, _ := a.exprType(n).(*TupleType)
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
		switch listType := a.exprType(n).(type) {
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
		fieldType := a.exprType(n)
		if fieldType == nil || a.typeCarriesBorrowedStorage(fieldType, map[Type]bool{}) {
			if index, ok := a.returnBorrowContentReadParam(n, fieldType, aliases); ok {
				return returnBorrowPointeeReadFlow(n, index, aliases)
			}
			return a.returnBorrowFlowForExpr(n.Object, aliases, active, localBindings)
		}
	case *ast.IndexExpr:
		indexType := a.exprType(n)
		if indexType == nil || a.typeCarriesBorrowedStorage(indexType, map[Type]bool{}) {
			if index, ok := a.returnBorrowContentReadParam(n, indexType, aliases); ok {
				return returnBorrowPointeeReadFlow(n, index, aliases)
			}
			return a.returnBorrowFlowForExpr(n.Object, aliases, active, localBindings)
		}
	case *ast.TernaryExpr:
		alt := a.returnBorrowFlowForExpr(n.Alt, aliases, active, localBindings)
		if !returnBorrowMarkConditionBinders(n.Cond, nil) {
			return mergeReturnBorrowFlow(a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings), alt)
		}
		// The condition's unwraps are in scope in the taken branch.
		savedScope := a.currentScope
		a.currentScope = NewScope(savedScope)
		valueAliases := a.beginReturnBorrowBranch(aliases)
		a.defineReturnBorrowConditionBindings(n.Cond, valueAliases, active, localBindings)
		value := a.returnBorrowFlowForExpr(n.Value, valueAliases, active, localBindings)
		a.endReturnBorrowBranch(aliases)
		a.currentScope = savedScope
		return mergeReturnBorrowFlow(value, alt)
	case *ast.ExprBlock:
		savedScope := a.currentScope
		a.currentScope = NewScope(savedScope)
		blockAliases := a.beginReturnBorrowBranch(aliases)
		flow := a.returnBorrowFlowForStatements(n.Stmts, blockAliases, active, localBindings, false)
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(n.Value, blockAliases, active, localBindings))
		a.endReturnBorrowBranch(aliases)
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
