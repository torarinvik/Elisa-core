package semantic

import "elisacore/src/ast"

func (a *Analyzer) cloneBuiltinCompatible(target Type, source Type, seen map[string]bool) (bool, bool) {
	if target == nil || source == nil {
		return false, false
	}
	target = StripAggregateStateType(target)
	source = StripAggregateStateType(source)
	if IsInvalidType(target) || IsInvalidType(source) {
		return false, true
	}
	if a.containsAffineHandleValues(target, nil) || a.containsAffineHandleValues(source, nil) {
		return false, false
	}
	key := target.String() + " <- " + source.String()
	if seen[key] {
		return false, true
	}
	seen[key] = true
	switch tt := target.(type) {
	case *BuiltinType, *ConstEnumType, *ErrorSetType, *NullType, *DStrType, *SViewType:
		// An sview is a borrowed (ptr, len) into someone else's storage. Cloning
		// it copies only the view header — the result still borrows the same
		// bytes. It does NOT become an owner: persisting bytes into a region is
		// the job of an owned string type, not of clone-on-a-view (which would
		// silently turn a borrow into an owner while keeping the borrow's type).
		return false, SameType(target, source)
	case *TypeParamType, *RefType, *FuncType, *ViewType, *PackedVariantViewType, *StoreRowsViewType, *StoreRowViewType, *DictType, *DictEntryType:
		return false, false
	case *TreeVariantViewType:
		return false, false
	case *TreeNodeType, *TreeCategoryType, *TreeBlockType, *TreeStructType:
		return true, cloneBuiltinTreeTargetCompatible(target, source)
	case *ArrayType:
		sourceArray, ok := source.(*ArrayType)
		if !ok || !arraySizesEqual(tt, sourceArray) {
			return false, false
		}
		needsOwner, ok := a.cloneBuiltinCompatible(tt.Elem, sourceArray.Elem, seen)
		return needsOwner, ok
	case *DArrayType:
		var sourceElem Type
		switch ss := source.(type) {
		case *DArrayType:
			sourceElem = ss.Elem
		case *ViewType:
			sourceElem = ss.Elem
		case *ArrayType:
			sourceElem = ss.Elem
		case *SViewType:
			// A string view (sview) is a bounded (ptr, len) borrow of u8 bytes.
			// Cloning it into a darray[u8] / dstr deep-copies those bytes into the
			// active region, producing an owner independent of the source storage.
			// This is the sanctioned, length-bounded way to persist a string — the
			// cure the unbounded-cstr cast warning points at (docs/26).
			sourceElem = a.namedTypes["u8"]
		default:
			return false, false
		}
		_, ok := a.cloneBuiltinCompatible(tt.Elem, sourceElem, seen)
		return true, ok
	case *OptionalType:
		sourceOptional, ok := source.(*OptionalType)
		if !ok || sourceOptional == nil {
			return false, false
		}
		return a.cloneBuiltinCompatible(tt.Value, sourceOptional.Value, seen)
	case *ErrorUnionType:
		sourceUnion, ok := source.(*ErrorUnionType)
		if !ok || sourceUnion == nil || !SameType(tt.Errors, sourceUnion.Errors) {
			return false, false
		}
		return a.cloneBuiltinCompatible(tt.Value, sourceUnion.Value, seen)
	case *TupleType, *StructType, *GenericInstanceType:
		if !SameType(target, source) {
			return false, false
		}
		fields, ok := a.resolvedStructFields(target)
		if !ok {
			return false, false
		}
		needsOwner := false
		for _, field := range fields {
			fieldNeedsOwner, fieldOK := a.cloneBuiltinCompatible(field.Type, field.Type, seen)
			if !fieldOK {
				return false, false
			}
			needsOwner = needsOwner || fieldNeedsOwner
		}
		return needsOwner, true
	default:
		return false, false
	}
}
func (a *Analyzer) analyzeCloneBuiltinCall(expr *ast.CallExpr) (Type, bool) {
	if expr == nil || callSpecializedIdentName(expr) != "clone" {
		return nil, false
	}
	targetType, ok := a.cloneBuiltinTargetType(expr)
	if !ok {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "clone expects 1 argument, got %d", len(expr.Args))
		return invalidType, true
	}
	sourceType := a.analyzeExpr(expr.Args[0])
	if targetType == nil || IsInvalidType(targetType) || IsInvalidType(sourceType) {
		return invalidType, true
	}
	needsOwner, compatible := a.cloneBuiltinCompatible(targetType, sourceType, map[string]bool{})
	if !compatible {
		a.errorf(expr.Pos(), "clone cannot clone %s into %s in v1", sourceType, targetType)
		return invalidType, true
	}
	if needsOwner && a.currentTreeAllocOwner.Kind == treeAllocOwnerNone && !a.regionAvailableForContainer(targetType) {
		a.errorf(expr.Pos(), "clone of %q requires an active in <owner>: scope", targetType.String())
		return invalidType, true
	}
	a.recordBuiltinHelperFuncType(expr, "clone", targetType)
	return targetType, true
}
func (a *Analyzer) analyzeDenseKeyHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 2 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "dense_key expects 2 arguments, got %d", len(expr.Args))
		return invalidType
	}
	nodeType := a.analyzeExpr(expr.Args[0])
	frozenType := a.analyzeExpr(expr.Args[1])
	enumType, ok := denseKeySourceEnumType(nodeType)
	if !ok || enumType == nil {
		a.errorf(expr.Args[0].Pos(), "dense_key expects a packed enum value or packedview, got %s", nodeType)
		return invalidType
	}
	frozenRoot, frozenPath, frozenStoreType, ok := a.resolveFrozenPackedStoreRootPath(expr.Args[1])
	if !ok || frozenStoreType == nil || frozenStoreType.Enum == nil {
		a.errorf(expr.Args[1].Pos(), "dense_key requires an exact frozen packed-store root")
		return invalidType
	}
	if _, ok := frozenType.(*PackedEnumStoreType); !ok {
		a.errorf(expr.Args[1].Pos(), "dense_key expects a frozen packed enum store, got %s", frozenType)
		return invalidType
	}
	if frozenStoreType.Enum != enumType {
		a.errorf(expr.Args[1].Pos(), "dense_key source enum %q does not match frozen store %q", enumType.Name, frozenStoreType.Enum.Name)
		return invalidType
	}
	nodeRoot, nodePath, ok := a.resolvePackedNodeStoreRoot(expr.Args[0], enumType)
	if !ok || nodeRoot == nil {
		a.errorf(expr.Args[0].Pos(), "dense_key requires a packed enum value or packedview proven to come from the exact frozen store root")
		return invalidType
	}
	if !samePackedStoreRootPath(nodeRoot, nodePath, frozenRoot, frozenPath) {
		a.errorf(expr.Pos(), "dense_key source and frozen store must share the same exact frozen store root")
		return invalidType
	}
	result := a.nodeKeyType(enumType)
	a.exprDenseNodeKeys[expr] = DenseNodeKeyInfo{Enum: enumType, StoreRoot: frozenRoot, StorePath: frozenPath}
	a.recordBuiltinHelperFuncType(expr, "dense_key", result)
	return result
}
func (a *Analyzer) analyzeNodeTableFillHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 3 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "node_table_fill expects 3 arguments, got %d", len(expr.Args))
		return invalidType
	}
	enumType, elemType, ok := a.nodeTableFillTypeArgs(expr)
	arenaType := a.analyzeExpr(expr.Args[0])
	frozenType := a.analyzeExpr(expr.Args[1])
	if !ok || enumType == nil || elemType == nil {
		if callSpecializedIdentName(expr) != "node_table_fill" {
			a.errorf(expr.Pos(), "node_table_fill expects explicit specialization like node_table_fill[Expr, T](...) in v1")
		} else {
			a.errorf(expr.Pos(), "node_table_fill expects packed enum and element type arguments")
		}
		_ = a.analyzeExpr(expr.Args[2])
		return invalidType
	}
	if !isArenaValueOrRefType(arenaType) {
		a.errorf(expr.Args[0].Pos(), "node_table_fill expects an Arena or proven non-null Arena reference, got %s", arenaType)
	}
	frozenRoot, frozenPath, frozenStoreType, rootOK := a.resolveFrozenPackedStoreRootPath(expr.Args[1])
	if !rootOK || frozenStoreType == nil || frozenStoreType.Enum == nil {
		a.errorf(expr.Args[1].Pos(), "node_table_fill requires an exact frozen packed-store root")
		_ = a.analyzeValueExpr(expr.Args[2], elemType)
		return invalidType
	}
	if _, ok := frozenType.(*PackedEnumStoreType); !ok {
		a.errorf(expr.Args[1].Pos(), "node_table_fill expects a frozen packed enum store, got %s", frozenType)
		_ = a.analyzeValueExpr(expr.Args[2], elemType)
		return invalidType
	}
	if frozenStoreType.Enum != enumType {
		a.errorf(expr.Args[1].Pos(), "node_table_fill enum %q does not match frozen store %q", enumType.Name, frozenStoreType.Enum.Name)
		_ = a.analyzeValueExpr(expr.Args[2], elemType)
		return invalidType
	}
	initType := a.analyzeValueExpr(expr.Args[2], elemType)
	if !AssignableTo(elemType, initType) {
		a.errorf(expr.Args[2].Pos(), "node_table_fill initializer expects %s, got %s", elemType, initType)
	}
	result := a.nodeTableType(enumType, elemType)
	a.exprNodeTables[expr] = NodeTableInfo{
		Enum:      enumType,
		Elem:      elemType,
		StoreRoot: frozenRoot,
		StorePath: frozenPath,
		CountExpr: optimizationExprString(&ast.FieldExpr{Position: expr.Args[1].Pos(), Object: expr.Args[1], Field: "count"}),
	}
	a.recordBuiltinHelperFuncType(expr, "node_table_fill", result)
	return result
}
func (a *Analyzer) analyzePackedNodeHelperCall(expr *ast.CallExpr) (Type, bool) {
	switch {
	case callIdentName(expr) == "dense_key":
		return a.analyzeDenseKeyHelperCall(expr), true
	case callSpecializedIdentName(expr) == "node_table_fill":
		return a.analyzeNodeTableFillHelperCall(expr), true
	default:
		return nil, false
	}
}
