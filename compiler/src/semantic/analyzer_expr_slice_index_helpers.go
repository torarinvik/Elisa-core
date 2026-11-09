package semantic

import "elisacore/src/ast"

func (a *Analyzer) tupleIndexResultType(tuple *TupleType, indexExpr ast.Expr) (Type, bool) {
	if tuple == nil {
		return nil, false
	}
	value, ok := a.evalConstExpr(indexExpr)
	if !ok || value.Kind != ConstInt {
		a.errorf(indexExpr.Pos(), "tuple index must be a compile-time integer")
		return invalidType, false
	}
	if value.Int < 0 || value.Int >= int64(len(tuple.Fields)) {
		a.errorf(indexExpr.Pos(), "constant tuple index %d out of bounds for %s", value.Int, tuple)
		return invalidType, false
	}
	return tuple.Fields[value.Int].Type, true
}

func safeIndexFallbackOperandType(t Type) bool {
	switch tt := StripAggregateStateType(t).(type) {
	case *ArrayType, *DArrayType, *ViewType, *PackedEnumStoreType:
		return true
	case *RefType:
		if tt.State != RefStateNonNull {
			return false
		}
		switch StripAggregateStateType(tt.Elem).(type) {
		case *ArrayType, *DArrayType, *ViewType, *PackedEnumStoreType:
			return true
		}
	}
	return false
}

func (a *Analyzer) analyzeDenseNodeKeyIndexExpr(expr *ast.IndexExpr, objType Type, keyEnum *EnumType) (Type, bool) {
	if expr == nil || objType == nil || keyEnum == nil {
		return nil, false
	}
	keyInfo, ok := a.denseNodeKeyInfoForExpr(expr.Index)
	if !ok || keyInfo.Enum == nil || keyInfo.StoreRoot == nil {
		a.errorf(expr.Index.Pos(), "node-key indexing requires a key produced by dense_key from an exact frozen store root")
		return invalidType, true
	}
	if keyInfo.Enum != keyEnum {
		a.errorf(expr.Index.Pos(), "node key enum %q does not match indexed object enum %q", keyInfo.Enum.Name, keyEnum.Name)
		return invalidType, true
	}
	storeRoot, storePath, storeType, ok := a.resolveFrozenPackedStoreRootPath(expr.Object)
	if ok && storeType != nil && storeType.Enum != nil {
		if storeType.Enum != keyEnum {
			a.errorf(expr.Pos(), "node key enum %q does not match frozen store %q", keyEnum.Name, storeType.Enum.Name)
			return invalidType, true
		}
		if !samePackedStoreRootPath(storeRoot, storePath, keyInfo.StoreRoot, keyInfo.StorePath) {
			a.errorf(expr.Pos(), "node key and frozen store must share the same exact frozen store root")
			return invalidType, true
		}
		return keyEnum, true
	}
	if tableInfo, ok := a.nodeTableInfoForExpr(expr.Object); ok && tableInfo.Enum != nil && tableInfo.StoreRoot != nil {
		if tableInfo.Enum != keyEnum {
			a.errorf(expr.Pos(), "node key enum %q does not match node table %q", keyEnum.Name, tableInfo.Enum.Name)
			return invalidType, true
		}
		if !samePackedStoreRootPath(tableInfo.StoreRoot, tableInfo.StorePath, keyInfo.StoreRoot, keyInfo.StorePath) {
			a.errorf(expr.Pos(), "node key and node table must share the same exact frozen store root")
			return invalidType, true
		}
		return tableInfo.Elem, true
	}
	if refType, ok := objType.(*RefType); ok && refType.State == RefStateNonNull {
		if storeType, ok := refType.Elem.(*PackedEnumStoreType); ok && storeType != nil && storeType.Enum != nil {
			a.errorf(expr.Object.Pos(), "node-key packed-store indexing requires the exact frozen store root value, not a store reference")
			return invalidType, true
		}
		if _, _, ok := NodeTableParts(refType.Elem); ok {
			a.errorf(expr.Object.Pos(), "node-key table indexing requires the exact node-table value, not a node-table reference")
			return invalidType, true
		}
	}
	a.errorf(expr.Pos(), "node-key indexing requires Expr.Store[Frozen] or NodeTable[Expr, T], got %s", objType)
	return invalidType, true
}

