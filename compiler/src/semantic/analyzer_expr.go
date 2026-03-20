package semantic

import (
	"strconv"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) analyzeExpr(expr ast.Expr) (result Type) {
	defer func() {
		if expr != nil {
			a.exprTypes[expr] = result
		}
	}()
	switch n := expr.(type) {
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				if sym.Kind == SymbolRegionMark {
					a.errorf(n.Pos(), "checkpoint %q can only be used in restore <region> from %q", n.Name, n.Name)
					result = sym.Type
					return
				}
				if refState, ok := a.currentRegionRefs[sym]; ok && !refState.Valid {
					a.errorf(n.Pos(), "reference %q is invalid after %s", n.Name, refState.InvalidatedBy)
					result = sym.Type
					return
				}
				if state, ok := a.lookupAffineValueState(n); ok && a.containsAffineHandleValues(sym.Type, map[string]bool{}) {
					a.errorf(n.Pos(), "%s %q cannot be used after %s", affineHandleKind(sym.Type), n.Name, state.ConsumedBy)
					result = sym.Type
					return
				}
				if t, ok := a.lookupRefinedExprType(n); ok {
					result = t
					return
				}
				result = sym.Type
				return
			}
		}
		if t, ok := a.lookupRefinedExprType(n); ok {
			result = t
			return
		}
		if sym, ok := a.globalScope.Lookup(n.Name); ok {
			result = sym.Type
			return
		}
		a.errorf(n.Pos(), "undefined identifier %q", n.Name)
		result = invalidType
		return
	case *ast.IntLit:
		if n.Suffix != "" {
			if t, ok := a.namedTypes[n.Suffix]; ok {
				result = t
				return
			}
			switch n.Suffix {
			case "u":
				result = a.namedTypes["usize"]
				return
			case "i":
				result = a.namedTypes["int"]
				return
			}
		}
		result = a.namedTypes["int"]
		return
	case *ast.StringLit:
		result = &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
		return
	case *ast.BoolLit:
		result = a.namedTypes["bool"]
		return
	case *ast.NullLit:
		result = nullType
		return
	case *ast.ZeroedLit:
		result = invalidType
		return
	case *ast.ListLitExpr:
		result = a.analyzeListLitExprWithExpected(n, nil)
		return
	case *ast.BinaryExpr:
		result = a.analyzeBinaryExpr(n)
		return
	case *ast.UnaryExpr:
		result = a.analyzeUnaryExpr(n)
		return
	case *ast.MoveExpr:
		result = a.analyzeMoveExpr(n)
		return
	case *ast.CallExpr:
		result = a.analyzeCallExpr(n)
		return
	case *ast.FieldExpr:
		if errorType, ok := a.errorTagType(n); ok {
			result = errorType
			return
		}
		if storeType, ctorType, ok := a.packedStoreExprType(n); ok {
			if ctorType != nil {
				result = ctorType
			} else {
				result = storeType
			}
			return
		}
		if enumType, ctorType, ok := a.enumVariantExprType(n); ok {
			if ctorType != nil {
				result = ctorType
			} else {
				result = enumType
			}
			return
		}
		if t, ok := a.lookupRefinedExprType(n); ok {
			result = t
			return
		}
		result = a.analyzeFieldExpr(n)
		return
	case *ast.RaiseExpr:
		errorType := a.analyzeExpr(n.Error)
		currentUnion, ok := a.currentReturn.(*ErrorUnionType)
		if !ok {
			a.errorf(n.Pos(), "raise requires the current function to return an error union")
			result = neverType
			return
		}
		if qualifiedTag, ok := a.errorExprTagName(n.Error); ok {
			if _, ok := MatchErrorTag(currentUnion.Errors, qualifiedTag); !ok {
				a.errorf(n.Pos(), "raise cannot propagate tag %q into %s", ErrorTagDiagnosticName(qualifiedTag), ErrorSetDiagnosticName(currentUnion.Errors))
			}
			result = neverType
			return
		}
		if errSet, ok := errorType.(*ErrorSetType); !ok || !ErrorSetAssignable(currentUnion.Errors, errSet) {
			a.errorf(n.Pos(), "raise expects %s, got %s", ErrorSetDiagnosticName(currentUnion.Errors), ErrorTypeDiagnosticName(errorType))
		}
		result = neverType
		return
	case *ast.TryExpr:
		valueType := a.analyzeExpr(n.Value)
		unionType, ok := valueType.(*ErrorUnionType)
		if !ok {
			a.errorf(n.Pos(), "try requires a fallible expression, got %s", valueType.String())
			result = invalidType
			return
		}
		if n.Fallback == nil {
			currentUnion, ok := a.currentReturn.(*ErrorUnionType)
			if !ok {
				a.errorf(n.Pos(), "try without else requires the current function to return an error union")
			} else if !ErrorSetAssignable(currentUnion.Errors, unionType.Errors) {
				a.errorf(n.Pos(), "cannot propagate %s from a function returning %s", ErrorSetDiagnosticName(unionType.Errors), ErrorSetDiagnosticName(currentUnion.Errors))
			}
			result = unionType.Value
			return
		}
		fallbackType := a.analyzeExpr(n.Fallback)
		if !IsNeverType(fallbackType) && !AssignableTo(unionType.Value, fallbackType) {
			a.errorf(n.Pos(), "try fallback expects %s, got %s", unionType.Value.String(), fallbackType.String())
			a.reportShapeMismatchNotes(n.Pos(), unionType.Value, fallbackType)
		}
		result = unionType.Value
		return
	case *ast.UnwrapElseExpr:
		valueType := a.analyzeExpr(n.Value)
		refType, ok := valueType.(*RefType)
		if !ok || refType.State == RefStateNonNull {
			a.errorf(n.Pos(), "else recovery requires a nullable reference, got %s", valueType.String())
			result = invalidType
			return
		}
		resultType := cloneRefTypeWithState(refType, RefStateNonNull)
		fallbackType := a.analyzeExpr(n.Fallback)
		if !IsNeverType(fallbackType) && !AssignableTo(resultType, fallbackType) {
			a.errorf(n.Pos(), "else fallback expects %s, got %s", resultType.String(), fallbackType.String())
			a.reportShapeMismatchNotes(n.Pos(), resultType, fallbackType)
		}
		result = resultType
		return
	case *ast.AllocExpr:
		result = a.analyzeAllocExpr(n)
		return
	case *ast.CanExpr:
		result = a.analyzeCanExpr(n)
		return
	case *ast.MatchExpr:
		result = a.analyzeMatchExpr(n)
		return
	case *ast.IndexExpr:
		result = a.analyzeIndexExpr(n)
		return
	case *ast.SliceExpr:
		result = a.analyzeSliceExpr(n)
		return
	case *ast.CastExpr:
		src := a.analyzeExpr(n.Operand)
		dst := a.resolveType(n.Target)
		if !a.validCast(src, dst) {
			a.errorf(n.Pos(), "invalid cast from %s to %s", src.String(), dst.String())
		}
		result = dst
		return
	case *ast.SizeofExpr:
		a.resolveType(n.Type)
		result = a.namedTypes["usize"]
		return
	case *ast.TernaryExpr:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "ternary condition must be bool, got %s", condType.String())
		}
		mergedAffine := a.cloneAffineValueStates()
		left, leftAffine := a.analyzeExprInAffineScope(n.Value, a.refinedScopeForCondition(a.currentScope, n.Cond, true))
		right, rightAffine := a.analyzeExprInAffineScope(n.Alt, a.refinedScopeForCondition(a.currentScope, n.Cond, false))
		mergedAffine = mergeAffineValueStates(mergedAffine, leftAffine)
		mergedAffine = mergeAffineValueStates(mergedAffine, rightAffine)
		a.currentAffineValues = mergedAffine
		merged := MergeTypes(left, right)
		if IsInvalidType(merged) {
			a.errorf(n.Pos(), "ternary branches are incompatible: %s and %s", left.String(), right.String())
		}
		result = merged
		return
	case *ast.AddrOfExpr:
		inner := a.analyzeExpr(n.Operand)
		if a.containsAffineHandleValues(inner, map[string]bool{}) {
			if _, ok := a.lookupAffineValueKey(n.Operand); ok {
				if isAffineHandleType(inner) {
					a.errorf(n.Pos(), "cannot take address of affine %s", affineHandleKind(inner))
				} else {
					a.errorf(n.Pos(), "cannot take address of value containing affine handles")
				}
			}
		}
		result = &RefType{Elem: inner, State: RefStateNonNull, Storage: a.inferAddrOfStorage(n.Operand), ExplicitStorage: true}
		return
	case *ast.SpecializeExpr:
		result = a.analyzeSpecializeExpr(n)
		return
	case *ast.StructLitExpr:
		if t, ok := a.namedTypes[n.Name]; ok {
			switch tt := t.(type) {
			case *StructType:
				a.analyzeStructLiteralArgs(n, tt, nil)
				result = tt
				return
			case *GenericInstanceType:
				if _, ok := tt.Base.(*StructType); ok {
					base := tt.Base.(*StructType)
					bindings := map[string]Type{}
					for i, name := range base.TypeParams {
						if i < len(tt.Args) {
							bindings[name] = tt.Args[i]
						}
					}
					a.analyzeStructLiteralArgs(n, base, bindings)
					result = tt
					return
				}
			}
		}
		a.errorf(n.Pos(), "unknown struct %q", n.Name)
		result = invalidType
		return
	case *ast.ParenExpr:
		result = a.analyzeExpr(n.Inner)
		return
	default:
		result = invalidType
		return
	}
}

