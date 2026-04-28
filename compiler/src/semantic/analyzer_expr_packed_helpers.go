package semantic

import (
	"llcontext/src/ast"
)

func callIdentName(expr *ast.CallExpr) string {
	if expr == nil {
		return ""
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

func callSpecializedIdent(expr ast.Expr) (*ast.Ident, *ast.SpecializeExpr, bool) {
	if expr == nil {
		return nil, nil, false
	}
	specialize, ok := expr.(*ast.SpecializeExpr)
	if !ok || specialize == nil {
		return nil, nil, false
	}
	ident, ok := specialize.Operand.(*ast.Ident)
	if !ok || ident == nil {
		return nil, nil, false
	}
	return ident, specialize, true
}

func callSpecializedIdentName(expr *ast.CallExpr) string {
	if expr == nil {
		return ""
	}
	if ident, _, ok := callSpecializedIdent(expr.Func); ok {
		return ident.Name
	}
	return ""
}

func (a *Analyzer) recordBuiltinHelperFuncType(expr *ast.CallExpr, name string, returnType Type) {
	if a == nil || expr == nil || expr.Func == nil || name == "" || returnType == nil {
		return
	}
	params := make([]Type, 0, len(expr.Args))
	for _, arg := range expr.Args {
		argType := a.exprTypes[arg]
		if argType == nil {
			argType = invalidType
		}
		params = append(params, argType)
	}
	a.exprTypes[expr.Func] = &FuncType{Name: name, Params: params, Return: returnType}
}

func (a *Analyzer) freezeStoreArg(expr *ast.CallExpr) (ast.Expr, bool) {
	if callIdentName(expr) != "freeze" || len(expr.Args) != 1 {
		return nil, false
	}
	return expr.Args[0], true
}

func (a *Analyzer) analyzeFreezeCallExpr(expr *ast.CallExpr) Type {
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "freeze expects 1 argument, got %d", len(expr.Args))
		return invalidType
	}
	storeType := a.analyzeExpr(expr.Args[0])
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(expr.Args[0].Pos(), "freeze expects a packed enum store, got %s", storeType)
		return invalidType
	}
	if !IsLocalPackedEnumStoreType(packedStore) {
		a.errorf(expr.Args[0].Pos(), "freeze expects local store type %q, got %s", PackedEnumStoreWithState(packedStore, a.namedTypes["Local"]), packedStore)
		return invalidType
	}
	if _, ok := explicitMoveOperand(expr.Args[0]); !ok {
		a.errorf(expr.Args[0].Pos(), "local packed enum store %q must be moved explicitly before freeze", affineValueDisplayName(expr.Args[0]))
		return invalidType
	}
	return PackedEnumStoreWithState(packedStore, a.namedTypes["Frozen"])
}

func (a *Analyzer) nodeKeyType(enumType *EnumType) Type {
	if a == nil || enumType == nil {
		return invalidType
	}
	base, ok := a.namedTypes["NodeKey"]
	if !ok {
		return invalidType
	}
	return &GenericInstanceType{Name: "NodeKey", Base: base, Args: []Type{enumType}}
}

func (a *Analyzer) nodeTableType(enumType *EnumType, elemType Type) Type {
	if a == nil || enumType == nil || elemType == nil {
		return invalidType
	}
	base, ok := a.namedTypes["NodeTable"]
	if !ok {
		return invalidType
	}
	return &GenericInstanceType{Name: "NodeTable", Base: base, Args: []Type{enumType, elemType}}
}

func denseKeySourceEnumType(t Type) (*EnumType, bool) {
	if t == nil {
		return nil, false
	}
	t = StripAggregateStateType(t)
	if enumType, ok := t.(*EnumType); ok && enumType != nil && enumType.Packed {
		return enumType, true
	}
	if viewType, ok := t.(*PackedVariantViewType); ok && viewType != nil && viewType.Enum != nil {
		return viewType.Enum, true
	}
	return nil, false
}

func isArenaValueOrRefType(t Type) bool {
	if t == nil {
		return false
	}
	t = StripAggregateStateType(t)
	if structType, ok := t.(*StructType); ok {
		return structType != nil && structType.Name == "Arena"
	}
	refType, ok := t.(*RefType)
	if !ok || refType.State != RefStateNonNull {
		return false
	}
	structType, ok := StripAggregateStateType(refType.Elem).(*StructType)
	return ok && structType != nil && structType.Name == "Arena"
}

func (a *Analyzer) resolveFrozenPackedStoreRoot(expr ast.Expr) (*Symbol, *PackedEnumStoreType, bool) {
	root, _, storeType, ok := a.resolveFrozenPackedStoreRootPathWithSeen(expr, map[*Symbol]bool{})
	return root, storeType, ok
}