func (a *Analyzer) reportInvalidRegionUse(expr ast.Expr, valueType Type) {
	if expr == nil || valueType == nil {
		return
	}
	refState, ok := a.regionRefStateForExpr(expr)
	if !ok {
		return
	}
	if _, dep, invalid := firstInvalidRegionDependency(refState); invalid {
		label := "value"
		if _, isRef := valueType.(*RefType); isRef {
			label = "reference"
		}
		a.errorf(expr.Pos(), invalidatedRegionFactUseMessage(label, affineValueDisplayName(expr), dep.InvalidatedBy))
	}
}

func (a *Analyzer) reportBorrowedOwnerRefUseAfterConsume(expr ast.Expr, valueType Type) {
	refState, hasRefState := a.borrowedOwnerRefStateForExpr(expr)
	if !hasRefState || !refState.HasDirect {
		return
	}
	consumed, ok := a.lookupAffineValueStateForKey(refState.Direct)
	if !ok || consumed.ConsumedBy == "" {
		return
	}
	// A raw interior pointer aliased out of a Pooled handle (e.g. stashed in a struct
	// field, `holder.p`) dangles once the handle is released, regardless of the value's
	// own type. Genuine owner borrows keep their owner-typed diagnostic.
	if refState.RawInteriorAffineAlias {
		a.errorf(expr.Pos(), consumedFactUseMessage("interior reference", affineValueDisplayName(expr), consumed.ConsumedBy))
		return
	}
	if ownerType, ok := borrowableOwnerRefElemType(valueType); ok {
		a.errorf(expr.Pos(), consumedFactUseMessage(affineHandleKind(ownerType), affineValueDisplayName(expr), consumed.ConsumedBy))
	}
}

func (a *Analyzer) analyzeSliceExpr(expr *ast.SliceExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	indexExpected := a.namedTypes["usize"]
	if indexExpected == nil {
		indexExpected = builtinUsizeType()
	}
	startType := a.analyzeValueExpr(expr.Start, indexExpected)
	endType := a.analyzeValueExpr(expr.End, indexExpected)
	if !IsNumericType(startType) {
		a.errorf(expr.Start.Pos(), "slice start must be numeric, got %s", startType)
	} else if !IsIntegralStorageType(startType) {
		a.errorf(expr.Start.Pos(), "slice start must be integral, got %s", startType)
	}
	if !IsNumericType(endType) {
		a.errorf(expr.End.Pos(), "slice end must be numeric, got %s", endType)
	} else if !IsIntegralStorageType(endType) {
		a.errorf(expr.End.Pos(), "slice end must be integral, got %s", endType)
	}
	if array, ok := objType.(*ArrayType); ok {
		a.checkConstantArraySliceBounds(array, expr.Start, expr.End)
		return &ViewType{Elem: array.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "view"}
	}
	if view, ok := objType.(*DArrayType); ok {
		return &ViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "view", Region: view.Region}
	}
	if view, ok := objType.(*ViewType); ok {
		return &ViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: view.SurfaceName, Region: view.Region}
	}
	if storeType, ok := objType.(*PackedEnumStoreType); ok && storeType.Enum != nil {
		return &ViewType{Elem: storeType.Enum, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "packedview"}
	}
	if cstr, ok := objType.(*CStrType); ok {
		return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), Region: cstr.Region}
	}
	if isStringViewType(objType) {
		region := ""
		if view, ok := objType.(*SViewType); ok {
			region = view.Region
		}
		if view, ok := objType.(*ViewType); ok {
			region = view.Region
		}
		return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), Region: region}
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "slicing requires proven non-null reference, got %s", objType)
			return invalidType
		}
		// A slice inherits the source's mutability: slicing a `mutable darray&`/`mutable array` (or a
		// `mutable view`) yields a `mutable view[T]` capable of write-through; slicing an immutable
		// source yields a read-only `view[T]`. The binding/param TYPE then decides the actual
		// capability — declaring `view[T]` narrows to read-only; declaring `mutable view[T]` requires
		// this source-mutability (AssignableTo rejects laundering a read-only view into a mutable one).
		if array, ok := ref.Elem.(*ArrayType); ok {
			a.checkConstantArraySliceBounds(array, expr.Start, expr.End)
			return &ViewType{Elem: array.Elem, Mutable: ref.Mutable, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "view"}
		}
		if view, ok := ref.Elem.(*DArrayType); ok {
			return &ViewType{Elem: view.Elem, Mutable: ref.Mutable, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "view", Region: view.Region}
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			region := view.Region
			if region == "" {
				region = ref.Region
			}
			return &ViewType{Elem: view.Elem, Mutable: ref.Mutable && view.Mutable, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: view.SurfaceName, Region: region}
		}
		if storeType, ok := ref.Elem.(*PackedEnumStoreType); ok && storeType.Enum != nil {
			return &ViewType{Elem: storeType.Enum, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "packedview"}
		}
		if cstr, ok := ref.Elem.(*CStrType); ok {
			region := ref.Region
			if region == "" {
				region = cstr.Region
			}
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), Region: region}
		}
		if view, ok := ref.Elem.(*SViewType); ok {
			region := ref.Region
			if region == "" {
				region = view.Region
			}
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), Region: region}
		}
		if builtin, ok := ref.Elem.(*BuiltinType); ok && builtin.Name == "u8" {
			if a.currentUnsafePointerCastGrantDepth == 0 {
				a.errorf(expr.Pos(), "slicing an unbounded raw `u8&` into `sview` requires an explicit `trusted Unsafe.PointerCast` or `can Unsafe.PointerCast` boundary; use a bounded `view[u8]` or `cstr` in safe code")
				return invalidType
			}
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), Region: ref.Region}
		}
	}
	a.errorf(expr.Pos(), "slicing requires string, array, view, or packed store type, got %s", objType)
	return invalidType
}

