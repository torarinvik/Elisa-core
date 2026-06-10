package semantic

import "elisacore/src/ast"

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
	if ok {
		// docs/82: pointer handles are not position-independent, so a ptr-handle store can
		// never graduate to the frozen (publishable/serializable) state.
		if packedStore.Enum != nil && packedStore.Enum.HandleIsPointer() {
			a.errorf(expr.Args[0].Pos(), "cannot freeze store of enum %q: pointer handles are not position-independent; use an index handle (`layout(handle: u32)`)", packedStore.Enum.Name)
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
	a.errorf(expr.Args[0].Pos(), "freeze expects a packed enum store, got %s", storeType)
	return invalidType
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
func IsArenaValueOrRefType(t Type) bool {
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

func isArenaValueOrRefType(t Type) bool {
	return IsArenaValueOrRefType(t)
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