func (a *Analyzer) resolvePackedStoreRoot(expr ast.Expr) (*Symbol, *PackedEnumStoreType, bool) {
	root, _, storeType, ok := a.resolvePackedStoreRootPathWithSeen(expr, map[*Symbol]bool{})
	return root, storeType, ok
}

func (a *Analyzer) resolvePackedStoreRootPath(expr ast.Expr) (*Symbol, string, *PackedEnumStoreType, bool) {
	return a.resolvePackedStoreRootPathWithSeen(expr, map[*Symbol]bool{})
}

func appendPackedStoreRootPath(base string, field string) string {
	if field == "" {
		return base
	}
	if base == "" {
		return field
	}
	return base + "." + field
}

func samePackedStoreRootPath(leftRoot *Symbol, leftPath string, rightRoot *Symbol, rightPath string) bool {
	return leftRoot != nil && rightRoot != nil && leftRoot == rightRoot && leftPath == rightPath
}

func (a *Analyzer) resolvePackedStoreRootPathWithSeen(expr ast.Expr, seen map[*Symbol]bool) (*Symbol, string, *PackedEnumStoreType, bool) {
	if a == nil || expr == nil {
		return nil, "", nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.resolvePackedStoreRootPathWithSeen(n.Inner, seen)
	case *ast.CastExpr:
		return a.resolvePackedStoreRootPathWithSeen(n.Operand, seen)
	case *ast.MoveExpr:
		return a.resolvePackedStoreRootPathWithSeen(n.Operand, seen)
	case *ast.FieldExpr:
		if actual := a.exprTypes[expr]; actual != nil {
			if storeType, ok := StripAggregateStateType(actual).(*PackedEnumStoreType); ok && storeType != nil {
				if resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok && resolved != nil {
					return a.resolvePackedStoreRootPathWithSeen(resolved, seen)
				}
				root, path, ok := a.resolveValueRootPathWithSeen(expr, seen)
				if !ok || root == nil {
					return nil, "", nil, false
				}
				return root, path, storeType, true
			}
		}
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, "", nil, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return nil, "", nil, false
		}
		if seen[sym] {
			return nil, "", nil, false
		}
		seen[sym] = true
		if a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				if root, path, storeType, ok := a.resolvePackedStoreRootPathWithSeen(valueExpr, seen); ok {
					return root, path, storeType, true
				}
			}
		}
		root := symbolAliasRoot(sym)
		if root == nil {
			root = sym
		}
		if storeType, ok := root.Type.(*PackedEnumStoreType); ok && storeType != nil {
			if root.Kind == SymbolLocal || root.Kind == SymbolParam {
				return root, "", storeType, true
			}
		}
		decl, ok := root.Node.(*ast.VarDeclStmt)
		if ok && decl != nil && decl.Value != nil {
			return a.resolvePackedStoreRootPathWithSeen(decl.Value, seen)
		}
	}
	return nil, "", nil, false
}

func (a *Analyzer) resolvePackedStoreRootWithSeen(expr ast.Expr, seen map[*Symbol]bool) (*Symbol, *PackedEnumStoreType, bool) {
	root, _, storeType, ok := a.resolvePackedStoreRootPathWithSeen(expr, seen)
	return root, storeType, ok
}

func (a *Analyzer) resolveFrozenPackedStoreRootPath(expr ast.Expr) (*Symbol, string, *PackedEnumStoreType, bool) {
	return a.resolveFrozenPackedStoreRootPathWithSeen(expr, map[*Symbol]bool{})
}

func (a *Analyzer) resolveFrozenPackedStoreRootPathWithSeen(expr ast.Expr, seen map[*Symbol]bool) (*Symbol, string, *PackedEnumStoreType, bool) {
	root, path, storeType, ok := a.resolvePackedStoreRootPathWithSeen(expr, seen)
	if !ok || storeType == nil || !IsFrozenPackedEnumStoreType(storeType) {
		return nil, "", nil, false
	}
	return root, path, storeType, true
}

func (a *Analyzer) resolveFrozenPackedStoreRootWithSeen(expr ast.Expr, seen map[*Symbol]bool) (*Symbol, *PackedEnumStoreType, bool) {
	root, _, storeType, ok := a.resolveFrozenPackedStoreRootPathWithSeen(expr, seen)
	return root, storeType, ok
}

func (a *Analyzer) resolvePackedNodeStoreRoot(expr ast.Expr, enumType *EnumType) (*Symbol, string, bool) {
	return a.resolvePackedNodeStoreRootWithSeen(expr, enumType, map[*Symbol]bool{})
}