func (a *Analyzer) analyzeSpecializeExpr(expr *ast.SpecializeExpr) Type {
	if expr == nil || expr.Operand == nil {
		return invalidType
	}
	ident, ok := expr.Operand.(*ast.Ident)
	if !ok {
		a.errorf(expr.Pos(), "specialize expects a named generic function")
		a.analyzeExpr(expr.Operand)
		for _, arg := range expr.TypeArgs {
			a.resolveType(arg)
		}
		return invalidType
	}
	sym, ok := a.globalScope.Lookup(ident.Name)
	if !ok {
		a.errorf(expr.Pos(), "undefined generic function %q", ident.Name)
		for _, arg := range expr.TypeArgs {
			a.resolveType(arg)
		}
		return invalidType
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		a.errorf(expr.Pos(), "specialize expects a function, got %s", sym.Type.String())
		for _, arg := range expr.TypeArgs {
			a.resolveType(arg)
		}
		return invalidType
	}
	if len(fnType.TypeParams) == 0 {
		a.errorf(expr.Pos(), "function %q is not generic", ident.Name)
		for _, arg := range expr.TypeArgs {
			a.resolveType(arg)
		}
		return invalidType
	}
	if len(expr.TypeArgs) != len(fnType.TypeParams) {
		a.errorf(expr.Pos(), "function %q expects %d type arguments, got %d", ident.Name, len(fnType.TypeParams), len(expr.TypeArgs))
	}
	bindings := make(map[string]Type, len(fnType.TypeParams))
	limit := len(expr.TypeArgs)
	if len(fnType.TypeParams) < limit {
		limit = len(fnType.TypeParams)
	}
	for i := 0; i < limit; i++ {
		bindings[fnType.TypeParams[i]] = a.resolveType(expr.TypeArgs[i])
	}
	for i := limit; i < len(expr.TypeArgs); i++ {
		a.resolveType(expr.TypeArgs[i])
	}
	specialized, _ := a.substituteType(fnType, bindings, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return invalidType
	}
	specialized.TypeParams = nil
	return specialized
}

func (a *Analyzer) analyzeCanExpr(expr *ast.CanExpr) Type {
	if expr == nil {
		return invalidType
	}
	refs := a.resolvePermissionRefs(expr.Permissions, true)
	a.recordFunctionPermissionRefs(refs)
	return a.analyzeExpr(expr.Expr)
}

func (a *Analyzer) analyzeAllocExpr(expr *ast.AllocExpr) Type {
	if expr == nil {
		return invalidType
	}
	if expr.Owner == nil {
		return a.analyzeScopedPackedAllocExpr(expr)
	}
	ownerType := a.analyzeExpr(expr.Owner)
	if storeType, ok := ownerType.(*PackedEnumStoreType); ok {
		return a.analyzePackedAllocExpr(expr, storeType)
	}
	ident, ok := expr.Owner.(*ast.Ident)
	if !ok {
		a.errorf(expr.Pos(), "new[...] owner must be a region name or packed enum store, got %s", ownerType.String())
		a.analyzeExpr(expr.Value)
		return invalidType
	}
	_, state := a.lookupRegionState(ident.Name)
	if a.currentScope == nil {
		a.errorf(expr.Pos(), "region allocation requires function scope")
		return invalidType
	}
	if sym, ok := a.currentScope.Lookup(ident.Name); !ok || sym.Kind != SymbolRegion {
		a.errorf(expr.Pos(), "new[...] owner must be a region name or packed enum store, got %s", ownerType.String())
		a.analyzeExpr(expr.Value)
		return invalidType
	}
	if state.Destroyed {
		a.errorf(expr.Pos(), "cannot allocate from destroyed region %q", ident.Name)
		return invalidType
	}
	valueType := a.analyzeExpr(expr.Value)
	return &RefType{Elem: valueType, State: RefStateNonNull, Storage: RefStorageAny, Region: ident.Name, ExplicitStorage: true}
}

func (a *Analyzer) regionRefStateForExpr(expr ast.Expr) (regionRefState, bool) {
	if expr == nil {
		return regionRefState{}, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.regionRefStateForExpr(n.Inner)
	case *ast.CastExpr:
		return a.regionRefStateForExpr(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil {
			return regionRefState{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return regionRefState{}, false
		}
		state, ok := a.currentRegionRefs[sym]
		if !ok {
			return regionRefState{}, false
		}
		return state, true
	case *ast.AllocExpr:
		ident, ok := n.Owner.(*ast.Ident)
		if !ok {
			return regionRefState{}, false
		}
		sym, state := a.lookupRegionState(ident.Name)
		if sym == nil || state.Destroyed {
			return regionRefState{}, false
		}
		return regionRefState{Region: sym, Generation: state.Generation, Valid: true}, true
	default:
		return regionRefState{}, false
	}
}

func (a *Analyzer) analyzeScopedPackedAllocExpr(expr *ast.AllocExpr) Type {
	enumType, variant, ok := a.packedAllocConstructorInfo(expr.Value)
	if !ok || enumType == nil || variant == nil || !enumType.Packed {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Pos(), "new without [...] expects a packed enum constructor inside an in-store block, got %s", valueType.String())
		return invalidType
	}
	storeType, ok := a.lookupPackedStore(enumType)
	if !ok {
		a.errorf(expr.Pos(), "packed enum constructor %q requires an active in %s: scope or explicit new[%s]", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name), packedEnumStoreTypeName(enumType.Name))
		if enumType.StoreType != nil {
			return a.analyzePackedAllocExpr(expr, enumType.StoreType)
		}
		return enumType
	}
	return a.analyzePackedAllocExpr(expr, storeType)
}