// specializeTypeArgAsIndexValue converts a single bracket argument the parser read as a
// generic type arg back into the value expression it would have been as an index. Only the
// two ambiguous forms qualify: a literal (`fs[0]`) and a bare name (`fs[i]`).
func specializeTypeArgAsIndexValue(arg ast.TypeExpr) (ast.Expr, bool) {
	switch t := arg.(type) {
	case *ast.GenericValueArgTypeExpr:
		return t.Value, true
	case *ast.NamedType:
		if t.Name != "" {
			return &ast.Ident{Position: t.Position, Name: t.Name}, true
		}
	}
	return nil, false
}

// rewriteIndexedElementCall undoes the parser's generic-call reading of `xs[i](...)` when
// `xs` is a value (e.g. a container of functions), not a generic function. `name[arg](...)`
// is ambiguous at parse time -- const-generic args make literals and bare names valid type
// args -- so the parser commits to the specialize-call form and the disambiguation happens
// here, where the operand resolves. Function-typed operands are left alone so the generic
// specialization path keeps producing its diagnostics ("not generic", arg-count, ...).
func (a *Analyzer) rewriteIndexedElementCall(expr *ast.CallExpr) {
	if a == nil || expr == nil {
		return
	}
	spec, ok := expr.Func.(*ast.SpecializeExpr)
	if !ok || spec == nil || len(spec.TypeArgs) != 1 {
		return
	}
	index, ok := specializeTypeArgAsIndexValue(spec.TypeArgs[0])
	if !ok {
		return
	}
	var operandType Type
	switch operand := spec.Operand.(type) {
	case *ast.Ident:
		if operand.Name == "" {
			return
		}
		if a.currentScope != nil {
			if sym, found := a.currentScope.Lookup(operand.Name); found && sym != nil {
				operandType = sym.Type
			}
		}
		if operandType == nil {
			if sym, _, found := a.lookupVisibleGlobal(operand.Name); found && sym != nil {
				operandType = sym.Type
			}
		}
	case *ast.FieldExpr:
		if operand.Field == "" || operand.Object == nil {
			return
		}
		savedSuppress := a.suppressDiagnostics
		restoreReportProbe1 := a.beginProofReportProbe()
		defer restoreReportProbe1.restore()
		a.suppressDiagnostics = true
		receiverType := a.analyzeExpr(operand.Object)
		a.suppressDiagnostics = savedSuppress
		restoreReportProbe1.restore()
		if receiverType == nil || IsInvalidType(receiverType) {
			return
		}
		field, found := a.lookupFieldNoError(receiverType, operand.Field)
		if !found {
			return
		}
		operandType = field.Type
	default:
		return
	}
	if operandType == nil {
		return
	}
	if _, isFunc := operandType.(*FuncType); isFunc {
		return
	}
	expr.Func = &ast.IndexExpr{Position: spec.Position, Object: spec.Operand, Index: index}
}