func (a *Analyzer) resolvePackedNodeStoreRootWithSeen(expr ast.Expr, enumType *EnumType, seen map[*Symbol]bool) (*Symbol, string, bool) {
	if a == nil || expr == nil || enumType == nil {
		return nil, "", false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.resolvePackedNodeStoreRootWithSeen(n.Inner, enumType, seen)
	case *ast.CastExpr:
		return a.resolvePackedNodeStoreRootWithSeen(n.Operand, enumType, seen)
	case *ast.MoveExpr:
		return a.resolvePackedNodeStoreRootWithSeen(n.Operand, enumType, seen)
	case *ast.CanExpr:
		return a.resolvePackedNodeStoreRootWithSeen(n.Expr, enumType, seen)
	case *ast.FieldExpr:
		if resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok && resolved != nil {
			return a.resolvePackedNodeStoreRootWithSeen(resolved, enumType, seen)
		}
		return nil, "", false
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, "", false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return nil, "", false
		}
		if seen[sym] {
			return nil, "", false
		}
		seen[sym] = true
		if a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				return a.resolvePackedNodeStoreRootWithSeen(valueExpr, enumType, seen)
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
					return a.resolvePackedNodeStoreRootWithSeen(valueExpr, enumType, seen)
				}
			}
		}
		declSym := sym
		if root := symbolAliasRoot(sym); root != nil {
			declSym = root
		}
		decl, ok := declSym.Node.(*ast.VarDeclStmt)
		if ok && decl != nil && decl.Value != nil {
			return a.resolvePackedNodeStoreRootWithSeen(decl.Value, enumType, seen)
		}
	case *ast.IndexExpr:
		if n.Fallback != nil {
			break
		}
		if root, path, storeType, ok := a.resolvePackedStoreRootPathWithSeen(n.Object, seen); ok {
			if storeType.Enum == enumType {
				return root, path, true
			}
		}
	}
	return nil, "", false
}

func (a *Analyzer) resolveValueRootPath(expr ast.Expr) (*Symbol, string, bool) {
	return a.resolveValueRootPathWithSeen(expr, map[*Symbol]bool{})
}

func (a *Analyzer) resolveValueRootPathWithSeen(expr ast.Expr, seen map[*Symbol]bool) (*Symbol, string, bool) {
	if a == nil || expr == nil {
		return nil, "", false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.resolveValueRootPathWithSeen(n.Inner, seen)
	case *ast.CastExpr:
		return a.resolveValueRootPathWithSeen(n.Operand, seen)
	case *ast.MoveExpr:
		return a.resolveValueRootPathWithSeen(n.Operand, seen)
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, "", false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return nil, "", false
		}
		if seen[sym] {
			return nil, "", false
		}
		seen[sym] = true
		if a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				if root, path, ok := a.resolveValueRootPathWithSeen(valueExpr, seen); ok {
					return root, path, true
				}
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
					if resolvedRoot, path, ok := a.resolveValueRootPathWithSeen(valueExpr, seen); ok {
						return resolvedRoot, path, true
					}
				}
			}
		}
		root := symbolAliasRoot(sym)
		if root == nil {
			root = sym
		}
		if root.Kind != SymbolLocal && root.Kind != SymbolParam {
			return nil, "", false
		}
		if root.Mutable {
			return nil, "", false
		}
		return root, "", true
	case *ast.FieldExpr:
		if resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok && resolved != nil {
			return a.resolveValueRootPathWithSeen(resolved, seen)
		}
		root, path, ok := a.resolveValueRootPathWithSeen(n.Object, seen)
		if !ok || root == nil {
			return nil, "", false
		}
		return root, appendPackedStoreRootPath(path, n.Field), true
	default:
		return nil, "", false
	}
}

func (a *Analyzer) denseNodeKeyInfoForExpr(expr ast.Expr) (DenseNodeKeyInfo, bool) {
	return a.denseNodeKeyInfoForExprWithSeen(expr, map[*Symbol]bool{})
}

func (a *Analyzer) denseNodeKeyInfoForExprWithSeen(expr ast.Expr, seen map[*Symbol]bool) (DenseNodeKeyInfo, bool) {
	if a == nil || expr == nil {
		return DenseNodeKeyInfo{}, false
	}
	if info, ok := a.exprDenseNodeKeys[expr]; ok {
		return info, true
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.denseNodeKeyInfoForExprWithSeen(n.Inner, seen)
	case *ast.CastExpr:
		return a.denseNodeKeyInfoForExprWithSeen(n.Operand, seen)
	case *ast.MoveExpr:
		return a.denseNodeKeyInfoForExprWithSeen(n.Operand, seen)
	case *ast.Ident:
		if a.currentScope == nil {
			return DenseNodeKeyInfo{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return DenseNodeKeyInfo{}, false
		}
		if seen[sym] {
			return DenseNodeKeyInfo{}, false
		}
		seen[sym] = true
		if a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				return a.denseNodeKeyInfoForExprWithSeen(valueExpr, seen)
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
					return a.denseNodeKeyInfoForExprWithSeen(valueExpr, seen)
				}
			}
		}
		declSym := sym
		if root := symbolAliasRoot(sym); root != nil {
			declSym = root
		}
		decl, ok := declSym.Node.(*ast.VarDeclStmt)
		if !ok || decl == nil || decl.Value == nil {
			return DenseNodeKeyInfo{}, false
		}
		return a.denseNodeKeyInfoForExprWithSeen(decl.Value, seen)
	default:
		return DenseNodeKeyInfo{}, false
	}
}