func (a *Analyzer) analyzePackedAllocExpr(expr *ast.AllocExpr, storeType *PackedEnumStoreType) Type {
	if fieldExpr, ok := expr.Value.(*ast.FieldExpr); ok {
		ident, ok := fieldExpr.Object.(*ast.Ident)
		if ok {
			base, ok := a.namedTypes[ident.Name]
			if ok {
				enumType, ok := base.(*EnumType)
				if ok {
					variant, ok := enumType.Variant(fieldExpr.Field)
					if ok && enumType.Packed && len(variant.Payload) == 0 {
						if storeType.Enum != enumType {
							a.errorf(allocOwnerPos(expr), "packed enum constructor %q requires store %q, got %q", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name), storeType.String())
						}
						return enumType
					}
				}
			}
		}
	}
	callExpr, ok := expr.Value.(*ast.CallExpr)
	if !ok {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Value.Pos(), "new[%s] expects a packed enum constructor call, got %s", storeType.String(), valueType.String())
		return invalidType
	}
	enumType, variant, ok := a.enumConstructorCall(callExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Value.Pos(), "new[%s] expects a packed enum constructor call, got %s", storeType.String(), valueType.String())
		return invalidType
	}
	if storeType.Enum != enumType {
		a.errorf(allocOwnerPos(expr), "packed enum constructor %q requires store %q, got %q", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name), storeType.String())
	}
	orderedArgs, commonArgs, ok := a.resolvePackedEnumConstructorArgs(callExpr, enumType, variant)
	if !ok {
		return enumType
	}
	if len(orderedArgs) != len(variant.Payload) {
		a.errorf(callExpr.Pos(), "enum constructor %q expects %d arguments, got %d", enumType.Name+"."+variant.Name, len(variant.Payload), len(callExpr.Args))
	}
	limit := len(orderedArgs)
	if len(variant.Payload) < limit {
		limit = len(variant.Payload)
	}
	for i := 0; i < len(orderedArgs); i++ {
		if i < limit {
			actual := a.analyzeValueExpr(orderedArgs[i], variant.Payload[i])
			if !AssignableTo(variant.Payload[i], actual) {
				label := variant.PayloadLabel(i)
				if label != "" {
					a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d (%s) to %q expects %s, got %s", i+1, label, enumType.Name+"."+variant.Name, variant.Payload[i].String(), actual.String())
				} else {
					a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d to %q expects %s, got %s", i+1, enumType.Name+"."+variant.Name, variant.Payload[i].String(), actual.String())
				}
			}
			a.consumeAffineValueExpr(orderedArgs[i], variant.Payload[i], a.enumConstructorMoveReason(enumType.Name, variant, i))
		} else {
			a.analyzeExpr(orderedArgs[i])
		}
	}
	for _, commonDecl := range enumType.Decl.Common {
		arg, ok := commonArgs[commonDecl.Name]
		if !ok {
			continue
		}
		field, ok := enumType.Common[commonDecl.Name]
		if !ok {
			a.analyzeExpr(arg)
			continue
		}
		actual := a.analyzeValueExpr(arg, field.Type)
		if !AssignableTo(field.Type, actual) {
			a.errorf(arg.Pos(), "packed enum common field %q for %q expects %s, got %s", commonDecl.Name, enumType.Name+"."+variant.Name, field.Type.String(), actual.String())
		}
		a.consumeAffineValueExpr(arg, field.Type, "move into enum common field "+strconv.Quote(commonDecl.Name))
	}
	return enumType
}

func allocOwnerPos(expr *ast.AllocExpr) lexer.Pos {
	if expr != nil && expr.Owner != nil {
		return expr.Owner.Pos()
	}
	if expr != nil {
		return expr.Pos()
	}
	return lexer.Pos{}
}

func (a *Analyzer) analyzeStructLiteralArgs(expr *ast.StructLitExpr, base *StructType, bindings map[string]Type) {
	if base == nil || base.Decl == nil {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return
	}
	if len(expr.Args) != len(base.Decl.Fields) {
		a.errorf(expr.Pos(), "struct literal %q expects %d arguments, got %d", expr.Name, len(base.Decl.Fields), len(expr.Args))
	}
	limit := len(expr.Args)
	if len(base.Decl.Fields) < limit {
		limit = len(base.Decl.Fields)
	}
	for i := 0; i < limit; i++ {
		fieldDecl := base.Decl.Fields[i]
		field, ok := base.Fields[fieldDecl.Name]
		if !ok {
			a.analyzeExpr(expr.Args[i])
			continue
		}
		expected := field.Type
		if len(bindings) > 0 {
			expected = a.substituteType(expected, bindings, nil, nil, nil)
		}
		actual := a.analyzeValueExpr(expr.Args[i], expected)
		if !AssignableTo(expected, actual) {
			a.errorf(expr.Args[i].Pos(), "struct literal field %q expects %s, got %s", fieldDecl.Name, expected.String(), actual.String())
		}
		a.consumeAffineValueExpr(expr.Args[i], expected, "move into struct literal field "+strconv.Quote(fieldDecl.Name))
	}
	for i := limit; i < len(expr.Args); i++ {
		a.analyzeExpr(expr.Args[i])
	}
}

func (a *Analyzer) errorExprTagName(expr ast.Expr) (string, bool) {
	fieldExpr, ok := expr.(*ast.FieldExpr)
	if !ok {
		return "", false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok {
		return "", false
	}
	base, ok := a.namedTypes[ident.Name]
	if !ok {
		return "", false
	}
	errSet, ok := base.(*ErrorSetType)
	if !ok || !errSet.HasQualifiedTag(ident.Name, fieldExpr.Field) {
		return "", false
	}
	return QualifyErrorTag(ident.Name, fieldExpr.Field), true
}

func (a *Analyzer) errorTagType(expr *ast.FieldExpr) (Type, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, ok := a.namedTypes[ident.Name]
	if !ok {
		return nil, false
	}
	errSet, ok := base.(*ErrorSetType)
	if !ok {
		return nil, false
	}
	if !errSet.HasQualifiedTag(ident.Name, expr.Field) {
		a.errorf(expr.Pos(), "error set %q has no tag %q", ErrorSetDiagnosticName(errSet), expr.Field)
		return invalidType, true
	}
	return errSet, true
}

func (a *Analyzer) enumVariantExprType(expr *ast.FieldExpr) (*EnumType, Type, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	base, ok := a.namedTypes[ident.Name]
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(expr.Field)
	if !ok {
		a.errorf(expr.Pos(), "enum %q has no variant %q", enumType.Name, expr.Field)
		return enumType, invalidType, true
	}
	if enumType.Packed {
		a.errorf(expr.Pos(), "packed enum constructor %q must be allocated with new[%s]", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name))
		return enumType, invalidType, true
	}
	if len(variant.Payload) == 0 {
		return enumType, nil, true
	}
	params := make([]Type, len(variant.Payload))
	copy(params, variant.Payload)
	return enumType, &FuncType{Name: enumType.Name + "." + variant.Name, Params: params, Return: enumType}, true
}