func (a *Analyzer) nodeTableInfoForExpr(expr ast.Expr) (NodeTableInfo, bool) {
	return a.nodeTableInfoForExprWithSeen(expr, map[*Symbol]bool{})
}

func (a *Analyzer) nodeTableInfoForExprWithSeen(expr ast.Expr, seen map[*Symbol]bool) (NodeTableInfo, bool) {
	if a == nil || expr == nil {
		return NodeTableInfo{}, false
	}
	if info, ok := a.exprNodeTables[expr]; ok {
		return info, true
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.nodeTableInfoForExprWithSeen(n.Inner, seen)
	case *ast.CastExpr:
		return a.nodeTableInfoForExprWithSeen(n.Operand, seen)
	case *ast.MoveExpr:
		return a.nodeTableInfoForExprWithSeen(n.Operand, seen)
	case *ast.Ident:
		if a.currentScope == nil {
			return NodeTableInfo{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return NodeTableInfo{}, false
		}
		if seen[sym] {
			return NodeTableInfo{}, false
		}
		seen[sym] = true
		if a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				return a.nodeTableInfoForExprWithSeen(valueExpr, seen)
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
					return a.nodeTableInfoForExprWithSeen(valueExpr, seen)
				}
			}
		}
		declSym := sym
		if root := symbolAliasRoot(sym); root != nil {
			declSym = root
		}
		decl, ok := declSym.Node.(*ast.VarDeclStmt)
		if !ok || decl == nil || decl.Value == nil {
			return NodeTableInfo{}, false
		}
		return a.nodeTableInfoForExprWithSeen(decl.Value, seen)
	default:
		return NodeTableInfo{}, false
	}
}

func (a *Analyzer) nodeTableFillTypeArgs(expr *ast.CallExpr) (*EnumType, Type, bool) {
	if a == nil || expr == nil || callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, false
	}
	_, specialize, ok := callSpecializedIdent(expr.Func)
	if !ok || specialize == nil || len(specialize.TypeArgs) != 2 {
		return nil, nil, false
	}
	enumArg := a.resolveType(specialize.TypeArgs[0])
	enumType, ok := StripAggregateStateType(enumArg).(*EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, false
	}
	elemType := a.resolveType(specialize.TypeArgs[1])
	if elemType == nil || IsInvalidType(elemType) {
		return nil, nil, false
	}
	return enumType, elemType, true
}

func (a *Analyzer) cloneBuiltinTargetType(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil || callSpecializedIdentName(expr) != "clone" {
		return nil, false
	}
	_, specialize, ok := callSpecializedIdent(expr.Func)
	if !ok || specialize == nil || len(specialize.TypeArgs) != 1 {
		return nil, false
	}
	targetType := a.resolveType(specialize.TypeArgs[0])
	if targetType == nil || IsInvalidType(targetType) {
		return invalidType, true
	}
	return targetType, true
}

func cloneBuiltinTreeTargetCompatible(target Type, source Type) bool {
	target = StripAggregateStateType(target)
	sourceMember, sourceFamily, ok := resolveTreeVisitSourceType(source)
	if !ok || sourceFamily == nil {
		return false
	}
	switch tt := target.(type) {
	case *TreeNodeType:
		return tt != nil && tt.Family == sourceFamily
	case *TreeCategoryType:
		category, _, ok := resolveMatchableTreeCategoryType(source)
		return ok && category == tt
	case *TreeBlockType:
		sourceBlock, ok := sourceMember.(*TreeBlockType)
		return ok && SameType(sourceBlock, tt)
	case *TreeStructType:
		sourceStruct, ok := sourceMember.(*TreeStructType)
		return ok && SameType(sourceStruct, tt)
	default:
		return false
	}
}

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
		return false, SameType(target, source)
	case *TypeParamType, *RefType, *FuncType, *ViewType, *DArrayViewType, *PackedVariantViewType, *StoreRowsViewType, *StoreRowViewType, *DictType, *DictEntryType:
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
		case *DArrayViewType:
			sourceElem = ss.Elem
		case *ArrayType:
			sourceElem = ss.Elem
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
	if needsOwner && a.currentTreeAllocOwner.Kind == treeAllocOwnerNone {
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