func (a *Analyzer) packedStoreExprType(expr *ast.FieldExpr) (*PackedEnumStoreType, Type, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	base, ok := a.namedTypes[ident.Name]
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok || !enumType.Packed || expr.Field != "Store" {
		return nil, nil, false
	}
	if enumType.StoreType == nil {
		return nil, invalidType, true
	}
	arenaType, ok := a.namedTypes["Arena"]
	if !ok {
		return enumType.StoreType, invalidType, true
	}
	return enumType.StoreType, &FuncType{Name: enumType.StoreType.Name, Params: []Type{arenaType}, Return: enumType.StoreType}, true
}

func (a *Analyzer) packedStoreConstructorCall(expr *ast.CallExpr) (*PackedEnumStoreType, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	storeType, _, ok := a.packedStoreExprType(fieldExpr)
	if !ok {
		return nil, false
	}
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "store constructor %q expects 1 argument, got %d", storeType.String(), len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return storeType, true
	}
	if arenaType, ok := a.namedTypes["Arena"]; ok {
		actual := a.analyzeValueExpr(expr.Args[0], arenaType)
		if !AssignableTo(arenaType, actual) {
			a.errorf(expr.Args[0].Pos(), "store constructor %q expects %s, got %s", storeType.String(), arenaType.String(), actual.String())
		}
	} else {
		a.analyzeExpr(expr.Args[0])
	}
	return storeType, true
}

func (a *Analyzer) enumConstructorCall(expr *ast.CallExpr) (*EnumType, *EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(fieldExpr)
	if !ok {
		return nil, nil, false
	}
	if variant == nil {
		a.errorf(fieldExpr.Pos(), "enum %q has no variant %q", enumType.Name, fieldExpr.Field)
		return enumType, nil, true
	}
	return enumType, variant, true
}

func (a *Analyzer) enumConstructorInfoFromFieldExpr(expr *ast.FieldExpr) (*EnumType, *EnumVariant, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	base, ok := a.namedTypes[ident.Name]
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(expr.Field)
	if !ok {
		return enumType, nil, true
	}
	return enumType, variant, true
}

func (a *Analyzer) packedAllocConstructorInfo(expr ast.Expr) (*EnumType, *EnumVariant, bool) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		return a.enumConstructorInfoFromFieldExpr(n)
	case *ast.CallExpr:
		return a.enumConstructorCall(n)
	default:
		return nil, nil, false
	}
}

func (a *Analyzer) analyzeBinaryExpr(expr *ast.BinaryExpr) Type {
	left := a.analyzeExpr(expr.Left)
	right := a.analyzeExpr(expr.Right)
	switch expr.Op {
	case lexer.TOKEN_AND, lexer.TOKEN_OR:
		if !IsBoolType(left) || !IsBoolType(right) {
			a.errorf(expr.Pos(), "logical operator requires bool operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		if runtimeStringComparable(left, right) {
			return a.namedTypes["bool"]
		}
		if IsNumericType(left) && IsNumericType(right) {
			return a.namedTypes["bool"]
		}
		if !(AssignableTo(left, right) || AssignableTo(right, left) || (IsNullType(left) && isRefLike(right)) || (IsNullType(right) && isRefLike(left))) {
			a.errorf(expr.Pos(), "cannot compare %s and %s", left.String(), right.String())
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "comparison requires numeric operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		if lref, ok := left.(*RefType); ok && IsNumericType(right) {
			return lref
		}
		if expr.Op == lexer.TOKEN_PLUS {
			if rref, ok := right.(*RefType); ok && IsNumericType(left) {
				return rref
			}
		}
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	case lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
		lexer.TOKEN_CARET, lexer.TOKEN_PIPE, lexer.TOKEN_AMPERSAND,
		lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeUnaryExpr(expr *ast.UnaryExpr) Type {
	operand := a.analyzeExpr(expr.Operand)
	switch expr.Op {
	case lexer.TOKEN_NOT:
		if !IsBoolType(operand) {
			a.errorf(expr.Pos(), "not operator requires bool operand")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_MINUS, lexer.TOKEN_TILDE:
		if !IsNumericType(operand) {
			a.errorf(expr.Pos(), "unary operator requires numeric operand")
		}
		return operand
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeMoveExpr(expr *ast.MoveExpr) Type {
	if expr == nil {
		return invalidType
	}
	return a.analyzeExpr(expr.Operand)
}

func (a *Analyzer) analyzeCallExpr(expr *ast.CallExpr) Type {
	if storeType, ok := a.packedStoreConstructorCall(expr); ok {
		return storeType
	}
	if enumType, variant, ok := a.enumConstructorCall(expr); ok {
		if variant == nil {
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
		if enumType.Packed {
			a.errorf(expr.Pos(), "packed enum constructor %q must be allocated with new[%s]", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name))
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return enumType
		}
		orderedArgs, ok := a.resolveEnumConstructorArgs(expr, enumType, variant)
		if !ok {
			return enumType
		}
		if len(orderedArgs) != len(variant.Payload) {
			a.errorf(expr.Pos(), "enum constructor %q expects %d arguments, got %d", enumType.Name+"."+variant.Name, len(variant.Payload), len(expr.Args))
		}
		limit := len(orderedArgs)
		if len(variant.Payload) < limit {
			limit = len(variant.Payload)
		}
		for i := 0; i < len(orderedArgs); i++ {
			if i < limit {
				actual := a.analyzeValueExpr(orderedArgs[i], variant.Payload[i])
				if !AssignableTo(variant.Payload[i], actual) {
					label := variant.PayloadLabel(i)
					if label != "" {
						a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d (%s) to %q expects %s, got %s", i+1, label, enumType.Name+"."+variant.Name, variant.Payload[i].String(), actual.String())
					} else {
						a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d to %q expects %s, got %s", i+1, enumType.Name+"."+variant.Name, variant.Payload[i].String(), actual.String())
					}
				}
				a.consumeAffineValueExpr(orderedArgs[i], variant.Payload[i], a.enumConstructorMoveReason(enumType.Name, variant, i))
			} else {
				a.analyzeExpr(orderedArgs[i])
			}
		}
		return enumType
	}
	fnType := a.analyzeExpr(expr.Func)
	ft, ok := fnType.(*FuncType)
	if !ok {
		a.errorf(expr.Pos(), "cannot call non-function value of type %s", fnType.String())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "named arguments are only supported for enum constructors")
	}
	if !ft.Variadic && len(expr.Args) != len(ft.Params) {
		a.errorf(expr.Pos(), "function %q expects %d arguments, got %d", ft.Name, len(ft.Params), len(expr.Args))
	}
	if ft.Variadic && len(expr.Args) < len(ft.Params) {
		a.errorf(expr.Pos(), "variadic function %q expects at least %d arguments, got %d", ft.Name, len(ft.Params), len(expr.Args))
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	regionBindings := map[string]string{}
	permissionBindings := map[string][]ast.PermissionRef{}
	regionParams := regionParamSet(ft.RegionParams)
	limit := len(ft.Params)
	if len(expr.Args) < limit {
		limit = len(expr.Args)
	}
	for i := 0; i < len(expr.Args); i++ {
		var argType Type
		if i < limit {
			expectedType := a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
			argType = a.analyzeValueExpr(expr.Args[i], expectedType)
			a.collectTypeBindings(ft.Params[i], argType, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			expectedType = a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
			if !AssignableTo(expectedType, argType) {
				a.errorf(expr.Args[i].Pos(), "argument %d to %q expects %s, got %s", i+1, ft.Name, expectedType.String(), argType.String())
				a.reportShapeMismatchNotes(expr.Args[i].Pos(), expectedType, argType)
			}
			a.consumeAffineValueExpr(expr.Args[i], expectedType, "argument to call "+strconv.Quote(ft.Name))
		} else {
			argType = a.analyzeExpr(expr.Args[i])
		}
	}
	for _, name := range ft.RegionParams {
		if _, ok := regionBindings[name]; !ok {
			a.errorf(expr.Pos(), "cannot infer region parameter %q for call to %q", name, ft.Name)
		}
	}
	for _, name := range ft.PermissionParams {
		if _, ok := permissionBindings[name]; !ok {
			a.errorf(expr.Pos(), "cannot infer permission parameter %q for call to %q", name, ft.Name)
		}
	}
	appliedType, _ := a.substituteType(ft, bindings, shapeBindings, regionBindings, permissionBindings).(*FuncType)
	if appliedType == nil {
		appliedType = ft
	}
	if len(bindings) != 0 || len(shapeBindings) != 0 || len(regionBindings) != 0 || len(permissionBindings) != 0 {
		a.exprTypes[expr.Func] = appliedType
	}
	a.recordFunctionPermissionRefs(functionPermissionRefs(appliedType))
	if ft.Return == nil {
		return a.namedTypes["void"]
	}
	a.bindFreshReturnShapes(appliedType, shapeBindings)
	return a.substituteType(appliedType.Return, bindings, shapeBindings, regionBindings, permissionBindings)
}

func (a *Analyzer) resolveEnumConstructorArgs(expr *ast.CallExpr, enumType *EnumType, variant *EnumVariant) ([]ast.Expr, bool) {
	if expr == nil || variant == nil {
		return nil, false
	}
	namedCount := expr.NamedArgCount()
	if namedCount == 0 {
		return expr.Args, true
	}
	if namedCount != len(expr.Args) {
		a.errorf(expr.Pos(), "enum constructor %q cannot mix positional and named arguments", enumType.Name+"."+variant.Name)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, false
	}
	if !variant.HasNamedPayloads() {
		a.errorf(expr.Pos(), "enum constructor %q does not declare named payload fields", enumType.Name+"."+variant.Name)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, false
	}
	if len(expr.Args) != len(variant.Payload) {
		a.errorf(expr.Pos(), "enum constructor %q expects %d arguments, got %d", enumType.Name+"."+variant.Name, len(variant.Payload), len(expr.Args))
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seen := make([]bool, len(variant.Payload))
	ok := true
	for i, arg := range expr.Args {
		name := expr.ArgName(i)
		index, found := variant.PayloadIndex(name)
		if !found {
			a.errorf(arg.Pos(), "enum constructor %q has no payload field %q", enumType.Name+"."+variant.Name, name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		if seen[index] {
			a.errorf(arg.Pos(), "enum constructor %q payload field %q is specified more than once", enumType.Name+"."+variant.Name, name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		ordered[index] = arg
		seen[index] = true
	}
	for i, wasSeen := range seen {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label != "" {
				a.errorf(expr.Pos(), "enum constructor %q is missing payload field %q", enumType.Name+"."+variant.Name, label)
			} else {
				a.errorf(expr.Pos(), "enum constructor %q is missing argument %d", enumType.Name+"."+variant.Name, i+1)
			}
			ok = false
		}
	}
	if !ok {
		return nil, false
	}
	return ordered, true
}

func (a *Analyzer) resolvePackedEnumConstructorArgs(expr *ast.CallExpr, enumType *EnumType, variant *EnumVariant) ([]ast.Expr, map[string]ast.Expr, bool) {
	if expr == nil || enumType == nil || variant == nil {
		return nil, nil, false
	}
	namedCount := expr.NamedArgCount()
	if namedCount == 0 {
		return expr.Args, nil, true
	}
	if namedCount != len(expr.Args) {
		a.errorf(expr.Pos(), "enum constructor %q cannot mix positional and named arguments", enumType.Name+"."+variant.Name)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, nil, false
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seenPayload := make([]bool, len(variant.Payload))
	commonArgs := make(map[string]ast.Expr)
	ok := true
	for i, arg := range expr.Args {
		name := expr.ArgName(i)
		if index, found := variant.PayloadIndex(name); found {
			if seenPayload[index] {
				a.errorf(arg.Pos(), "enum constructor %q payload field %q is specified more than once", enumType.Name+"."+variant.Name, name)
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			ordered[index] = arg
			seenPayload[index] = true
			continue
		}
		if _, found := enumType.Common[name]; found {
			if _, exists := commonArgs[name]; exists {
				a.errorf(arg.Pos(), "packed enum constructor %q common field %q is specified more than once", enumType.Name+"."+variant.Name, name)
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			commonArgs[name] = arg
			continue
		}
		a.errorf(arg.Pos(), "packed enum constructor %q has no payload or common field %q", enumType.Name+"."+variant.Name, name)
		a.analyzeExpr(arg)
		ok = false
	}
	for i, wasSeen := range seenPayload {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label != "" {
				a.errorf(expr.Pos(), "enum constructor %q is missing payload field %q", enumType.Name+"."+variant.Name, label)
			} else {
				a.errorf(expr.Pos(), "enum constructor %q is missing argument %d", enumType.Name+"."+variant.Name, i+1)
			}
			ok = false
		}
	}
	if !ok {
		return nil, nil, false
	}
	return ordered, commonArgs, true
}

func (a *Analyzer) collectRuntimeBridgeBindings(pattern, actual Type, bindings map[string]Type, shapeBindings map[string]Shape, regionBindings map[string]string, permissionBindings map[string][]ast.PermissionRef, regionParams map[string]bool) bool {
	bridge, ok := classifyRuntimeBridge(pattern, actual)
	if !ok {
		return false
	}
	switch bridge.Kind {
	case runtimeBridgeDArrayDynArray:
		if patternDArray, ok := pattern.(*DArrayType); ok {
			a.collectTypeBindings(patternDArray.Elem, bridge.DynArray.Args[0], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return true
		}
		if patternDynArray, ok := dynArrayRuntimeInstance(pattern); ok {
			a.collectTypeBindings(patternDynArray.Args[0], bridge.DArray.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return true
		}
		return true
	case runtimeBridgeDictDynDict:
		if patternDict, ok := pattern.(*DictType); ok {
			a.collectTypeBindings(patternDict.Value, bridge.DynDict.Args[0], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return true
		}
		if patternDynDict, ok := dynDictRuntimeInstance(pattern); ok {
			a.collectTypeBindings(patternDynDict.Args[0], bridge.Dict.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return true
		}
		return true
	case runtimeBridgeDArrayViewDynArrayView, runtimeBridgeDStrU8Ref:
		return true
	default:
		return false
	}
}

func regionParamSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func (a *Analyzer) collectRegionBinding(patternRegion, actualRegion string, bindings map[string]string, regionParams map[string]bool) {
	if patternRegion == "" || actualRegion == "" || bindings == nil || regionParams == nil || !regionParams[patternRegion] {
		return
	}
	if _, exists := bindings[patternRegion]; !exists {
		bindings[patternRegion] = actualRegion
	}
}

func (a *Analyzer) collectPermissionBinding(name string, refs []ast.PermissionRef, permissionBindings map[string][]ast.PermissionRef) {
	if name == "" || permissionBindings == nil {
		return
	}
	canonical := canonicalizePermissionRefs(refs)
	if _, exists := permissionBindings[name]; !exists {
		permissionBindings[name] = canonical
	}
}

func (a *Analyzer) collectTypeBindings(pattern, actual Type, bindings map[string]Type, shapeBindings map[string]Shape, regionBindings map[string]string, permissionBindings map[string][]ast.PermissionRef, regionParams map[string]bool) {
	if pattern == nil || actual == nil {
		return
	}
	if a.collectRuntimeBridgeBindings(pattern, actual, bindings, shapeBindings, regionBindings, permissionBindings, regionParams) {
		return
	}
	switch p := pattern.(type) {
	case *TypeParamType:
		if _, exists := bindings[p.Name]; !exists {
			bindings[p.Name] = actual
		}
	case *ErrorUnionType:
		if act, ok := actual.(*ErrorUnionType); ok {
			a.collectTypeBindings(p.Value, act.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return
		}
		a.collectTypeBindings(p.Value, actual, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
	case *RefType:
		if act, ok := actual.(*RefType); ok {
			a.collectRegionBinding(p.Region, act.Region, regionBindings, regionParams)
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *ArrayType:
		if act, ok := actual.(*ArrayType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *DArrayType:
		if act, ok := actual.(*DArrayType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectShapeBinding(p.Shape, act.Shape, shapeBindings)
		}
	case *DArrayViewType:
		if act, ok := actual.(*DArrayViewType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *DStrType:
		if act, ok := actual.(*DStrType); ok {
			a.collectShapeBinding(p.Shape, act.Shape, shapeBindings)
		}
	case *DictType:
		if act, ok := actual.(*DictType); ok {
			a.collectTypeBindings(p.Key, act.Key, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectTypeBindings(p.Value, act.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *SViewType:
		_, _ = actual.(*SViewType)
	case *EnumType:
		_, _ = actual.(*EnumType)
	case *GenericInstanceType:
		if act, ok := actual.(*GenericInstanceType); ok && p.Name == act.Name && len(p.Args) == len(act.Args) {
			for i := range p.Args {
				a.collectTypeBindings(p.Args[i], act.Args[i], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			}
		}
	case *FuncType:
		if act, ok := actual.(*FuncType); ok {
			limit := len(p.Params)
			if len(act.Params) < limit {
				limit = len(act.Params)
			}
			for i := 0; i < limit; i++ {
				a.collectTypeBindings(p.Params[i], act.Params[i], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			}
			a.collectTypeBindings(p.Return, act.Return, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			if name, ok := funcTypeHasSinglePermissionRowParam(p); ok {
				a.collectPermissionBinding(name, functionPermissionRefs(act), permissionBindings)
			}
		}
	}
}

func (a *Analyzer) collectShapeBinding(pattern, actual Shape, bindings map[string]Shape) {
	param, ok := pattern.(*ShapeParam)
	if !ok {
		return
	}
	if _, exists := bindings[param.Name]; !exists {
		bindings[param.Name] = actual
	}
}

func (a *Analyzer) matchReturnType(actual Type) Type {
	if a.currentReturn == nil || actual == nil {
		return a.currentReturn
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	permissionBindings := map[string][]ast.PermissionRef{}
	a.collectTypeBindings(a.currentReturn, actual, bindings, shapeBindings, nil, permissionBindings, nil)
	return a.substituteType(a.currentReturn, bindings, shapeBindings, nil, permissionBindings)
}

func (a *Analyzer) bindFreshReturnShapes(fn *FuncType, bindings map[string]Shape) {
	if fn == nil || fn.Return == nil {
		return
	}
	for _, name := range fn.FreshReturnShapeParams {
		a.bindFreshShape(&ShapeParam{Name: name}, fn.Name, bindings)
	}
}

func (a *Analyzer) bindFreshShape(shape Shape, origin string, bindings map[string]Shape) {
	param, ok := shape.(*ShapeParam)
	if !ok {
		return
	}
	if _, exists := bindings[param.Name]; exists {
		return
	}
	a.freshShapeCounter++
	bindings[param.Name] = &FreshShape{ID: a.freshShapeCounter, Label: param.Name, Origin: origin}
}

func (a *Analyzer) analyzeFieldExpr(expr *ast.FieldExpr) Type {
	if field, ok := dstrSyntheticField(a.analyzeExpr(expr.Object), expr.Field); ok {
		if state, ok := a.lookupAffineValueState(expr); ok && a.containsAffineHandleValues(field.Type, map[string]bool{}) {
			a.errorf(expr.Pos(), "%s %q cannot be used after %s", affineHandleKind(field.Type), affineValueDisplayName(expr), state.ConsumedBy)
		}
		return field.Type
	}
	field, ok := a.lookupField(a.analyzeExpr(expr.Object), expr.Field, expr.Pos())
	if !ok {
		return invalidType
	}
	if state, ok := a.lookupAffineValueState(expr); ok && a.containsAffineHandleValues(field.Type, map[string]bool{}) {
		a.errorf(expr.Pos(), "%s %q cannot be used after %s", affineHandleKind(field.Type), affineValueDisplayName(expr), state.ConsumedBy)
	}
	return field.Type
}

func (a *Analyzer) analyzeIndexExpr(expr *ast.IndexExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	indexType := a.analyzeExpr(expr.Index)
	if !IsNumericType(indexType) {
		a.errorf(expr.Index.Pos(), "index must be numeric, got %s", indexType.String())
	}
	if arr, ok := objType.(*ArrayType); ok {
		a.checkConstantArrayIndexBounds(arr, expr.Index)
		if isStringArrayType(arr) {
			return a.namedTypes["char"]
		}
		return arr.Elem
	}
	if darray, ok := objType.(*DArrayType); ok {
		return darray.Elem
	}
	if view, ok := objType.(*ViewType); ok {
		return view.Elem
	}
	if view, ok := objType.(*DArrayViewType); ok {
		return view.Elem
	}
	if _, ok := objType.(*DStrType); ok {
		return a.namedTypes["char"]
	}
	if isStringViewType(objType) {
		return a.namedTypes["char"]
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "indexing requires proven non-null reference, got %s", objType.String())
			return invalidType
		}
		if arr, ok := ref.Elem.(*ArrayType); ok {
			a.checkConstantArrayIndexBounds(arr, expr.Index)
			if isStringArrayType(arr) {
				return a.namedTypes["char"]
			}
			return arr.Elem
		}
		if darray, ok := ref.Elem.(*DArrayType); ok {
			return darray.Elem
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			return view.Elem
		}
		if view, ok := ref.Elem.(*DArrayViewType); ok {
			return view.Elem
		}
		if _, ok := ref.Elem.(*DStrType); ok {
			return a.namedTypes["char"]
		}
		if isStringViewType(ref.Elem) {
			return a.namedTypes["char"]
		}
		return ref.Elem
	}
	a.errorf(expr.Pos(), "indexing requires string, array, view, or reference type, got %s", objType.String())
	return invalidType
}

func (a *Analyzer) analyzeSliceExpr(expr *ast.SliceExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	startType := a.analyzeExpr(expr.Start)
	endType := a.analyzeExpr(expr.End)
	if !IsNumericType(startType) {
		a.errorf(expr.Start.Pos(), "slice start must be numeric, got %s", startType.String())
	}
	if !IsNumericType(endType) {
		a.errorf(expr.End.Pos(), "slice end must be numeric, got %s", endType.String())
	}
	if array, ok := objType.(*ArrayType); ok {
		if isStringArrayType(array) {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		return &ViewType{Elem: array.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if view, ok := objType.(*DArrayType); ok {
		return &DArrayViewType{Elem: view.Elem}
	}
	if view, ok := objType.(*ViewType); ok {
		return &ViewType{Elem: view.Elem}
	}
	if view, ok := objType.(*DArrayViewType); ok {
		return &DArrayViewType{Elem: view.Elem}
	}
	if dstr, ok := objType.(*DStrType); ok {
		_ = dstr
		return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if isStringViewType(objType) {
		return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "slicing requires proven non-null reference, got %s", objType.String())
			return invalidType
		}
		if array, ok := ref.Elem.(*ArrayType); ok {
			if isStringArrayType(array) {
				return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
			}
			return &ViewType{Elem: array.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		if view, ok := ref.Elem.(*DArrayType); ok {
			return &DArrayViewType{Elem: view.Elem}
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			return &ViewType{Elem: view.Elem}
		}
		if view, ok := ref.Elem.(*DArrayViewType); ok {
			return &DArrayViewType{Elem: view.Elem}
		}
		if _, ok := ref.Elem.(*DStrType); ok {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		if isStringViewType(ref.Elem) {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
	}
	a.errorf(expr.Pos(), "slicing requires string, array, or view type, got %s", objType.String())
	return invalidType
}

func (a *Analyzer) analyzeValueExpr(expr ast.Expr, expected Type) Type {
	if list, ok := expr.(*ast.ListLitExpr); ok {
		return a.analyzeListLitExprWithExpected(list, expected)
	}
	return a.analyzeExpr(expr)
}

func (a *Analyzer) analyzeListLitExprWithExpected(expr *ast.ListLitExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	expectedArray, useExpected := contextualArrayLiteralType(expected)
	if len(expr.Elems) == 0 {
		if useExpected {
			if expectedArray.HasConstSize && expectedArray.ConstSize != 0 {
				a.errorf(expr.Pos(), "array literal expects %d elements, got 0", expectedArray.ConstSize)
			}
			a.exprTypes[expr] = expectedArray
			return expectedArray
		}
		a.errorf(expr.Pos(), "empty array literal requires an expected array type")
		a.exprTypes[expr] = invalidType
		return invalidType
	}

	var elemType Type
	if useExpected {
		elemType = expectedArray.Elem
		if expectedArray.HasConstSize && expectedArray.ConstSize != int64(len(expr.Elems)) {
			a.errorf(expr.Pos(), "array literal expects %d elements, got %d", expectedArray.ConstSize, len(expr.Elems))
		}
	}

	for _, elem := range expr.Elems {
		itemType := a.analyzeValueExpr(elem, elemType)
		if useExpected {
			if !AssignableTo(expectedArray.Elem, itemType) {
				a.errorf(elem.Pos(), "array literal element expects %s, got %s", expectedArray.Elem.String(), itemType.String())
			}
			a.consumeAffineValueExpr(elem, expectedArray.Elem, "move into array literal element")
			continue
		}
		a.consumeAffineValueExpr(elem, itemType, "move into array literal element")
		if elemType == nil {
			elemType = itemType
			continue
		}
		merged := MergeTypes(elemType, itemType)
		if IsInvalidType(merged) {
			a.errorf(elem.Pos(), "array literal elements are incompatible: %s and %s", elemType.String(), itemType.String())
			a.exprTypes[expr] = invalidType
			return invalidType
		}
		elemType = merged
	}

	if useExpected {
		a.exprTypes[expr] = expectedArray
		return expectedArray
	}
	if elemType == nil || IsInvalidType(elemType) {
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	result := &ArrayType{Elem: elemType, Size: strconv.Itoa(len(expr.Elems)), HasConstSize: true, ConstSize: int64(len(expr.Elems))}
	a.exprTypes[expr] = result
	return result
}

func contextualArrayLiteralType(expected Type) (*ArrayType, bool) {
	arrayType, ok := expected.(*ArrayType)
	if !ok {
		return nil, false
	}
	return arrayType, true
}

func (a *Analyzer) enumConstructorMoveReason(enumName string, variant *EnumVariant, index int) string {
	if variant == nil {
		return "move into enum constructor payload"
	}
	if label := variant.PayloadLabel(index); label != "" {
		return "move into enum payload " + strconv.Quote(enumName+"."+variant.Name+"."+label)
	}
	return "move into enum payload " + strconv.Quote(enumName+"."+variant.Name) + " argument " + strconv.Itoa(index+1)
}

func containsTypeParam(t Type) bool {
	switch n := t.(type) {
	case nil:
		return false
	case *TypeParamType:
		return true
	case *ErrorUnionType:
		return containsTypeParam(n.Value)
	case *RefType:
		return containsTypeParam(n.Elem)
	case *ArrayType:
		return containsTypeParam(n.Elem)
	case *DArrayType:
		return containsTypeParam(n.Elem)
	case *ViewType:
		return containsTypeParam(n.Elem)
	case *DArrayViewType:
		return containsTypeParam(n.Elem)
	case *GenericInstanceType:
		for _, arg := range n.Args {
			if containsTypeParam(arg) {
				return true
			}
		}
		return containsTypeParam(n.Base)
	case *FuncType:
		for _, param := range n.Params {
			if containsTypeParam(param) {
				return true
			}
		}
		return containsTypeParam(n.Return)
	case *EnumType:
		for _, variant := range n.Variants {
			for _, payload := range variant.Payload {
				if containsTypeParam(payload) {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) assignmentTargetType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, ok = a.globalScope.Lookup(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			if ref, ok := sym.Type.(*RefType); ok {
				return ref.Elem
			}
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
			return sym.Type
		}
		if a.currentScope != nil {
			if current, exists := a.currentScope.Symbols[n.Name]; exists && current == sym && a.currentScope.Parent != nil {
				if parent, ok := a.currentScope.Parent.Lookup(n.Name); ok && parent.Node == sym.Node && parent.Kind == sym.Kind && parent.Mutable {
					return parent.Type
				}
			}
		}
		return sym.Type
	case *ast.FieldExpr:
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		return field.Type
	case *ast.IndexExpr:
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot assign to %s", kind)
			return invalidType
		}
		return targetType
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func (a *Analyzer) asRefTargetType(expr ast.Expr, asKind string) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, ok = a.globalScope.Lookup(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
		}
		return a.refTypeWithAsKind(sym.Type, asKind)
	case *ast.FieldExpr:
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		return a.refTypeWithAsKind(field.Type, asKind)
	case *ast.IndexExpr:
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot take a reference to %s", kind)
			return invalidType
		}
		return a.refTypeWithAsKind(targetType, asKind)
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func (a *Analyzer) refTypeWithAsKind(t Type, asKind string) Type {
	ref, ok := t.(*RefType)
	if !ok {
		return t
	}
	switch asKind {
	case "&":
		return cloneRefTypeWithState(ref, RefStateNonNull)
	case "!":
		return cloneRefTypeWithState(ref, RefStateNull)
	default:
		return t
	}
}

func (a *Analyzer) inferAddrOfStorage(expr ast.Expr) RefStorage {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.inferAddrOfStorage(n.Inner)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				switch sym.Kind {
				case SymbolLocal, SymbolParam, SymbolRegion:
					return RefStorageStack
				case SymbolGlobal:
					return RefStorageStatic
				}
			}
		}
		if sym, ok := a.globalScope.Lookup(n.Name); ok && sym.Kind == SymbolGlobal {
			return RefStorageStatic
		}
	case *ast.FieldExpr:
		if objType, ok := a.exprTypes[n.Object].(*RefType); ok {
			return objType.Storage
		}
		return a.inferAddrOfStorage(n.Object)
	case *ast.IndexExpr:
		switch objType := a.exprTypes[n.Object].(type) {
		case *RefType:
			if _, ok := objType.Elem.(*ArrayType); ok {
				return objType.Storage
			}
			return RefStorageAny
		case *ArrayType:
			return a.inferAddrOfStorage(n.Object)
		}
	}
	return RefStorageAny
}

func (a *Analyzer) lookupField(objType Type, fieldName string, pos lexer.Pos) (Field, bool) {
	if field, ok := dstrSyntheticField(objType, fieldName); ok {
		return field, true
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(pos, "field access requires proven non-null reference, got %s", objType.String())
			return Field{}, false
		}
		objType = ref.Elem
	}
	if enumType, ok := objType.(*EnumType); ok && enumType.Packed {
		field, ok := enumType.Common[fieldName]
		if !ok {
			a.errorf(pos, "packed enum %q has no common field %q", enumType.Name, fieldName)
			return Field{}, false
		}
		return field, true
	}
	if runtimeBacked := a.runtimeBackedStructType(objType); runtimeBacked != nil {
		objType = runtimeBacked
	}
	switch t := objType.(type) {
	case *StructType:
		field, ok := t.Fields[fieldName]
		if !ok {
			a.errorf(pos, "struct %q has no field %q", t.Name, fieldName)
			return Field{}, false
		}
		return field, true
	case *GenericInstanceType:
		baseStruct, ok := t.Base.(*StructType)
		if !ok {
			a.errorf(pos, "field access requires struct type, got %s", objType.String())
			return Field{}, false
		}
		field, ok := baseStruct.Fields[fieldName]
		if !ok {
			a.errorf(pos, "struct %q has no field %q", baseStruct.Name, fieldName)
			return Field{}, false
		}
		bindings := map[string]Type{}
		for i, name := range baseStruct.TypeParams {
			if i < len(t.Args) {
				bindings[name] = t.Args[i]
			}
		}
		field.Type = a.substituteType(field.Type, bindings, nil, nil, nil)
		return field, true
	default:
		a.errorf(pos, "field access requires struct type, got %s", objType.String())
		return Field{}, false
	}
}

func (a *Analyzer) runtimeBackedStructType(t Type) Type {
	if dav, ok := t.(*DArrayViewType); ok {
		base, ok := a.namedTypes["DynArrayView"]
		if !ok {
			return nil
		}
		_ = dav
		return base
	}
	if _, ok := t.(*SViewType); ok {
		base, ok := a.namedTypes["StringView"]
		if !ok {
			return nil
		}
		return base
	}
	if dict, ok := t.(*DictType); ok {
		base, ok := a.namedTypes["DynDict"]
		if !ok {
			return nil
		}
		return &GenericInstanceType{Name: "DynDict", Base: base, Args: []Type{dict.Value}}
	}
	darray, ok := t.(*DArrayType)
	if !ok {
		return nil
	}
	base, ok := a.namedTypes["DynArray"]
	if !ok {
		return nil
	}
	return &GenericInstanceType{Name: "DynArray", Base: base, Args: []Type{darray.Elem}}
}

func valueOnlyIndexKind(t Type) (string, bool) {
	if _, ok := t.(*DStrType); ok {
		return "string index", true
	}
	if array, ok := t.(*ArrayType); ok && isStringArrayType(array) {
		return "string index", true
	}
	if isStringViewType(t) {
		return "string view index", true
	}
	ref, ok := t.(*RefType)
	if !ok {
		return "", false
	}
	if _, ok := ref.Elem.(*DStrType); ok {
		return "string index", true
	}
	if array, ok := ref.Elem.(*ArrayType); ok && isStringArrayType(array) {
		return "string index", true
	}
	if isStringViewType(ref.Elem) {
		return "string view index", true
	}
	return "", false
}

func isStringArrayType(t *ArrayType) bool {
	return t != nil && (t.SurfaceName == "str" || t.SurfaceName == "string")
}

func isStringViewType(t Type) bool {
	if _, ok := t.(*SViewType); ok {
		return true
	}
	return isRuntimeStringViewType(t)
}

func isRuntimeStringViewType(t Type) bool {
	st, ok := t.(*StructType)
	return ok && st.Name == "StringView"
}

func dstrSyntheticField(t Type, fieldName string) (Field, bool) {
	if fieldName != "len" {
		return Field{}, false
	}
	if _, ok := t.(*DStrType); ok {
		return Field{Name: "len", Type: builtinI64Type(), Mutable: false}, true
	}
	ref, ok := t.(*RefType)
	if !ok {
		return Field{}, false
	}
	if _, ok := ref.Elem.(*DStrType); ok {
		return Field{Name: "len", Type: builtinI64Type(), Mutable: false}, true
	}
	return Field{}, false
}

func builtinI64Type() Type {
	return &BuiltinType{Name: "i64"}
}

type runtimeStringKind int

const (
	runtimeStringNone runtimeStringKind = iota
	runtimeStringDStr
	runtimeStringView
	runtimeStringRaw
)

func runtimeStringComparable(left Type, right Type) bool {
	leftKind := runtimeStringKindOf(left)
	rightKind := runtimeStringKindOf(right)
	if leftKind == runtimeStringNone || rightKind == runtimeStringNone {
		return false
	}
	if leftKind == runtimeStringRaw && rightKind == runtimeStringRaw {
		return false
	}
	return leftKind != runtimeStringNone && rightKind != runtimeStringNone
}

func runtimeStringKindOf(t Type) runtimeStringKind {
	if t == nil {
		return runtimeStringNone
	}
	if _, ok := t.(*DStrType); ok {
		return runtimeStringDStr
	}
	if isStringViewType(t) {
		return runtimeStringView
	}
	ref, ok := t.(*RefType)
	if !ok {
		return runtimeStringNone
	}
	if builtin, ok := ref.Elem.(*BuiltinType); ok && builtin.Name == "u8" {
		return runtimeStringRaw
	}
	if isStringViewType(ref.Elem) {
		return runtimeStringView
	}
	return runtimeStringNone
}
