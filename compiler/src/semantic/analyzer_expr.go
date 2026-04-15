package semantic

import (
	"fmt"
	"strconv"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) analyzeExpr(expr ast.Expr) (result Type) {
	defer func() {
		if expr != nil {
			a.exprTypes[expr] = result
			a.recordExprOptimizationFacts(expr, result)
		}
	}()
	switch n := expr.(type) {
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				result = promoteWritableRefType(sym.Type, sym.Mutable)
				if sym.Kind == SymbolRegionMark {
					a.errorf(n.Pos(), "checkpoint %q can only be used in restore <region> from %q", n.Name, n.Name)
					return
				}
				if sym.Kind == SymbolCheckpoint {
					a.errorf(n.Pos(), "checkpoint %q can only be used in restore %q", n.Name, n.Name)
					return
				}
				if refState, ok := a.currentRegionRefs[sym]; ok {
					if _, dep, invalid := firstInvalidRegionDependency(refState); invalid {
						label := "value"
						if _, isRef := result.(*RefType); isRef {
							label = "reference"
						}
						a.errorf(n.Pos(), "%s %q is invalid after %s", label, n.Name, dep.InvalidatedBy)
						return
					}
				}
				if state, ok := a.lookupAffineValueState(n); ok && a.containsAffineHandleValues(result, map[string]bool{}) {
					a.errorf(n.Pos(), "%s %q cannot be used after %s", affineHandleKind(sym.Type), n.Name, state.ConsumedBy)
					return
				}
				if ownerType, ok := borrowableOwnerRefElemType(result); ok {
					if key, ok := a.lookupBorrowedOwnerRefKey(n); ok {
						if state, ok := a.lookupAffineValueStateForKey(key); ok && state.ConsumedBy != "" {
							a.errorf(n.Pos(), "%s %q cannot be used after %s", affineHandleKind(ownerType), n.Name, state.ConsumedBy)
							return
						}
					}
				}
				if fnType, ok := a.lookupCurrentFunctionValueType(sym); ok {
					result = fnType
					return
				}
				if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok {
					if fnType, ok := a.functionValueTypeForExpr(valueExpr); ok {
						result = promoteWritableRefType(fnType, sym.Mutable)
						return
					}
				}
				if specializedType, ok := a.lookupCurrentSpecializedValueType(sym); ok {
					result = promoteWritableRefType(specializedType, sym.Mutable)
				}
				if t, ok := a.lookupRefinedExprType(n); ok {
					if specializedType, ok := a.specializeCallbackCarryingType(t, result); ok {
						result = promoteWritableRefType(specializedType, sym.Mutable)
					} else {
						result = promoteWritableRefType(t, sym.Mutable)
					}
					return
				}
				return
			}
		}
		if t, ok := a.lookupRefinedExprType(n); ok {
			result = t
			return
		}
		if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok {
			result = promoteWritableRefType(sym.Type, sym.Mutable)
			if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok {
				if fnType, ok := a.functionValueTypeForExpr(valueExpr); ok {
					result = promoteWritableRefType(fnType, sym.Mutable)
					return
				}
			}
			return
		}
		if a.currentScope != nil {
			if hint, ok := a.currentScope.LookupConditionalBindingHint(n.Name); ok {
				a.errorf(n.Pos(), "%s", hint)
				result = invalidType
				return
			}
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
	case *ast.FloatLit:
		if n.Suffix != "" {
			if t, ok := a.namedTypes[n.Suffix]; ok {
				result = t
				return
			}
		}
		result = a.namedTypes["f64"]
		return
	case *ast.ShorthandMemberExpr:
		result = a.analyzeShorthandMemberExpr(n, nil)
		return
	case *ast.StringLit:
		result = &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
		return
	case *ast.CharLit:
		result = a.namedTypes["char"]
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
	case *ast.ExprBlock:
		result = a.analyzeExprBlock(n)
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
		if interfaceMethodType, ok := a.resolveInterfaceMethodExprType(n); ok {
			result = interfaceMethodType
			return
		}
		if errorType, ok := a.errorTagType(n); ok {
			result = errorType
			return
		}
		if tagType, ok := a.packedEnumTagExprType(n); ok {
			result = tagType
			return
		}
		if kindType, ok := a.treeCategoryKindExprType(n); ok {
			result = kindType
			return
		}
		if constEnumType, ok := a.constEnumMemberExprType(n); ok {
			result = constEnumType
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
		if storeType, ctorType, ok := a.treeStoreExprType(n); ok {
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
		if treeType, variant, ok := a.treeConstructorInfoFromFieldExpr(n); ok && treeType != nil && variant != nil && len(variant.Payload) == 0 && !a.treeConstructorCallees[n] {
			a.requireActiveTreeConstructorOwner(n.Pos(), treeType, variant)
			if treeType.Family != nil && treeType.Family.Decl != nil && len(treeType.Family.Decl.Common) != 0 {
				a.errorf(n.Pos(), "tree constructor %q requires explicit common fields; use call syntax with named arguments", treeType.Name+"."+variant.Name)
				result = invalidType
				return
			}
		}
		if treeType, ctorType, ok := a.treeVariantExprType(n); ok {
			if ctorType != nil {
				result = ctorType
			} else {
				result = treeType
			}
			return
		}
		if memberType, ctorType, ok := a.treeExactMemberExprType(n); ok {
			if ctorType != nil {
				result = ctorType
			} else {
				if family, ok := TreeFamilyForMemberType(memberType); ok {
					a.requireActiveTreeFamilyConstructorOwner(n.Pos(), family, memberType.String())
				}
				if len(treeExactMemberFieldDecls(memberType)) != 0 {
					a.errorf(n.Pos(), "tree constructor %q requires explicit constructor arguments", memberType.String())
					result = invalidType
				} else {
					result = memberType
				}
			}
			return
		}
		if t, ok := a.lookupRefinedExprType(n); ok {
			result = promoteWritableRefType(t, a.fieldExprProvidesWritableRef(n))
			return
		}
		result = promoteWritableRefType(a.analyzeFieldExpr(n), a.fieldExprProvidesWritableRef(n))
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
		if unionType, ok := valueType.(*ErrorUnionType); ok {
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
		}
		if n.UsesDefaultShorthandForm {
			a.analyzeExpr(n.Fallback)
			a.errorf(n.Pos(), "try? ... default requires an error union, got %s", valueType.String())
			if optionalType, ok := valueType.(*OptionalType); ok {
				result = optionalType.Value
			} else {
				result = invalidType
			}
			return
		}
		if optionalType, ok := valueType.(*OptionalType); ok {
			if n.Fallback == nil {
				a.errorf(n.Pos(), "try without else requires an error union, got %s", valueType.String())
				result = optionalType.Value
				return
			}
			fallbackType := a.analyzeExpr(n.Fallback)
			if !IsNeverType(fallbackType) && !AssignableTo(optionalType.Value, fallbackType) {
				a.errorf(n.Pos(), "try fallback expects %s, got %s", optionalType.Value.String(), fallbackType.String())
				a.reportShapeMismatchNotes(n.Pos(), optionalType.Value, fallbackType)
			}
			result = optionalType.Value
			return
		}
		a.errorf(n.Pos(), "try requires a fallible expression, got %s", valueType.String())
		result = invalidType
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
	case *ast.OptionalBindExpr:
		valueType := a.analyzeExpr(n.Value)
		if a.optionalBindSourceTypes != nil {
			if existing, ok := a.optionalBindSourceTypes[n]; !ok {
				a.optionalBindSourceTypes[n] = valueType
			} else if _, existingOK := conditionOptionalBindType(existing); !existingOK {
				if _, valueOK := conditionOptionalBindType(valueType); valueOK {
					a.optionalBindSourceTypes[n] = valueType
				}
			}
		}
		if _, ok := conditionOptionalBindType(valueType); !ok {
			a.errorf(n.Pos(), "let condition requires an optional or nullable reference, got %s", valueType.String())
			result = invalidType
			return
		}
		result = a.namedTypes["bool"]
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
	case *ast.VisitExpr:
		result = a.analyzeVisitExpr(n)
		return
	case *ast.FoldExpr:
		result = a.analyzeFoldExpr(n)
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
		if n.LegacySyntax {
			a.warnf(n.Pos(), "legacy cast syntax `.cast[T]()` is deprecated; use `.cast[T]` instead")
		}
		if n.Origin == ast.CastExprOriginPostfixShorthand {
			if hookSym, ok := a.lookupVisibleCastHook(src, dst); ok {
				a.resolvedCastHooks[n] = hookSym
				if fnType, ok := hookSym.Type.(*FuncType); ok {
					a.recordFunctionPermissionRefs(functionPermissionRefs(fnType))
				}
				result = dst
				return
			}
		}
		if !a.validCast(src, dst) {
			a.errorf(n.Pos(), "invalid cast from %s to %s", src.String(), dst.String())
		}
		if srcRef, ok := src.(*RefType); ok {
			if dstRef, ok := dst.(*RefType); ok && srcRef.Mutable && !dstRef.Mutable {
				cloned := cloneRefType(dstRef)
				cloned.Mutable = true
				dst = cloned
			}
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
		mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
		left, leftSnapshot := a.analyzeExprInConditionAffineScope(n.Value, a.currentScope, n.Cond, true)
		right, rightSnapshot := a.analyzeExprInConditionAffineScope(n.Alt, a.currentScope, n.Cond, false)
		mergedAffine = mergeAffineValueStates(mergedAffine, leftSnapshot.Affine)
		mergedAffine = mergeAffineValueStates(mergedAffine, rightSnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, leftSnapshot.BorrowedOwnerRefs)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, rightSnapshot.BorrowedOwnerRefs)
		a.currentAffineValues = mergedAffine
		a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
		if mergedFunctionValues, ok := a.intersectFunctionValueFlows(leftSnapshot.FunctionValues, rightSnapshot.FunctionValues); ok {
			a.currentFunctionValues = mergedFunctionValues
		}
		merged := MergeTypes(left, right)
		if IsInvalidType(merged) {
			a.errorf(n.Pos(), "ternary branches are incompatible: %s and %s", left.String(), right.String())
		}
		result = merged
		return
	case *ast.AddrOfExpr:
		inner := a.analyzeExpr(n.Operand)
		if a.containsAffineHandleValues(inner, map[string]bool{}) && !isBorrowableAffineOwnerType(inner) {
			if _, ok := a.lookupAffineValueKey(n.Operand); ok {
				if isAffineHandleType(inner) {
					kind := affineHandleKind(inner)
					if kind == "affine value" {
						a.errorf(n.Pos(), "cannot take address of affine value")
					} else {
						a.errorf(n.Pos(), "cannot take address of %s", kind)
					}
				} else {
					a.errorf(n.Pos(), "cannot take address of value containing affine handles")
				}
			}
		}
		result = &RefType{Elem: inner, Mutable: a.exprCanYieldWritableRef(n.Operand), State: RefStateNonNull, Storage: a.inferAddrOfStorage(n.Operand), ExplicitStorage: true}
		return
	case *ast.SpecializeExpr:
		result = a.analyzeSpecializeExpr(n)
		return
	case *ast.StructLitExpr:
		result = a.analyzeStructLiteralExpr(n, nil)
		return
	case *ast.TupleExpr:
		result = a.analyzeTupleExprWithExpected(n, nil)
		return
	case *ast.ParenExpr:
		result = a.analyzeExpr(n.Inner)
		return
	default:
		result = invalidType
		return
	}
}

func (a *Analyzer) analyzeExprBlock(expr *ast.ExprBlock) Type {
	if expr == nil || expr.Value == nil {
		return invalidType
	}
	savedScope := a.currentScope
	a.currentScope = NewScope(savedScope)
	for _, stmt := range expr.Stmts {
		a.analyzeStmt(stmt)
	}
	result := a.analyzeExpr(expr.Value)
	a.currentScope = savedScope
	return result
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
	sym, _, ok := a.lookupVisibleGlobal(ident.Name)
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
	params := genericParamsForFuncType(fnType)
	if len(params) == 0 {
		a.errorf(expr.Pos(), "function %q is not generic", ident.Name)
		for _, arg := range expr.TypeArgs {
			a.resolveType(arg)
		}
		return invalidType
	}
	if len(expr.TypeArgs) != len(params) {
		a.errorf(expr.Pos(), "function %q expects %d %s, got %d", ident.Name, len(params), genericArgLabel(params), len(expr.TypeArgs))
	}
	bindings := make(map[string]Type, len(params))
	limit := len(expr.TypeArgs)
	if len(params) < limit {
		limit = len(params)
	}
	for i := 0; i < limit; i++ {
		bindings[params[i].Name] = a.resolveGenericArgForParam(expr.TypeArgs[i], params[i])
	}
	for i := limit; i < len(expr.TypeArgs); i++ {
		a.resolveType(expr.TypeArgs[i])
	}
	specialized, _ := a.substituteType(fnType, bindings, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return invalidType
	}
	specialized.TypeParams = nil
	specialized.RefStorageParams = nil
	specialized.RefStateParams = nil
	specialized.GenericParams = nil
	return specialized
}

func promoteWritableRefType(t Type, mutable bool) Type {
	if !mutable {
		return t
	}
	ref, ok := t.(*RefType)
	if !ok || ref == nil || ref.Mutable {
		return t
	}
	cloned := cloneRefType(ref)
	cloned.Mutable = true
	return cloned
}

func (a *Analyzer) fieldExprProvidesWritableRef(expr *ast.FieldExpr) bool {
	if expr == nil {
		return false
	}
	if expr.Safe {
		return false
	}
	field, ok := a.lookupField(a.analyzeExpr(expr.Object), expr.Field, expr.Pos())
	if !ok {
		return false
	}
	if ref, ok := field.Type.(*RefType); ok {
		return field.Mutable || ref.Mutable
	}
	return false
}

func (a *Analyzer) exprCanYieldWritableRef(expr ast.Expr) bool {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return false
	}
	switch n := stripped.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, _, ok = a.lookupVisibleGlobal(n.Name); !ok {
				return false
			}
		}
		if _, isRef := sym.Type.(*RefType); !isRef {
			return true
		}
		return sym.Mutable
	case *ast.FieldExpr:
		if n.Safe {
			return false
		}
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok || !field.Mutable {
			return false
		}
		return a.mutationPathWritable(n.Object)
	case *ast.IndexExpr:
		if facts, ok := a.exprFacts[n.Object]; ok && facts.ReadOnly {
			return false
		}
		return a.mutationPathWritable(n.Object)
	default:
		return false
	}
}

func (a *Analyzer) exprCanYieldAddressableValue(expr ast.Expr) bool {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return false
	}
	switch n := stripped.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			sym, _, ok = a.lookupVisibleGlobal(n.Name)
		}
		if !ok || sym == nil {
			return false
		}
		switch sym.Kind {
		case SymbolLocal, SymbolParam, SymbolGlobal, SymbolConst, SymbolExternVar:
			return true
		default:
			return false
		}
	case *ast.FieldExpr:
		if _, ok := a.lookupFieldNoError(a.analyzeExpr(n.Object), n.Field); !ok {
			return false
		}
		if _, ok := a.exprTypes[n.Object].(*RefType); ok {
			return true
		}
		if _, ok := a.analyzeExpr(n.Object).(*RefType); ok {
			return true
		}
		return a.exprCanYieldAddressableValue(n.Object)
	case *ast.IndexExpr:
		objType := a.analyzeExpr(n.Object)
		if _, ok := objType.(*RefType); ok {
			return true
		}
		switch StripAggregateStateType(objType).(type) {
		case *ArrayType, *DArrayType, *DArrayViewType, *ViewType:
			return a.exprCanYieldAddressableValue(n.Object)
		default:
			return false
		}
	default:
		return false
	}
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
	return a.analyzeAllocExprWithExpected(expr, nil)
}

func allocValueExpectedType(expected Type) Type {
	refExpected, ok := expected.(*RefType)
	if !ok || refExpected == nil {
		return nil
	}
	return refExpected.Elem
}

func (a *Analyzer) analyzeAllocExprWithExpected(expr *ast.AllocExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if expr.Owner == nil {
		return a.analyzeScopedPackedAllocExpr(expr)
	}
	if treeType, variant, callExpr, ok := a.treeAllocConstructorInfo(expr.Value); ok {
		owner, ownerType, ownerOK := a.classifyTreeAllocOwnerExpr(expr.Owner)
		if !ownerOK {
			ownerLabel := "<invalid>"
			if ownerType != nil {
				ownerLabel = ownerType.String()
			}
			a.errorf(allocOwnerPos(expr), "tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference, got %s", ownerLabel)
			return a.analyzeTreeAllocExpr(expr, treeType, variant, callExpr)
		}
		if owner.Kind == treeAllocOwnerStore && owner.StoreFamily != nil && treeType.Family != owner.StoreFamily {
			a.errorf(allocOwnerPos(expr), "tree constructor %q requires store %q, got %q", treeType.Name+"."+variant.Name, treeType.Family.StoreType.String(), ownerType.String())
		}
		return a.analyzeTreeAllocExpr(expr, treeType, variant, callExpr)
	}
	if memberType, callExpr, ok := a.treeExactAllocConstructorInfo(expr.Value); ok {
		owner, ownerType, ownerOK := a.classifyTreeAllocOwnerExpr(expr.Owner)
		family, _ := TreeFamilyForMemberType(memberType)
		if !ownerOK {
			ownerLabel := "<invalid>"
			if ownerType != nil {
				ownerLabel = ownerType.String()
			}
			a.errorf(allocOwnerPos(expr), "tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference, got %s", ownerLabel)
			return a.analyzeTreeExactAllocExpr(expr, memberType, callExpr)
		}
		if owner.Kind == treeAllocOwnerStore && owner.StoreFamily != nil && family != owner.StoreFamily {
			a.errorf(allocOwnerPos(expr), "tree constructor %q requires store %q, got %q", memberType.String(), family.StoreType.String(), ownerType.String())
		}
		return a.analyzeTreeExactAllocExpr(expr, memberType, callExpr)
	}
	if isTreeAllocPermExpr(expr.Owner) {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Value.Pos(), "new[perm] expects a tree constructor, got %s", valueType.String())
		return invalidType
	}
	ownerType := a.analyzeExpr(expr.Owner)
	if storeType, ok := ownerType.(*PackedEnumStoreType); ok {
		return a.analyzePackedAllocExpr(expr, storeType)
	}
	if _, ok := ownerType.(*TreeStoreType); ok {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Value.Pos(), "new[%s] expects a tree constructor, got %s", ownerType.String(), valueType.String())
		return invalidType
	}
	ident, ok := expr.Owner.(*ast.Ident)
	if !ok {
		a.errorf(expr.Pos(), "new[...] owner must be a region name, tree store, or packed enum store, got %s", ownerType.String())
		a.analyzeExpr(expr.Value)
		return invalidType
	}
	_, state := a.lookupRegionState(ident.Name)
	if a.currentScope == nil {
		a.errorf(expr.Pos(), "region allocation requires function scope")
		return invalidType
	}
	if sym, ok := a.currentScope.Lookup(ident.Name); !ok || sym.Kind != SymbolRegion {
		a.errorf(expr.Pos(), "new[...] owner must be a region name, tree store, or packed enum store, got %s", ownerType.String())
		a.analyzeExpr(expr.Value)
		return invalidType
	}
	if state.Destroyed {
		a.errorf(expr.Pos(), "cannot allocate from destroyed region %q", ident.Name)
		return invalidType
	}
	valueType := a.analyzeValueExpr(expr.Value, allocValueExpectedType(expected))
	return &RefType{Elem: valueType, State: RefStateNonNull, Storage: RefStorageAny, Region: ident.Name, ExplicitStorage: true}
}

func stripTreeAllocOwnerExpr(expr ast.Expr) ast.Expr {
	for expr != nil {
		if paren, ok := expr.(*ast.ParenExpr); ok {
			expr = paren.Inner
			continue
		}
		return expr
	}
	return nil
}

func isTreeAllocPermExpr(expr ast.Expr) bool {
	ident, ok := stripTreeAllocOwnerExpr(expr).(*ast.Ident)
	return ok && ident != nil && ident.Name == "perm"
}

func (a *Analyzer) classifyTreeAllocOwnerExpr(expr ast.Expr) (treeAllocOwnerBinding, Type, bool) {
	if expr == nil {
		return treeAllocOwnerBinding{}, invalidType, false
	}
	if isTreeAllocPermExpr(expr) {
		return treeAllocOwnerBinding{Kind: treeAllocOwnerPerm}, nil, true
	}
	ownerType := a.analyzeExpr(expr)
	if storeType, ok := ownerType.(*TreeStoreType); ok && storeType != nil {
		return treeAllocOwnerBinding{Kind: treeAllocOwnerStore, StoreFamily: storeType.Family}, ownerType, true
	}
	arenaType := a.namedTypes["Arena"]
	if arenaType == nil {
		return treeAllocOwnerBinding{}, ownerType, false
	}
	stripped := stripTreeAllocOwnerExpr(expr)
	if ident, ok := stripped.(*ast.Ident); ok && SameType(ownerType, arenaType) {
		if regionSym, state := a.lookupRegionState(ident.Name); regionSym != nil {
			if state.Destroyed {
				a.errorf(expr.Pos(), "cannot allocate from destroyed region %q", ident.Name)
				return treeAllocOwnerBinding{}, ownerType, false
			}
			return treeAllocOwnerBinding{Kind: treeAllocOwnerRegion, RegionName: ident.Name}, ownerType, true
		}
		return treeAllocOwnerBinding{Kind: treeAllocOwnerArena}, ownerType, true
	}
	if refType, ok := ownerType.(*RefType); ok && refType != nil && SameType(refType.Elem, arenaType) {
		return treeAllocOwnerBinding{Kind: treeAllocOwnerArena}, ownerType, true
	}
	return treeAllocOwnerBinding{}, ownerType, false
}

func (a *Analyzer) requireActiveTreeConstructorOwner(pos lexer.Pos, treeType *TreeCategoryType, variant *EnumVariant) bool {
	if treeType == nil || variant == nil {
		return false
	}
	return a.requireActiveTreeFamilyConstructorOwner(pos, treeType.Family, treeType.Name+"."+variant.Name)
}

func (a *Analyzer) requireActiveTreeFamilyConstructorOwner(pos lexer.Pos, family *TreeType, constructorName string) bool {
	if family == nil {
		return false
	}
	switch a.currentTreeAllocOwner.Kind {
	case treeAllocOwnerPerm, treeAllocOwnerArena:
		return true
	case treeAllocOwnerRegion:
		if a.currentTreeAllocOwner.RegionName != "" {
			if regionSym, state := a.lookupRegionState(a.currentTreeAllocOwner.RegionName); regionSym == nil || state.Destroyed {
				a.errorf(pos, "tree constructor %q cannot allocate from destroyed region %q", constructorName, a.currentTreeAllocOwner.RegionName)
				return false
			}
		}
		return true
	case treeAllocOwnerStore:
		if a.currentTreeAllocOwner.StoreFamily != nil && family != a.currentTreeAllocOwner.StoreFamily {
			a.errorf(pos, "tree constructor %q requires active store %q, got active store for %q", constructorName, family.StoreType.String(), a.currentTreeAllocOwner.StoreFamily.Name)
			return false
		}
		return true
	default:
		a.errorf(pos, "tree constructor %q requires an active in <owner>: scope or explicit new[owner]", constructorName)
		return false
	}
}

func (a *Analyzer) treeAllocConstructorInfo(expr ast.Expr) (*TreeCategoryType, *EnumVariant, *ast.CallExpr, bool) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		treeType, variant, ok := a.treeConstructorInfoFromFieldExpr(n)
		if !ok {
			return nil, nil, nil, false
		}
		if variant != nil && len(variant.Payload) != 0 {
			return nil, nil, nil, false
		}
		return treeType, variant, nil, true
	case *ast.CallExpr:
		treeType, variant, ok := a.treeConstructorCall(n)
		return treeType, variant, n, ok
	default:
		return nil, nil, nil, false
	}
}

func (a *Analyzer) treeExactAllocConstructorInfo(expr ast.Expr) (Type, *ast.CallExpr, bool) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		memberType, ok := a.treeExactMemberTypeFromFieldExpr(n)
		if !ok {
			return nil, nil, false
		}
		if len(treeExactMemberFieldDecls(memberType)) != 0 {
			return nil, nil, false
		}
		return memberType, nil, true
	case *ast.CallExpr:
		memberType, ok := a.treeExactMemberConstructorCall(n)
		if !ok {
			return nil, nil, false
		}
		return memberType, n, true
	default:
		return nil, nil, false
	}
}

func (a *Analyzer) analyzeTreeAllocExpr(expr *ast.AllocExpr, treeType *TreeCategoryType, variant *EnumVariant, callExpr *ast.CallExpr) Type {
	if treeType == nil {
		if expr != nil && expr.Value != nil {
			a.analyzeExpr(expr.Value)
		}
		return invalidType
	}
	if variant == nil {
		if callExpr != nil {
			for _, arg := range callExpr.Args {
				a.analyzeExpr(arg)
			}
		}
		return invalidType
	}
	if callExpr == nil {
		if treeType.Family != nil && treeType.Family.Decl != nil && len(treeType.Family.Decl.Common) != 0 {
			a.errorf(expr.Pos(), "tree constructor %q requires explicit common fields; use call syntax with named arguments", treeType.Name+"."+variant.Name)
			return invalidType
		}
		return treeType
	}
	return a.analyzeTreeConstructorCallExpr(callExpr, treeType, variant)
}

func (a *Analyzer) analyzeTreeExactAllocExpr(expr *ast.AllocExpr, memberType Type, callExpr *ast.CallExpr) Type {
	if memberType == nil {
		if expr != nil && expr.Value != nil {
			a.analyzeExpr(expr.Value)
		}
		return invalidType
	}
	if callExpr == nil {
		if len(treeExactMemberFieldDecls(memberType)) != 0 {
			a.errorf(expr.Pos(), "tree constructor %q requires explicit constructor arguments", memberType.String())
			return invalidType
		}
		return memberType
	}
	return a.analyzeTreeExactMemberConstructorCallExpr(callExpr, memberType)
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
	case *ast.MoveExpr:
		return a.regionRefStateForExpr(n.Operand)
	case *ast.AddrOfExpr:
		return a.regionRefStateForExpr(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil {
			return regionRefState{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return regionRefState{}, false
		}
		stateSym := sym
		state, ok := a.currentRegionRefs[sym]
		if (!ok || !hasRegionProvenance(state)) && sym != nil {
			root := symbolAliasRoot(sym)
			if root != nil && root != sym {
				stateSym = root
				state, ok = a.currentRegionRefs[root]
			}
		}
		if !ok {
			return regionRefState{}, false
		}
		state = a.canonicalizeStoredRegionRefBinding(stateSym, state)
		return state, true
	case *ast.AllocExpr:
		if n.Owner != nil {
			if _, _, _, ok := a.treeAllocConstructorInfo(n.Value); ok {
				if isTreeAllocPermExpr(n.Owner) {
					return regionRefState{}, false
				}
				if owner, _, ownerOK := a.classifyTreeAllocOwnerExpr(n.Owner); ownerOK && owner.Kind != treeAllocOwnerNone {
					return regionRefState{}, false
				}
			}
			if _, _, ok := a.treeExactAllocConstructorInfo(n.Value); ok {
				if isTreeAllocPermExpr(n.Owner) {
					return regionRefState{}, false
				}
				if owner, _, ownerOK := a.classifyTreeAllocOwnerExpr(n.Owner); ownerOK && owner.Kind != treeAllocOwnerNone {
					return regionRefState{}, false
				}
			}
			if ownerType := a.exprTypes[n.Owner]; ownerType != nil {
				if _, ok := ownerType.(*PackedEnumStoreType); ok {
					ownerState, ownerOK := a.regionRefStateForExpr(n.Owner)
					if !ownerOK {
						return regionRefState{}, false
					}
					callExpr, ok := n.Value.(*ast.CallExpr)
					if !ok {
						return ownerState, true
					}
					enumType, variant, ok := a.enumConstructorCall(callExpr)
					if !ok || enumType == nil || variant == nil {
						return ownerState, true
					}
					orderedArgs, _, ok := a.resolvePackedEnumConstructorArgs(callExpr, enumType, variant)
					if !ok {
						return ownerState, true
					}
					fieldStates := map[string]regionRefState{}
					states := []regionRefState{ownerState}
					for i := 0; i < len(orderedArgs) && i < len(variant.Payload); i++ {
						fieldState, ok := a.regionRefStateForExpr(orderedArgs[i])
						if !ok || !hasRegionProvenance(fieldState) {
							continue
						}
						key := moveBindVariantFieldKey(variant, i)
						fieldStates[key] = fieldState
						states = append(states, fieldState)
					}
					return mergeRegionRefStatesWithExplicitFields(states, fieldStates)
				}
			}
			if ownerType := a.analyzeExpr(n.Owner); ownerType != nil {
				if _, ok := ownerType.(*PackedEnumStoreType); ok {
					return a.regionRefStateForExpr(n.Owner)
				}
			}
		}
		ident, ok := n.Owner.(*ast.Ident)
		if !ok {
			return regionRefState{}, false
		}
		sym, state := a.lookupRegionState(ident.Name)
		if sym == nil || state.Destroyed {
			return regionRefState{}, false
		}
		return regionRefStateFromDependency(sym, state.Generation), true
	case *ast.StructLitExpr:
		actual := a.exprTypes[n]
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return regionRefState{}, false
		}
		fieldStates := map[string]regionRefState{}
		unionStates := make([]regionRefState, 0, len(fields))
		for i, field := range fields {
			if i >= len(n.Args) {
				break
			}
			fieldState, ok := a.regionRefStateForExpr(n.Args[i])
			if !ok || !hasRegionProvenance(fieldState) {
				continue
			}
			fieldStates[field.Name] = fieldState
			unionStates = append(unionStates, fieldState)
		}
		if len(unionStates) == 0 {
			return regionRefState{}, false
		}
		return mergeRegionRefStatesWithExplicitFields(unionStates, fieldStates)
	case *ast.ListLitExpr:
		elemStates := make([]regionRefState, 0, len(n.Elems))
		fieldStates := map[string]regionRefState{}
		for i, elem := range n.Elems {
			if state, ok := a.regionRefStateForExpr(elem); ok && hasRegionProvenance(state) {
				elemStates = append(elemStates, state)
				fieldStates[regionIndexFieldKey(int64(i))] = state
			}
		}
		merged, ok := mergeRegionRefStates(elemStates...)
		if !ok {
			return regionRefState{}, false
		}
		if len(fieldStates) != 0 {
			fieldStates[regionAnyIndexFieldKey()] = cloneRegionRefState(merged)
			merged.Fields = fieldStates
			merged.PackedStoreSummaryKnown = false
		}
		return withPackedStoreProvenanceSummary(merged), true
	case *ast.FieldExpr:
		if enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(n); ok && enumType != nil && variant != nil && enumType.Packed && len(variant.Payload) == 0 {
			if state, ok := a.activePackedStoreRegionState(enumType); ok {
				return state, true
			}
			return regionRefState{}, false
		}
		if n.Field == "tags" {
			if storeType, ok := a.exprTypes[n.Object].(*PackedEnumStoreType); ok && IsFrozenPackedEnumStoreType(storeType) {
				return a.regionRefStateForExpr(n.Object)
			}
		}
		state, ok := a.regionRefStateForExpr(n.Object)
		if !ok {
			return regionRefState{}, false
		}
		return projectRegionFieldState(state, n.Field)
	case *ast.IndexExpr:
		if viewType, ok := a.exprTypes[n.Object].(*DArrayViewType); ok && viewType.SurfaceName == "packedtags" {
			return a.regionRefStateForExpr(n.Object)
		}
		resultType := a.exprTypes[n]
		if resultType == nil || !a.typeCanContainRegionRefs(resultType, map[string]bool{}) {
			return regionRefState{}, false
		}
		state, ok := a.regionRefStateForExpr(n.Object)
		if !ok || !hasRegionDependencies(state) {
			if !ok || len(state.Fields) == 0 {
				return regionRefState{}, false
			}
		}
		if fieldState, ok := projectRegionIndexState(state, n.Index, a.evalConstExpr); ok {
			return fieldState, true
		}
		return cloneRegionRefState(state), true
	case *ast.SliceExpr:
		if viewType, ok := a.exprTypes[n.Object].(*DArrayViewType); ok && viewType.SurfaceName == "packedtags" {
			return a.regionRefStateForExpr(n.Object)
		}
		resultType := a.exprTypes[n]
		if resultType == nil || !a.typeCanContainRegionRefs(resultType, map[string]bool{}) {
			return regionRefState{}, false
		}
		state, ok := a.regionRefStateForExpr(n.Object)
		if !ok || !hasRegionProvenance(state) {
			return regionRefState{}, false
		}
		return summarizeRegionIndexStates(state)
	case *ast.TryExpr:
		return a.regionRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.UnwrapElseExpr:
		return a.regionRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.TernaryExpr:
		left, leftOK := a.regionRefStateForExpr(n.Value)
		right, rightOK := a.regionRefStateForExpr(n.Alt)
		if !leftOK && !rightOK {
			return regionRefState{}, false
		}
		if leftOK && rightOK {
			return mergeRegionRefStates(left, right)
		}
		if leftOK {
			return cloneRegionRefState(left), true
		}
		return cloneRegionRefState(right), true
	case *ast.CallExpr:
		if state, ok := a.regionRefStateForProofCarryingViewCall(n); ok {
			return state, true
		}
		if freezeStoreArg, ok := a.freezeStoreArg(n); ok {
			return a.regionRefStateForExpr(freezeStoreArg)
		}
		if _, ok := a.packedStoreConstructorCall(n); ok {
			return regionRefState{}, false
		}
		if _, ok := a.treeStoreConstructorCall(n); ok {
			return regionRefState{}, false
		}
		if enumType, variant, ok := a.enumConstructorCall(n); ok && enumType != nil && variant != nil {
			states := make([]regionRefState, 0, len(n.Args))
			fieldStates := map[string]regionRefState{}
			if enumType.Packed {
				orderedArgs, commonArgs, ok := a.resolvePackedEnumConstructorArgs(n, enumType, variant)
				if !ok {
					return regionRefState{}, false
				}
				for i := 0; i < len(orderedArgs) && i < len(variant.Payload); i++ {
					state, ok := a.regionRefStateForExpr(orderedArgs[i])
					if !ok || !hasRegionProvenance(state) {
						continue
					}
					states = append(states, state)
					fieldStates[moveBindVariantFieldKey(variant, i)] = state
				}
				for name, arg := range commonArgs {
					state, ok := a.regionRefStateForExpr(arg)
					if !ok || !hasRegionProvenance(state) {
						continue
					}
					states = append(states, state)
					fieldStates[name] = state
				}
			} else {
				orderedArgs, ok := a.resolveEnumConstructorArgs(n, enumType, variant)
				if !ok {
					return regionRefState{}, false
				}
				for _, arg := range orderedArgs {
					if state, ok := a.regionRefStateForExpr(arg); ok && hasRegionProvenance(state) {
						states = append(states, state)
					}
				}
				for i := 0; i < len(orderedArgs) && i < len(variant.Payload); i++ {
					if state, ok := a.regionRefStateForExpr(orderedArgs[i]); ok && hasRegionProvenance(state) {
						fieldStates[moveBindVariantFieldKey(variant, i)] = state
					}
				}
			}
			return mergeRegionRefStatesWithExplicitFields(states, fieldStates)
		}
		fnType, _ := a.exprTypes[n.Func].(*FuncType)
		if fnType == nil {
			if analyzed := a.analyzeExpr(n.Func); analyzed != nil {
				fnType, _ = analyzed.(*FuncType)
			}
		}
		if fnType != nil {
			if !fnType.ReturnProvenanceKnown {
				a.inferFuncReturnProvenanceForExpr(n.Func, fnType)
			}
			if fnType.ReturnProvenanceKnown {
				return a.instantiateReturnProvenance(fnType.ReturnProvenance, n.Args)
			}
		}
		return regionRefState{}, false
	default:
		return regionRefState{}, false
	}
}

func (a *Analyzer) regionRefStateForProofCarryingViewCall(call *ast.CallExpr) (regionRefState, bool) {
	if a == nil || call == nil || len(call.Args) == 0 {
		return regionRefState{}, false
	}
	sourceState, ok := a.regionRefStateForExpr(call.Args[0])
	if !ok || !hasRegionProvenance(sourceState) {
		return regionRefState{}, false
	}
	summarized, summaryOK := summarizeRegionIndexStates(sourceState)
	if !summaryOK {
		summarized = cloneRegionRefState(sourceState)
	}
	switch callIdentName(call) {
	case "readonly":
		return cloneRegionRefState(sourceState), true
	case "split_at":
		state := cloneRegionRefState(summarized)
		state.Fields = map[string]regionRefState{
			"left":  cloneRegionRefState(summarized),
			"right": cloneRegionRefState(summarized),
		}
		state.PackedStoreSummaryKnown = false
		return withPackedStoreProvenanceSummary(state), true
	case "chunks_exact":
		state := cloneRegionRefState(summarized)
		state.Fields = map[string]regionRefState{
			"source": cloneRegionRefState(summarized),
		}
		state.PackedStoreSummaryKnown = false
		return withPackedStoreProvenanceSummary(state), true
	case "reduce_sum":
		return regionRefState{}, true
	default:
		return regionRefState{}, false
	}
}

func (a *Analyzer) regionRefStateForRecoveredExpr(value ast.Expr, fallback ast.Expr) (regionRefState, bool) {
	valueState, valueOK := a.regionRefStateForExpr(value)
	if fallback == nil || a.exprDefinitelyNever(fallback) {
		if !valueOK {
			return regionRefState{}, false
		}
		return cloneRegionRefState(valueState), true
	}
	fallbackState, fallbackOK := a.regionRefStateForExpr(fallback)
	if !valueOK && !fallbackOK {
		return regionRefState{}, false
	}
	if valueOK && fallbackOK {
		return mergeRegionRefStates(valueState, fallbackState)
	}
	if valueOK {
		return cloneRegionRefState(valueState), true
	}
	return cloneRegionRefState(fallbackState), true
}

func (a *Analyzer) exprDefinitelyNever(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	t := a.exprTypes[expr]
	if t == nil {
		return false
	}
	return IsNeverType(t)
}

func (a *Analyzer) inferFuncReturnProvenanceForExpr(expr ast.Expr, fnType *FuncType) {
	if fnType == nil || fnType.ReturnProvenanceKnown || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.inferFuncReturnProvenanceForExpr(n.Inner, fnType)
	case *ast.FieldExpr:
		if fieldExpr, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok {
			a.inferFuncReturnProvenanceForExpr(fieldExpr, fnType)
		}
	case *ast.Ident:
		if a.inferFuncReturnProvenanceForLocalIdent(n, fnType) {
			return
		}
		if a.globalScope == nil {
			return
		}
		sym, _, ok := a.lookupVisibleGlobal(n.Name)
		if !ok {
			return
		}
		fnDecl, _ := sym.Node.(*ast.FuncDecl)
		if fnDecl == nil {
			return
		}
		a.inferFuncReturnProvenance(fnDecl, fnType)
	case *ast.SpecializeExpr:
		a.inferFuncReturnProvenanceForExpr(n.Operand, fnType)
	}
}

func (a *Analyzer) inferFuncReturnProvenanceForLocalIdent(ident *ast.Ident, fnType *FuncType) bool {
	if ident == nil || fnType == nil || a.currentScope == nil {
		return false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return false
	}
	if sourceType, ok := a.lookupCurrentFunctionValueType(sym); ok && sourceType != fnType && hasRegionProvenance(sourceType.ReturnProvenance) {
		fnType.ReturnProvenance = cloneRegionRefState(sourceType.ReturnProvenance)
		fnType.ReturnProvenanceKnown = true
		return true
	}
	if sourceType, ok := sym.Type.(*FuncType); ok && sourceType != fnType && hasRegionProvenance(sourceType.ReturnProvenance) {
		fnType.ReturnProvenance = cloneRegionRefState(sourceType.ReturnProvenance)
		fnType.ReturnProvenanceKnown = true
		return true
	}
	if sym.Kind != SymbolLocal || sym.Mutable {
		return false
	}
	decl, ok := sym.Node.(*ast.VarDeclStmt)
	if !ok || decl.Value == nil {
		return false
	}
	a.inferFuncReturnProvenanceForExpr(decl.Value, fnType)
	return fnType.ReturnProvenanceKnown
}

func (a *Analyzer) inferFuncReturnBorrowedOwnerRefsForExpr(expr ast.Expr, fnType *FuncType) {
	if fnType == nil || fnType.ReturnBorrowedOwnerRefsKnown || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.inferFuncReturnBorrowedOwnerRefsForExpr(n.Inner, fnType)
	case *ast.FieldExpr:
		if fieldExpr, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok {
			a.inferFuncReturnBorrowedOwnerRefsForExpr(fieldExpr, fnType)
		}
	case *ast.Ident:
		if a.inferFuncReturnBorrowedOwnerRefsForLocalIdent(n, fnType) {
			return
		}
		if a.globalScope == nil {
			return
		}
		sym, _, ok := a.lookupVisibleGlobal(n.Name)
		if !ok {
			return
		}
		fnDecl, _ := sym.Node.(*ast.FuncDecl)
		if fnDecl == nil {
			return
		}
		a.inferFuncReturnBorrowedOwnerRefs(fnDecl, fnType)
	case *ast.SpecializeExpr:
		a.inferFuncReturnBorrowedOwnerRefsForExpr(n.Operand, fnType)
	}
}

func (a *Analyzer) inferFuncReturnBorrowedOwnerRefsForLocalIdent(ident *ast.Ident, fnType *FuncType) bool {
	if ident == nil || fnType == nil || a.currentScope == nil {
		return false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return false
	}
	if sourceType, ok := a.lookupCurrentFunctionValueType(sym); ok && sourceType != fnType && hasBorrowedOwnerRefSummary(sourceType.ReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(sourceType.ReturnBorrowedOwnerRefs)
		fnType.ReturnBorrowedOwnerRefsKnown = true
		return true
	}
	if sourceType, ok := sym.Type.(*FuncType); ok && sourceType != fnType && hasBorrowedOwnerRefSummary(sourceType.ReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(sourceType.ReturnBorrowedOwnerRefs)
		fnType.ReturnBorrowedOwnerRefsKnown = true
		return true
	}
	if sym.Kind != SymbolLocal || sym.Mutable {
		return false
	}
	decl, ok := sym.Node.(*ast.VarDeclStmt)
	if !ok || decl.Value == nil {
		return false
	}
	a.inferFuncReturnBorrowedOwnerRefsForExpr(decl.Value, fnType)
	return fnType.ReturnBorrowedOwnerRefsKnown
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
			return a.analyzePackedAllocExpr(expr, PackedEnumStoreWithState(enumType.StoreType, a.namedTypes["Local"]))
		}
		return enumType
	}
	return a.analyzePackedAllocExpr(expr, storeType)
}

func (a *Analyzer) analyzePackedAllocExpr(expr *ast.AllocExpr, storeType *PackedEnumStoreType) Type {
	if storeType == nil {
		a.errorf(expr.Pos(), "missing packed enum store type")
		return invalidType
	}
	if !IsLocalPackedEnumStoreType(storeType) {
		a.errorf(allocOwnerPos(expr), "packed enum allocation requires local store type %q, got %s", PackedEnumStoreWithState(storeType, a.namedTypes["Local"]).String(), storeType.String())
	}
	if fieldExpr, ok := expr.Value.(*ast.FieldExpr); ok {
		ident, ok := fieldExpr.Object.(*ast.Ident)
		if ok {
			base, _, ok := a.lookupVisibleType(ident.Name)
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
			var actual Type
			orderedArgs[i], actual, ok = a.analyzePackedEnumConstructorArg(orderedArgs[i], variant, i, enumType.Name)
			if !ok {
				label := variant.PayloadLabel(i)
				if label != "" {
					a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d (%s) to %q expects %s, got %s", i+1, label, enumType.Name+"."+variant.Name, variant.Payload[i].String(), actual.String())
				} else {
					a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d to %q expects %s, got %s", i+1, enumType.Name+"."+variant.Name, variant.Payload[i].String(), actual.String())
				}
			}
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
		var actual Type
		commonArgs[commonDecl.Name], actual = a.analyzeCallLikeValueExpr(arg, field.Type)
		if !AssignableTo(field.Type, actual) {
			a.errorf(commonArgs[commonDecl.Name].Pos(), "packed enum common field %q for %q expects %s, got %s", commonDecl.Name, enumType.Name+"."+variant.Name, field.Type.String(), actual.String())
		}
		a.consumeAffineValueExpr(commonArgs[commonDecl.Name], field.Type, "move into enum common field "+strconv.Quote(commonDecl.Name))
	}
	return enumType
}

func (a *Analyzer) analyzePackedEnumConstructorArg(expr ast.Expr, variant *EnumVariant, index int, enumName string) (ast.Expr, Type, bool) {
	if variant == nil || index < 0 || index >= len(variant.Payload) {
		actual := a.analyzeExpr(expr)
		return expr, actual, false
	}
	expected := variant.Payload[index]
	if tailIndex, ok := variant.TailPayloadIndex(); ok && tailIndex == index {
		if expectedView, ok := expected.(*DArrayViewType); ok {
			actual, ok := a.analyzePackedEnumTailPayloadArg(expr, expectedView, a.enumConstructorMoveReason(enumName, variant, index))
			return expr, actual, ok
		}
	}
	rewritten, actual := a.analyzeCallLikeValueExpr(expr, expected)
	ok := AssignableTo(expected, actual)
	a.consumeAffineValueExpr(rewritten, expected, a.enumConstructorMoveReason(enumName, variant, index))
	return rewritten, actual, ok
}

func (a *Analyzer) analyzePackedEnumTailPayloadArg(expr ast.Expr, expected *DArrayViewType, moveReason string) (Type, bool) {
	if expected == nil {
		actual := a.analyzeExpr(expr)
		return actual, false
	}
	if list, ok := expr.(*ast.ListLitExpr); ok {
		for _, elem := range list.Elems {
			actualElem := a.analyzeValueExpr(elem, expected.Elem)
			if !AssignableTo(expected.Elem, actualElem) {
				a.errorf(elem.Pos(), "tail payload element expects %s, got %s", expected.Elem.String(), actualElem.String())
			}
			a.consumeAffineValueExpr(elem, expected.Elem, moveReason)
		}
		arrayType := &ArrayType{Elem: expected.Elem, Size: strconv.Itoa(len(list.Elems)), HasConstSize: true, ConstSize: int64(len(list.Elems))}
		a.exprTypes[list] = arrayType
		return arrayType, true
	}
	actual := a.analyzeValueExpr(expr, expected)
	ok := AssignableTo(expected, actual) || packedEnumTailPayloadSourceCompatible(expected, actual)
	a.consumeAffineValueExpr(expr, expected, moveReason)
	return actual, ok
}

func packedEnumTailPayloadSourceCompatible(expected *DArrayViewType, actual Type) bool {
	if expected == nil || actual == nil {
		return false
	}
	if actualView, ok := actual.(*DArrayViewType); ok {
		return SameType(expected.Elem, actualView.Elem)
	}
	if actualView, ok := actual.(*ViewType); ok {
		return SameType(expected.Elem, actualView.Elem)
	}
	return false
}

func (a *Analyzer) analyzeStructLiteralExpr(expr *ast.StructLitExpr, expected Type) Type {
	targetType := a.structLiteralTargetType(expr, expected)
	base, bindings, ok := structLiteralBaseAndBindings(targetType)
	if !ok || base == nil {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		if !IsInvalidType(targetType) {
			a.errorf(expr.Pos(), "type %q is not a struct", targetType.String())
		} else {
			a.errorf(expr.Pos(), "unknown struct %q", expr.Name)
		}
		return invalidType
	}
	a.analyzeStructLiteralArgs(expr, base, bindings)
	if len(base.NamedStateCases) == 0 {
		return targetType
	}
	desiredState, ok := namedStateCurrentArg(targetType)
	if !ok || desiredState == nil {
		return targetType
	}
	fullState := fullNamedStateType(base)
	if sameNamedStateType(desiredState, fullState) {
		inferredState := a.inferStructLiteralNamedState(expr, base)
		if inferredState != nil && !sameNamedStateType(inferredState, fullState) {
			return instantiateNamedStateStructLiteralType(base, targetType, inferredState)
		}
		return targetType
	}
	if !a.proveStructLiteralNamedState(expr, base, desiredState) {
		a.errorf(expr.Pos(), "struct literal %q does not satisfy derived state %s", expr.Name, desiredState.String())
	}
	return targetType
}

func (a *Analyzer) structLiteralTargetType(expr *ast.StructLitExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if len(expr.TypeArgs) != 0 {
		return a.resolveType(&ast.GenericType{Position: expr.Position, Name: expr.Name, Args: expr.TypeArgs})
	}
	if base, _, ok := structLiteralBaseAndBindings(expected); ok && structTypeMatchesLiteralName(base, expr.Name) {
		return expected
	}
	if t, _, ok := a.lookupVisibleType(expr.Name); ok {
		return DefaultStatefulType(t)
	}
	return invalidType
}

func structTypeMatchesLiteralName(base *StructType, name string) bool {
	if base == nil {
		return false
	}
	return base.Name == name || strings.HasSuffix(base.Name, "."+name)
}

func structLiteralBaseAndBindings(t Type) (*StructType, map[string]Type, bool) {
	t = StripAggregateStateType(t)
	switch tt := t.(type) {
	case *StructType:
		return tt, nil, true
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil {
			return nil, nil, false
		}
		return base, genericBindingsForStructInstance(base, tt.Args), true
	default:
		return nil, nil, false
	}
}

func instantiateNamedStateStructLiteralType(base *StructType, template Type, state Type) Type {
	if base == nil || state == nil {
		return template
	}
	template = StripAggregateStateType(template)
	gi, ok := template.(*GenericInstanceType)
	if !ok || gi == nil {
		if defaultGI, ok := DefaultNamedStateType(base).(*GenericInstanceType); ok {
			gi = defaultGI
		} else {
			return template
		}
	}
	idx := namedStateArgIndex(base)
	if idx < 0 || idx >= len(gi.Args) {
		return template
	}
	args := append([]Type(nil), gi.Args...)
	args[idx] = state
	return &GenericInstanceType{Name: gi.Name, Base: gi.Base, Args: args}
}

func (a *Analyzer) inferStructLiteralNamedState(expr *ast.StructLitExpr, base *StructType) Type {
	if expr == nil || base == nil || len(base.NamedStateCases) == 0 {
		return nil
	}
	fieldValues, ok := structLiteralFieldValues(expr, base)
	if !ok {
		return fullNamedStateType(base)
	}
	trueStates := make([]string, 0, len(base.NamedStateCases))
	for _, stateName := range base.NamedStateCases {
		proven, value := a.evaluateDerivedStateForFields(base, stateName, fieldValues)
		if !proven {
			return fullNamedStateType(base)
		}
		if value {
			trueStates = append(trueStates, stateName)
		}
	}
	if len(trueStates) == 1 {
		return newNamedStateType(base.Name, base.NamedStateCases, trueStates)
	}
	if len(trueStates) == 0 {
		a.errorf(expr.Pos(), "struct literal %q does not satisfy any derived state", expr.Name)
		return fullNamedStateType(base)
	}
	a.errorf(expr.Pos(), "struct literal %q satisfies multiple derived states: %s", expr.Name, strings.Join(trueStates, ", "))
	return fullNamedStateType(base)
}

func (a *Analyzer) proveStructLiteralNamedState(expr *ast.StructLitExpr, base *StructType, desired Type) bool {
	if expr == nil || base == nil || desired == nil {
		return false
	}
	desiredCases, _, ok := namedStateTypeCases(desired)
	if !ok {
		return false
	}
	if len(desiredCases) != 1 {
		return sameNamedStateType(desired, fullNamedStateType(base))
	}
	fieldValues, ok := structLiteralFieldValues(expr, base)
	if !ok {
		return false
	}
	proven, value := a.evaluateDerivedStateForFields(base, desiredCases[0], fieldValues)
	if !proven || !value {
		return false
	}
	for _, other := range base.NamedStateCases {
		if other == desiredCases[0] {
			continue
		}
		if otherProven, otherValue := a.evaluateDerivedStateForFields(base, other, fieldValues); otherProven && otherValue {
			a.errorf(expr.Pos(), "struct literal %q satisfies multiple derived states including %q and %q", expr.Name, desiredCases[0], other)
			return false
		}
	}
	return true
}

func structLiteralFieldValues(expr *ast.StructLitExpr, base *StructType) (map[string]ast.Expr, bool) {
	if expr == nil || base == nil || base.Decl == nil || len(expr.Args) != len(base.Decl.Fields) {
		return nil, false
	}
	fieldValues := make(map[string]ast.Expr, len(base.Decl.Fields))
	for i, fieldDecl := range base.Decl.Fields {
		fieldValues[fieldDecl.Name] = expr.Args[i]
	}
	return fieldValues, true
}

func (a *Analyzer) evaluateDerivedStateForFields(base *StructType, stateName string, fieldValues map[string]ast.Expr) (bool, bool) {
	if base == nil || fieldValues == nil || base.DerivedStateMap == nil {
		return false, false
	}
	derived := base.DerivedStateMap[stateName]
	if derived == nil || derived.Condition == nil {
		return false, false
	}
	substituted, ok := substituteDerivedStateFieldExpr(derived.Condition, fieldValues)
	if !ok {
		return false, false
	}
	value, ok := a.evalConstBoolExpr(substituted)
	return ok, value
}

func substituteDerivedStateFieldExpr(expr ast.Expr, fieldValues map[string]ast.Expr) (ast.Expr, bool) {
	if path, ok := derivedStateSelfFieldPath(expr); ok {
		if len(path) == 0 {
			return nil, false
		}
		baseExpr, ok := fieldValues[path[0]]
		if !ok || baseExpr == nil {
			return nil, false
		}
		out := cloneDerivedStateExpr(baseExpr)
		for _, field := range path[1:] {
			out = &ast.FieldExpr{Position: expr.Pos(), Object: out, Field: field}
		}
		return out, true
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		inner, ok := substituteDerivedStateFieldExpr(n.Inner, fieldValues)
		if !ok {
			return nil, false
		}
		return &ast.ParenExpr{Position: n.Position, Inner: inner}, true
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.NullLit:
		return cloneDerivedStateExpr(expr), true
	case *ast.UnaryExpr:
		operand, ok := substituteDerivedStateFieldExpr(n.Operand, fieldValues)
		if !ok {
			return nil, false
		}
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: operand}, true
	case *ast.BinaryExpr:
		left, ok := substituteDerivedStateFieldExpr(n.Left, fieldValues)
		if !ok {
			return nil, false
		}
		right, ok := substituteDerivedStateFieldExpr(n.Right, fieldValues)
		if !ok {
			return nil, false
		}
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: left, Right: right}, true
	default:
		return nil, false
	}
}

func derivedStateSelfFieldPath(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "self" {
			return nil, true
		}
		return nil, false
	case *ast.FieldExpr:
		path, ok := derivedStateSelfFieldPath(n.Object)
		if !ok {
			return nil, false
		}
		return append(path, n.Field), true
	default:
		return nil, false
	}
}

func cloneDerivedStateExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.Ident:
		return &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.IntLit:
		return &ast.IntLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix, IsHex: n.IsHex}
	case *ast.FloatLit:
		return &ast.FloatLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix}
	case *ast.StringLit:
		return &ast.StringLit{Position: n.Position, Value: n.Value}
	case *ast.CharLit:
		return &ast.CharLit{Position: n.Position, Value: n.Value}
	case *ast.BoolLit:
		return &ast.BoolLit{Position: n.Position, Value: n.Value}
	case *ast.NullLit:
		return &ast.NullLit{Position: n.Position}
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: cloneDerivedStateExpr(n.Inner)}
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: n.Position, Object: cloneDerivedStateExpr(n.Object), Field: n.Field}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: cloneDerivedStateExpr(n.Operand)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: cloneDerivedStateExpr(n.Left), Right: cloneDerivedStateExpr(n.Right)}
	default:
		return expr
	}
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
		var actual Type
		expr.Args[i], actual = a.analyzeCallLikeValueExpr(expr.Args[i], expected)
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
	base, _, ok := a.lookupVisibleType(ident.Name)
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
	base, _, ok := a.lookupVisibleType(ident.Name)
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

func (a *Analyzer) packedEnumTagExprType(expr *ast.FieldExpr) (Type, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, _, ok := a.lookupVisibleType(ident.Name)
	if !ok {
		return nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok || expr.Field != "Tag" {
		return nil, false
	}
	if !enumType.Packed || enumType.TagType == nil {
		a.errorf(expr.Pos(), "enum %q has no nested tag type", enumType.Name)
		return invalidType, true
	}
	return enumType.TagType, true
}

func shorthandMemberName(parts []string) string {
	return strings.Join(parts, ".")
}

func shorthandMemberDisplay(parts []string) string {
	return "." + shorthandMemberName(parts)
}

func contextualShorthandExpr(expr ast.Expr) (*ast.ShorthandMemberExpr, bool) {
	switch n := expr.(type) {
	case *ast.ShorthandMemberExpr:
		return n, true
	case *ast.ParenExpr:
		return contextualShorthandExpr(n.Inner)
	default:
		return nil, false
	}
}

func (a *Analyzer) analyzeShorthandMemberExpr(expr *ast.ShorthandMemberExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if expected == nil {
		a.errorf(expr.Pos(), "shorthand member %q requires an expected const enum type", shorthandMemberDisplay(expr.Parts))
		return invalidType
	}
	if IsInvalidType(expected) {
		return invalidType
	}
	constEnumType, ok := expected.(*ConstEnumType)
	if !ok || constEnumType == nil {
		a.errorf(expr.Pos(), "shorthand member %q requires an expected const enum type", shorthandMemberDisplay(expr.Parts))
		return invalidType
	}
	memberName := shorthandMemberName(expr.Parts)
	if _, ok := constEnumType.Member(memberName); !ok {
		a.errorf(expr.Pos(), "const enum %q has no member %q", constEnumType.Name, memberName)
		return invalidType
	}
	return constEnumType
}

func (a *Analyzer) analyzeContextualShorthandValueExpr(expr ast.Expr, expected Type) (Type, bool) {
	switch n := expr.(type) {
	case *ast.ShorthandMemberExpr:
		result := a.analyzeShorthandMemberExpr(n, expected)
		a.recordAnalyzedExprType(n, result)
		return result, true
	case *ast.ParenExpr:
		innerType, ok := a.analyzeContextualShorthandValueExpr(n.Inner, expected)
		if !ok {
			return nil, false
		}
		a.recordAnalyzedExprType(n, innerType)
		return innerType, true
	default:
		return nil, false
	}
}

func (a *Analyzer) treeCategoryKindExprType(expr *ast.FieldExpr) (Type, bool) {
	fullName, ok := qualifiedTypePathFromExpr(expr)
	if !ok || expr.Field != "Kind" {
		return nil, false
	}
	base, _, ok := a.lookupVisibleType(fullName)
	if ok {
		if constEnumType, ok := base.(*ConstEnumType); ok {
			return constEnumType, true
		}
	}
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, false
	}
	base, _, ok = a.lookupVisibleType(baseName)
	if !ok {
		return nil, false
	}
	switch tt := base.(type) {
	case *TreeCategoryType:
		if tt.KindType == nil {
			a.errorf(expr.Pos(), "tree category %q has no nested kind type", tt.Name)
			return invalidType, true
		}
		return tt.KindType, true
	case *TreeNodeType:
		if tt.KindType == nil {
			a.errorf(expr.Pos(), "tree root %q has no nested kind type", tt.Name)
			return invalidType, true
		}
		return tt.KindType, true
	default:
		return nil, false
	}
}

func (a *Analyzer) constEnumMemberInfoForExpr(expr ast.Expr) (*ConstEnumType, string, *ConstEnumMember, bool) {
	fullName, ok := qualifiedTypePathFromExpr(expr)
	if !ok || fullName == "" {
		return nil, "", nil, false
	}
	parts := strings.Split(fullName, ".")
	var matchedType *ConstEnumType
	var matchedMemberName string
	for i := len(parts) - 1; i >= 1; i-- {
		baseName := strings.Join(parts[:i], ".")
		base, _, ok := a.lookupVisibleType(baseName)
		if !ok {
			continue
		}
		constEnumType, ok := base.(*ConstEnumType)
		if !ok || constEnumType == nil {
			continue
		}
		memberName := strings.Join(parts[i:], ".")
		if matchedType == nil {
			matchedType = constEnumType
			matchedMemberName = memberName
		}
		if member, ok := constEnumType.Member(memberName); ok {
			return constEnumType, memberName, member, true
		}
	}
	if matchedType != nil {
		return matchedType, matchedMemberName, nil, true
	}
	return nil, "", nil, false
}

func (a *Analyzer) constEnumTypeForExpr(expr ast.Expr) (*ConstEnumType, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		base, _, ok := a.lookupVisibleType(n.Name)
		if !ok {
			return nil, false
		}
		constEnumType, ok := base.(*ConstEnumType)
		return constEnumType, ok
	case *ast.FieldExpr:
		if kindType, ok := a.treeCategoryKindExprType(n); ok {
			constEnumType, ok := kindType.(*ConstEnumType)
			return constEnumType, ok
		}
		tagType, ok := a.packedEnumTagExprType(n)
		if !ok {
			return nil, false
		}
		constEnumType, ok := tagType.(*ConstEnumType)
		return constEnumType, ok
	case *ast.ParenExpr:
		return a.constEnumTypeForExpr(n.Inner)
	default:
		return nil, false
	}
}

func (a *Analyzer) constEnumMemberExprType(expr *ast.FieldExpr) (Type, bool) {
	constEnumType, memberName, member, ok := a.constEnumMemberInfoForExpr(expr)
	if !ok {
		return nil, false
	}
	if member == nil {
		a.errorf(expr.Pos(), "const enum %q has no member %q", constEnumType.Name, memberName)
		return invalidType, true
	}
	return constEnumType, true
}

func qualifiedTypePathFromExpr(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return "", false
		}
		return n.Name, true
	case *ast.FieldExpr:
		prefix, ok := qualifiedTypePathFromExpr(n.Object)
		if !ok || prefix == "" || n.Field == "" {
			return "", false
		}
		return prefix + "." + n.Field, true
	case *ast.ParenExpr:
		return qualifiedTypePathFromExpr(n.Inner)
	case *ast.TypeExprExpr:
		named, ok := n.Type.(*ast.NamedType)
		if !ok || named == nil || named.Name == "" {
			return "", false
		}
		return named.Name, true
	default:
		return "", false
	}
}

func (a *Analyzer) enumVariantExprType(expr *ast.FieldExpr) (*EnumType, Type, bool) {
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
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
		if len(variant.Payload) == 0 {
			if _, ok := a.lookupPackedStore(enumType); ok {
				return enumType, nil, true
			}
			a.errorf(expr.Pos(), "packed enum constructor %q requires an active in %s: scope or explicit new[%s]", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name), packedEnumStoreTypeName(enumType.Name))
			return enumType, invalidType, true
		}
		params := make([]Type, len(variant.Payload))
		copy(params, variant.Payload)
		return enumType, &FuncType{Name: enumType.Name + "." + variant.Name, Params: params, Return: enumType}, true
	}
	if len(variant.Payload) == 0 {
		return enumType, nil, true
	}
	params := make([]Type, len(variant.Payload))
	copy(params, variant.Payload)
	return enumType, &FuncType{Name: enumType.Name + "." + variant.Name, Params: params, Return: enumType}, true
}

func (a *Analyzer) treeVariantExprType(expr *ast.FieldExpr) (*TreeCategoryType, Type, bool) {
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return nil, nil, false
	}
	treeType, ok := base.(*TreeCategoryType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := treeType.Variant(expr.Field)
	if !ok {
		a.errorf(expr.Pos(), "tree category %q has no variant %q", treeType.Name, expr.Field)
		return treeType, invalidType, true
	}
	if len(variant.Payload) == 0 {
		return treeType, nil, true
	}
	params := make([]Type, len(variant.Payload))
	copy(params, variant.Payload)
	return treeType, &FuncType{Name: treeType.Name + "." + variant.Name, Params: params, Return: treeType}, true
}

func treeExactMemberFieldDecls(memberType Type) []ast.FieldDecl {
	switch tt := StripAggregateStateType(memberType).(type) {
	case *TreeBlockType:
		return TreeBlockFieldDeclsWithCommon(tt)
	case *TreeStructType:
		return TreeStructFieldDeclsWithCommon(tt)
	default:
		return nil
	}
}

func (a *Analyzer) treeExactMemberTypeFromFieldExpr(expr *ast.FieldExpr) (Type, bool) {
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return nil, false
	}
	treeType, ok := base.(*TreeType)
	if !ok || treeType == nil {
		return nil, false
	}
	memberType, ok := treeType.Member(expr.Field)
	if !ok {
		return nil, false
	}
	switch StripAggregateStateType(memberType).(type) {
	case *TreeBlockType, *TreeStructType:
		return memberType, true
	default:
		return nil, false
	}
}

func (a *Analyzer) treeExactMemberExprType(expr *ast.FieldExpr) (Type, Type, bool) {
	memberType, ok := a.treeExactMemberTypeFromFieldExpr(expr)
	if !ok {
		return nil, nil, false
	}
	fieldDecls := treeExactMemberFieldDecls(memberType)
	if len(fieldDecls) == 0 {
		return memberType, nil, true
	}
	params := make([]Type, 0, len(fieldDecls))
	for _, fieldDecl := range fieldDecls {
		field, ok := TreeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			params = append(params, invalidType)
			continue
		}
		params = append(params, field.Type)
	}
	return memberType, &FuncType{Name: memberType.String(), Params: params, Return: memberType}, true
}

func (a *Analyzer) packedStoreExprType(expr *ast.FieldExpr) (*PackedEnumStoreType, Type, bool) {
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
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
	localStore := PackedEnumStoreWithState(enumType.StoreType, a.namedTypes["Local"])
	return enumType.StoreType, &FuncType{Name: enumType.StoreType.Name, Params: []Type{arenaType}, Return: localStore}, true
}

func (a *Analyzer) treeStoreExprType(expr *ast.FieldExpr) (*TreeStoreType, Type, bool) {
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return nil, nil, false
	}
	treeType, ok := base.(*TreeType)
	if !ok || expr.Field != "Store" {
		return nil, nil, false
	}
	if treeType.StoreType == nil {
		return nil, invalidType, true
	}
	arenaType, ok := a.namedTypes["Arena"]
	if !ok {
		return treeType.StoreType, invalidType, true
	}
	localStore := TreeStoreWithState(treeType.StoreType, a.namedTypes["Local"])
	return treeType.StoreType, &FuncType{Name: treeType.StoreType.Name, Params: []Type{arenaType}, Return: localStore}, true
}

func (a *Analyzer) treeStoreConstructorCall(expr *ast.CallExpr) (*TreeStoreType, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	storeType, _, ok := a.treeStoreExprType(fieldExpr)
	if !ok {
		return nil, false
	}
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "store constructor %q expects 1 argument, got %d", storeType.String(), len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return TreeStoreWithState(storeType, a.namedTypes["Local"]), true
	}
	if arenaType, ok := a.namedTypes["Arena"]; ok {
		actual := a.analyzeValueExpr(expr.Args[0], arenaType)
		if !AssignableTo(arenaType, actual) {
			a.errorf(expr.Args[0].Pos(), "store constructor %q expects %s, got %s", storeType.String(), arenaType.String(), actual.String())
		}
	} else {
		a.analyzeExpr(expr.Args[0])
	}
	return TreeStoreWithState(storeType, a.namedTypes["Local"]), true
}

func (a *Analyzer) treeExactMemberConstructorCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	a.treeConstructorCallees[fieldExpr] = true
	memberType, ok := a.treeExactMemberTypeFromFieldExpr(fieldExpr)
	if !ok {
		return nil, false
	}
	return memberType, true
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
		return PackedEnumStoreWithState(storeType, a.namedTypes["Local"]), true
	}
	if arenaType, ok := a.namedTypes["Arena"]; ok {
		actual := a.analyzeValueExpr(expr.Args[0], arenaType)
		if !AssignableTo(arenaType, actual) {
			a.errorf(expr.Args[0].Pos(), "store constructor %q expects %s, got %s", storeType.String(), arenaType.String(), actual.String())
		}
	} else {
		a.analyzeExpr(expr.Args[0])
	}
	return PackedEnumStoreWithState(storeType, a.namedTypes["Local"]), true
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
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
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

func (a *Analyzer) treeConstructorInfoFromFieldExpr(expr *ast.FieldExpr) (*TreeCategoryType, *EnumVariant, bool) {
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return nil, nil, false
	}
	treeType, ok := base.(*TreeCategoryType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := treeType.Variant(expr.Field)
	if !ok {
		return treeType, nil, true
	}
	return treeType, variant, true
}

func (a *Analyzer) activePackedStoreRegionState(enumType *EnumType) (regionRefState, bool) {
	if a == nil || enumType == nil {
		return regionRefState{}, false
	}
	activeStore, ok := a.lookupPackedStore(enumType)
	if !ok || activeStore == nil {
		return regionRefState{}, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		for _, sym := range scope.Symbols {
			storeType, ok := sym.Type.(*PackedEnumStoreType)
			if !ok || storeType == nil || storeType.Enum != enumType || !SameType(storeType, activeStore) {
				continue
			}
			if a.currentRegionRefs != nil {
				if state, ok := a.currentRegionRefs[sym]; ok && hasRegionProvenance(state) {
					state = a.canonicalizeStoredRegionRefBinding(sym, state)
					return cloneRegionRefState(state), true
				}
			}
			return regionRefStateFromPackedStoreDependency(sym, storeType), true
		}
	}
	return regionRefState{}, false
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

func (a *Analyzer) treeConstructorCall(expr *ast.CallExpr) (*TreeCategoryType, *EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	a.treeConstructorCallees[fieldExpr] = true
	treeType, variant, ok := a.treeConstructorInfoFromFieldExpr(fieldExpr)
	if !ok {
		return nil, nil, false
	}
	if variant == nil {
		a.errorf(fieldExpr.Pos(), "tree category %q has no variant %q", treeType.Name, fieldExpr.Field)
		return treeType, nil, true
	}
	return treeType, variant, true
}

func (a *Analyzer) analyzeBinaryExpr(expr *ast.BinaryExpr) Type {
	if expr.Op == lexer.TOKEN_IS {
		return a.analyzeIsExpr(expr)
	}
	leftShorthand, leftIsShorthand := contextualShorthandExpr(expr.Left)
	rightShorthand, rightIsShorthand := contextualShorthandExpr(expr.Right)
	var left Type
	var right Type
	switch {
	case leftIsShorthand && !rightIsShorthand:
		right = a.analyzeExpr(expr.Right)
		left = a.analyzeValueExpr(expr.Left, right)
	case rightIsShorthand && !leftIsShorthand:
		left = a.analyzeExpr(expr.Left)
		right = a.analyzeValueExpr(expr.Right, left)
	default:
		if leftIsShorthand && leftShorthand != nil {
			left = a.analyzeShorthandMemberExpr(leftShorthand, nil)
		} else {
			left = a.analyzeExpr(expr.Left)
		}
		if rightIsShorthand && rightShorthand != nil {
			right = a.analyzeShorthandMemberExpr(rightShorthand, nil)
		} else {
			right = a.analyzeExpr(expr.Right)
		}
	}
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
		if !(AssignableTo(left, right) || AssignableTo(right, left) || refsComparableIgnoringMutability(left, right) || (IsNullType(left) && isRefLike(right)) || (IsNullType(right) && isRefLike(left))) {
			a.errorf(expr.Pos(), "cannot compare %s and %s", left.String(), right.String())
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "comparison requires numeric operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		if lref, ok := left.(*RefType); ok && IsIntegralStorageType(right) {
			return lref
		}
		if expr.Op == lexer.TOKEN_PLUS {
			if rref, ok := right.(*RefType); ok && IsIntegralStorageType(left) {
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
		requiresIntegral := expr.Op == lexer.TOKEN_PERCENT || expr.Op == lexer.TOKEN_CARET || expr.Op == lexer.TOKEN_PIPE || expr.Op == lexer.TOKEN_AMPERSAND || expr.Op == lexer.TOKEN_LSHIFT || expr.Op == lexer.TOKEN_RSHIFT
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		if requiresIntegral && (!IsIntegralStorageType(left) || !IsIntegralStorageType(right)) {
			a.errorf(expr.Pos(), "operator requires integral operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	default:
		return invalidType
	}
}

func refsComparableIgnoringMutability(left Type, right Type) bool {
	leftRef, ok := left.(*RefType)
	if !ok || leftRef == nil {
		return false
	}
	rightRef, ok := right.(*RefType)
	if !ok || rightRef == nil {
		return false
	}
	leftClone := cloneRefType(leftRef)
	rightClone := cloneRefType(rightRef)
	leftClone.Mutable = false
	rightClone.Mutable = false
	return AssignableTo(leftClone, rightClone) || AssignableTo(rightClone, leftClone)
}

func appendIsTargetExprs(out []ast.Expr, expr ast.Expr) []ast.Expr {
	if expr == nil {
		return out
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return appendIsTargetExprs(out, n.Inner)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			out = appendIsTargetExprs(out, target)
		}
		return out
	default:
		return append(out, expr)
	}
}

func flattenIsTargetExprs(expr ast.Expr) []ast.Expr {
	return appendIsTargetExprs(nil, expr)
}

func isComparableValueTarget(left Type, right Type) bool {
	return runtimeStringComparable(left, right) ||
		(IsNumericType(left) && IsNumericType(right)) ||
		AssignableTo(left, right) ||
		AssignableTo(right, left) ||
		refsComparableIgnoringMutability(left, right) ||
		(IsNullType(left) && isRefLike(right)) ||
		(IsNullType(right) && isRefLike(left))
}

func (a *Analyzer) analyzeIsComparableTarget(left Type, target ast.Expr) bool {
	if typedExpr, ok := target.(*ast.TypeExprExpr); ok && typedExpr != nil && typedExpr.Type != nil {
		a.resolveType(typedExpr.Type)
		a.errorf(target.Pos(), "is target must be a variant, a named-state target, or a comparable value")
		return false
	}
	targetShorthand, targetIsShorthand := contextualShorthandExpr(target)
	var right Type
	if targetIsShorthand && targetShorthand != nil {
		right = a.analyzeValueExpr(target, left)
	} else {
		right = a.analyzeExpr(target)
	}
	if !isComparableValueTarget(left, right) {
		a.errorf(target.Pos(), "is expects a comparable value alternative, got %s", right.String())
		return false
	}
	return true
}

func (a *Analyzer) analyzeIsExpr(expr *ast.BinaryExpr) Type {
	left := a.analyzeExpr(expr.Left)
	targets := flattenIsTargetExprs(expr.Right)
	for _, target := range targets {
		if enumType, variant, ok := a.resolveEnumVariantIsTarget(target); ok {
			if _, _, ok := resolveMatchableEnumType(left); !ok {
				a.errorf(expr.Left.Pos(), "is requires an enum value for variant tests, got %s", left.String())
				continue
			}
			matchableEnum, _, _ := resolveMatchableEnumType(left)
			if matchableEnum == nil || enumType == nil || matchableEnum.Name != enumType.Name {
				expected := "<invalid>"
				if matchableEnum != nil {
					expected = matchableEnum.Name
				}
				got := "<invalid>"
				if enumType != nil && variant != nil {
					got = enumType.Name + "." + variant.Name
				}
				a.errorf(expr.Pos(), "is expects a variant of enum %q, got %s", expected, got)
			}
			if pattern, ok := a.enumVariantIsTargetPattern(target, enumType, variant); ok && pattern != nil {
				a.validateEnumVariantIsTargetPattern(pattern, variant)
			}
			continue
		}
		if treeType, variant, ok := a.resolveTreeVariantIsTarget(target); ok {
			if _, _, ok := resolveMatchableTreeCategoryType(left); !ok {
				a.errorf(expr.Left.Pos(), "is requires an enum or tree-category value for variant tests, got %s", left.String())
				continue
			}
			matchableTree, _, _ := resolveMatchableTreeCategoryType(left)
			if matchableTree == nil || treeType == nil || matchableTree.Name != treeType.Name {
				expected := "<invalid>"
				if matchableTree != nil {
					expected = matchableTree.Name
				}
				got := "<invalid>"
				if treeType != nil && variant != nil {
					got = treeType.Name + "." + variant.Name
				}
				a.errorf(expr.Pos(), "is expects a variant of tree category %q, got %s", expected, got)
			}
			if pattern, ok := a.treeVariantIsTargetPattern(target, treeType, variant); ok && pattern != nil {
				a.validateTreeVariantIsTargetPattern(pattern, treeType, variant)
			}
			continue
		}
		if pattern, ok := a.structIsTargetPattern(target); ok && pattern != nil {
			a.validateStructIsTargetPattern(pattern, left)
			continue
		}
		if targetBase, _, ok := a.resolveNamedStateIsTarget(target); ok {
			leftBase, ok := namedStateStructBase(left)
			if !ok || leftBase == nil {
				a.errorf(expr.Left.Pos(), "is requires a named-state struct value for type-state tests, got %s", left.String())
				continue
			}
			if leftBase.Name != targetBase.Name {
				a.errorf(expr.Pos(), "is expects a state of struct %q, got state target for %q", leftBase.Name, targetBase.Name)
			}
			continue
		}
		a.analyzeIsComparableTarget(left, target)
	}
	return a.namedTypes["bool"]
}

func (a *Analyzer) structIsTargetPattern(expr ast.Expr) (*ast.MatchStructPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return a.structIsTargetPattern(paren.Inner)
	}
	if testExpr, ok := expr.(*ast.StructTestExpr); ok && testExpr != nil && testExpr.Pattern != nil {
		return testExpr.Pattern, true
	}
	return nil, false
}

func (a *Analyzer) validateStructIsTargetPattern(pattern *ast.MatchStructPattern, actual Type) {
	if pattern == nil {
		return
	}
	fields, orderedArgs, ok := a.resolveMatchStructPattern(pattern, actual)
	if !ok {
		return
	}
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		a.analyzeStructIsPayloadPattern(arg.Pattern, fields[i].Type)
	}
}

func (a *Analyzer) enumVariantIsTargetPattern(expr ast.Expr, enumType *EnumType, variant *EnumVariant) (*ast.MatchVariantPattern, bool) {
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok && testExpr != nil && testExpr.Pattern != nil {
		return testExpr.Pattern, true
	}
	if enumType == nil || variant == nil {
		return nil, false
	}
	return &ast.MatchVariantPattern{Position: expr.Pos(), EnumName: enumType.Name, Variant: variant.Name}, true
}

func (a *Analyzer) validateEnumVariantIsTargetPattern(pattern *ast.MatchVariantPattern, variant *EnumVariant) {
	if pattern == nil || variant == nil {
		return
	}
	orderedArgs := a.resolveMatchPatternArgs(pattern, variant, pattern.EnumName+"."+pattern.Variant, false)
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		a.analyzeVariantIsPayloadPattern(arg.Pattern, variant.Payload[i])
	}
}

func (a *Analyzer) treeVariantIsTargetPattern(expr ast.Expr, treeType *TreeCategoryType, variant *EnumVariant) (*ast.MatchVariantPattern, bool) {
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok && testExpr != nil && testExpr.Pattern != nil {
		return testExpr.Pattern, true
	}
	if treeType == nil || variant == nil {
		return nil, false
	}
	return &ast.MatchVariantPattern{Position: expr.Pos(), EnumName: treeType.Name, Variant: variant.Name}, true
}

func (a *Analyzer) validateTreeVariantIsTargetPattern(pattern *ast.MatchVariantPattern, treeType *TreeCategoryType, variant *EnumVariant) {
	if pattern == nil || treeType == nil || variant == nil {
		return
	}
	orderedArgs := a.resolveMatchPatternArgs(pattern, variant, treeType.Name+"."+pattern.Variant, false)
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		a.analyzeVariantIsPayloadPattern(arg.Pattern, variant.Payload[i])
	}
}

func (a *Analyzer) analyzeVariantIsPayloadPattern(pattern ast.MatchPattern, expected Type) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return
	case *ast.MatchBindPattern:
		return
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "variant is payload pattern")
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "variant is payload pattern")
	case *ast.MatchStructPattern:
		a.analyzeStructIsPayloadPattern(p, expected)
	case *ast.MatchVariantPattern:
		switch target := expected.(type) {
		case *EnumType:
			if p.EnumName != target.Name {
				a.errorf(p.Pos(), "nested variant is pattern expects enum %q, got %q", target.Name, p.EnumName)
				return
			}
			variant, ok := target.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "enum %q has no variant %q", target.Name, p.Variant)
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, target.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				a.analyzeVariantIsPayloadPattern(arg.Pattern, variant.Payload[i])
			}
		case *TreeCategoryType:
			if p.EnumName != target.Name {
				a.errorf(p.Pos(), "nested variant is pattern expects tree category %q, got %q", target.Name, p.EnumName)
				return
			}
			variant, ok := target.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "tree category %q has no variant %q", target.Name, p.Variant)
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, target.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				a.analyzeVariantIsPayloadPattern(arg.Pattern, variant.Payload[i])
			}
		default:
			a.errorf(p.Pos(), "nested variant is pattern %q requires an enum or tree-category payload, got %s", p.EnumName+"."+p.Variant, expected.String())
		}
	default:
		a.errorf(pattern.Pos(), "unsupported variant is payload pattern %T", pattern)
	}
}

func (a *Analyzer) analyzeEnumIsPayloadPattern(pattern ast.MatchPattern, expected Type) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return
	case *ast.MatchBindPattern:
		return
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "variant is payload pattern")
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "variant is payload pattern")
	case *ast.MatchStructPattern:
		a.analyzeStructIsPayloadPattern(p, expected)
	case *ast.MatchVariantPattern:
		enumType, ok := expected.(*EnumType)
		if !ok {
			a.errorf(p.Pos(), "nested variant is pattern %q requires an enum payload, got %s", p.EnumName+"."+p.Variant, expected.String())
			return
		}
		if p.EnumName != enumType.Name {
			a.errorf(p.Pos(), "nested variant is pattern expects enum %q, got %q", enumType.Name, p.EnumName)
			return
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			a.errorf(p.Pos(), "enum %q has no variant %q", enumType.Name, p.Variant)
			return
		}
		orderedArgs := a.resolveMatchPatternArgs(p, variant, enumType.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			a.analyzeEnumIsPayloadPattern(arg.Pattern, variant.Payload[i])
		}
	default:
		a.errorf(pattern.Pos(), "unsupported variant is payload pattern %T", pattern)
	}
}

func (a *Analyzer) analyzeStructIsPayloadPattern(pattern ast.MatchPattern, expected Type) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return
	case *ast.MatchBindPattern:
		return
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "struct is field pattern")
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "struct is field pattern")
	case *ast.MatchVariantPattern:
		a.analyzeVariantIsPayloadPattern(p, expected)
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			a.analyzeStructIsPayloadPattern(arg.Pattern, fields[i].Type)
		}
	default:
		a.errorf(pattern.Pos(), "unsupported struct is field pattern %T", pattern)
	}
}

func (a *Analyzer) resolveEnumVariantIsTarget(expr ast.Expr) (*EnumType, *EnumVariant, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return a.resolveEnumVariantIsTarget(paren.Inner)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok {
		if testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, false
		}
		base, _, ok := a.lookupVisibleType(testExpr.Pattern.EnumName)
		if !ok {
			return nil, nil, false
		}
		enumType, ok := base.(*EnumType)
		if !ok || enumType == nil {
			return nil, nil, false
		}
		variant, ok := enumType.Variant(testExpr.Pattern.Variant)
		if !ok || variant == nil {
			return enumType, nil, false
		}
		return enumType, variant, true
	}
	if fieldExpr, ok := isEnumVariantExpr(expr); ok {
		enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(fieldExpr)
		if ok && variant != nil {
			return enumType, variant, true
		}
		return nil, nil, false
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	named, ok := typedExpr.Type.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	return a.enumVariantTargetFromNamedType(named)
}

func (a *Analyzer) resolveTreeVariantIsTarget(expr ast.Expr) (*TreeCategoryType, *EnumVariant, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return a.resolveTreeVariantIsTarget(paren.Inner)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok {
		if testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, false
		}
		base, _, ok := a.lookupVisibleType(testExpr.Pattern.EnumName)
		if !ok {
			return nil, nil, false
		}
		treeType, ok := base.(*TreeCategoryType)
		if !ok || treeType == nil {
			return nil, nil, false
		}
		variant, ok := treeType.Variant(testExpr.Pattern.Variant)
		if !ok || variant == nil {
			return treeType, nil, false
		}
		return treeType, variant, true
	}
	if fieldExpr, ok := expr.(*ast.FieldExpr); ok {
		treeType, variant, ok := a.treeConstructorInfoFromFieldExpr(fieldExpr)
		if ok && variant != nil {
			return treeType, variant, true
		}
		return nil, nil, false
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	named, ok := typedExpr.Type.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	return a.treeVariantTargetFromNamedType(named)
}

func (a *Analyzer) enumVariantTargetFromNamedType(named *ast.NamedType) (*EnumType, *EnumVariant, bool) {
	if named == nil {
		return nil, nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, nil, false
	}
	enumName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, _, ok := a.lookupVisibleType(enumName)
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok || enumType == nil {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(variantName)
	if !ok || variant == nil {
		return enumType, nil, false
	}
	return enumType, variant, true
}

func (a *Analyzer) treeVariantTargetFromNamedType(named *ast.NamedType) (*TreeCategoryType, *EnumVariant, bool) {
	if named == nil {
		return nil, nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, nil, false
	}
	treeName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, _, ok := a.lookupVisibleType(treeName)
	if !ok {
		return nil, nil, false
	}
	treeType, ok := base.(*TreeCategoryType)
	if !ok || treeType == nil {
		return nil, nil, false
	}
	variant, ok := treeType.Variant(variantName)
	if !ok || variant == nil {
		return treeType, nil, false
	}
	return treeType, variant, true
}

func resolveMatchableTreeCategoryType(actual Type) (*TreeCategoryType, *TreeVariantViewType, bool) {
	actual = StripAggregateStateType(actual)
	switch tt := actual.(type) {
	case *TreeCategoryType:
		if tt == nil {
			return nil, nil, false
		}
		return tt, nil, true
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil, nil, false
		}
		return tt.Category, tt, true
	default:
		return nil, nil, false
	}
}

func (a *Analyzer) resolveNamedStateIsTarget(expr ast.Expr) (*StructType, Type, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return a.resolveNamedStateIsTarget(paren.Inner)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	if named, ok := typedExpr.Type.(*ast.NamedType); ok && named != nil {
		if idx := strings.LastIndex(named.Name, "."); idx > 0 {
			if base, _, ok := a.lookupVisibleType(named.Name[:idx]); ok {
				switch base.(type) {
				case *EnumType, *TreeCategoryType:
					return nil, nil, false
				}
			}
		}
	}
	resolved := a.resolveType(typedExpr.Type)
	base, ok := namedStateStructBase(resolved)
	if !ok || base == nil {
		return nil, nil, false
	}
	stateArg, ok := namedStateCurrentArg(resolved)
	if !ok || stateArg == nil {
		return nil, nil, false
	}
	return base, stateArg, true
}

func isEnumVariantExpr(expr ast.Expr) (*ast.FieldExpr, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return isEnumVariantExpr(n.Inner)
	case *ast.FieldExpr:
		if _, ok := qualifiedTypePathFromExpr(n.Object); ok {
			return n, true
		}
	}
	return nil, false
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
		if expr.Op == lexer.TOKEN_TILDE && !IsIntegralStorageType(operand) {
			a.errorf(expr.Pos(), "unary operator requires integral operand")
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
		a.errorf(expr.Args[0].Pos(), "freeze expects a packed enum store, got %s", storeType.String())
		return invalidType
	}
	if !IsLocalPackedEnumStoreType(packedStore) {
		a.errorf(expr.Args[0].Pos(), "freeze expects local store type %q, got %s", PackedEnumStoreWithState(packedStore, a.namedTypes["Local"]).String(), packedStore.String())
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
		a.errorf(expr.Args[0].Pos(), "dense_key expects a packed enum value or packedview, got %s", nodeType.String())
		return invalidType
	}
	frozenRoot, frozenPath, frozenStoreType, ok := a.resolveFrozenPackedStoreRootPath(expr.Args[1])
	if !ok || frozenStoreType == nil || frozenStoreType.Enum == nil {
		a.errorf(expr.Args[1].Pos(), "dense_key requires an exact frozen packed-store root")
		return invalidType
	}
	if _, ok := frozenType.(*PackedEnumStoreType); !ok {
		a.errorf(expr.Args[1].Pos(), "dense_key expects a frozen packed enum store, got %s", frozenType.String())
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
		a.errorf(expr.Args[0].Pos(), "node_table_fill expects an Arena or proven non-null Arena reference, got %s", arenaType.String())
	}
	frozenRoot, frozenPath, frozenStoreType, rootOK := a.resolveFrozenPackedStoreRootPath(expr.Args[1])
	if !rootOK || frozenStoreType == nil || frozenStoreType.Enum == nil {
		a.errorf(expr.Args[1].Pos(), "node_table_fill requires an exact frozen packed-store root")
		_ = a.analyzeValueExpr(expr.Args[2], elemType)
		return invalidType
	}
	if _, ok := frozenType.(*PackedEnumStoreType); !ok {
		a.errorf(expr.Args[1].Pos(), "node_table_fill expects a frozen packed enum store, got %s", frozenType.String())
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
		a.errorf(expr.Args[2].Pos(), "node_table_fill initializer expects %s, got %s", elemType.String(), initType.String())
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

func (a *Analyzer) typeStructurallyThreadShareable(t Type, seen map[string]bool) bool {
	if t == nil {
		return true
	}
	if a.containsAffineHandleValues(t, map[string]bool{}) {
		return false
	}
	if isBlessedThreadTransferCarrierType(t) {
		return true
	}
	key := t.String()
	if seen[key] {
		return true
	}
	seen[key] = true
	switch tt := t.(type) {
	case *RefType:
		return tt.Storage == RefStorageStatic
	case *ArrayType:
		return a.typeStructurallyThreadShareable(tt.Elem, seen)
	case *DArrayType:
		return a.typeStructurallyThreadShareable(tt.Elem, seen)
	case *ViewType:
		return a.typeStructurallyThreadShareable(tt.Elem, seen)
	case *DArrayViewType:
		return a.typeStructurallyThreadShareable(tt.Elem, seen)
	case *DictType:
		return a.typeStructurallyThreadShareable(tt.Key, seen) && a.typeStructurallyThreadShareable(tt.Value, seen)
	case *EnumType:
		if tt.Packed {
			return true
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if !a.typeStructurallyThreadShareable(payload, seen) {
					return false
				}
			}
		}
		return true
	case *StructType:
		for _, field := range tt.Fields {
			if !a.typeStructurallyThreadShareable(field.Type, seen) {
				return false
			}
		}
		return true
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := genericBindingsForStructInstance(base, tt.Args)
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if !a.typeStructurallyThreadShareable(fieldType, seen) {
					return false
				}
			}
			return true
		}
		for _, arg := range tt.Args {
			if !a.typeStructurallyThreadShareable(arg, seen) {
				return false
			}
		}
		return a.typeStructurallyThreadShareable(tt.Base, seen)
	case *PackedEnumStoreType:
		return IsFrozenPackedEnumStoreType(tt)
	default:
		return true
	}
}

func isBlessedThreadTransferCarrierType(t Type) bool {
	switch tt := t.(type) {
	case *StructType:
		return tt.Builtin && (tt.Name == "Mutex" || tt.Name == "CondVar")
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		return ok && base.Builtin && (base.Name == "Mutex" || base.Name == "CondVar")
	default:
		return false
	}
}

func threadTransferResultPayloadType(callName string, returnType Type) (Type, bool) {
	instance, ok := returnType.(*GenericInstanceType)
	if !ok || len(instance.Args) == 0 {
		return nil, false
	}
	switch callName {
	case "spawn1":
		if instance.Name != "Thread" {
			return nil, false
		}
	case "pool_submit1":
		if instance.Name != "Task" {
			return nil, false
		}
	default:
		return nil, false
	}
	return instance.Args[0], true
}

func atomicRmwPayloadType(argType Type) (Type, bool) {
	refType, ok := argType.(*RefType)
	if !ok {
		return nil, false
	}
	instance, ok := refType.Elem.(*GenericInstanceType)
	if !ok || instance.Name != "atomic" || len(instance.Args) != 1 {
		return nil, false
	}
	return instance.Args[0], true
}

func isAtomicRmwCallName(name string) bool {
	switch name {
	case "fetch_add", "fetch_sub", "fetch_or", "fetch_and", "fetch_xor":
		return true
	default:
		return false
	}
}

func (a *Analyzer) validateThreadTransferArg(callName string, arg ast.Expr, argType Type) {
	if !a.typeStructurallyThreadShareable(argType, map[string]bool{}) {
		a.errorf(arg.Pos(), "argument to %q is not structurally shareable across threads: %s", callName, argType.String())
		return
	}
	state, ok := a.regionRefStateForExpr(arg)
	if !ok {
		return
	}
	if region, _, ok := firstLiveRegionDependency(state); ok && region != nil {
		a.errorf(arg.Pos(), "argument to %q cannot depend on local region %q", callName, region.Name)
		return
	}
	if store, dep, ok := firstNonShareablePackedStoreDependency(state); ok {
		label := "<packed store>"
		if store != nil {
			label = store.Name
		}
		if dep.Type != nil {
			label = dep.Type.String()
		}
		a.errorf(arg.Pos(), "argument to %q cannot depend on unpublished packed store %q", callName, label)
	}
}

func (a *Analyzer) validateThreadTransferResultType(callName string, pos lexer.Pos, resultType Type) {
	if !a.typeStructurallyThreadShareable(resultType, map[string]bool{}) {
		a.errorf(pos, "result of %q is not structurally shareable across threads: %s", callName, resultType.String())
	}
}

func (a *Analyzer) validateAtomicRmwArg(callName string, arg ast.Expr, argType Type) {
	payloadType, ok := atomicRmwPayloadType(argType)
	if !ok {
		return
	}
	if !IsNumericType(payloadType) {
		a.errorf(arg.Pos(), "argument to %q requires atomic_numeric(T), got atomic[%s]", callName, payloadType.String())
	}
}

func (a *Analyzer) specializeFunctionValueType(expected Type, actual Type) (Type, bool) {
	expectedFunc, ok := expected.(*FuncType)
	if !ok {
		return expected, false
	}
	actualFunc, ok := actual.(*FuncType)
	if !ok {
		return expected, false
	}
	specialized, _ := a.substituteType(expectedFunc, nil, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return expected, false
	}
	changed := false
	if len(specialized.ExplicitParamNames) == 0 && len(actualFunc.ExplicitParamNames) != 0 {
		specialized.ExplicitParamNames = append([]string(nil), actualFunc.ExplicitParamNames...)
		changed = true
	}
	if !funcTypeHasAnyExplicitDefault(specialized) && funcTypeHasAnyExplicitDefault(actualFunc) {
		specialized.ExplicitParamDefaultExprs = append([]ast.Expr(nil), actualFunc.ExplicitParamDefaultExprs...)
		specialized.ExplicitParamHasDefault = append([]bool(nil), actualFunc.ExplicitParamHasDefault...)
		changed = true
	}
	if len(specialized.ImplicitParamNames) == 0 && len(actualFunc.ImplicitParamNames) != 0 {
		specialized.ImplicitParamNames = append([]string(nil), actualFunc.ImplicitParamNames...)
		changed = true
	}
	if specialized.Name == "func" && actualFunc.Name != "" && actualFunc.Name != specialized.Name {
		specialized.Name = actualFunc.Name
		changed = true
	}
	limit := len(specialized.Params)
	if len(actualFunc.Params) < limit {
		limit = len(actualFunc.Params)
	}
	for i := 0; i < limit; i++ {
		if paramType, ok := a.specializeFunctionValueType(specialized.Params[i], actualFunc.Params[i]); ok {
			specialized.Params[i] = paramType
			changed = true
		}
	}
	if returnType, ok := a.specializeFunctionValueType(specialized.Return, actualFunc.Return); ok {
		specialized.Return = returnType
		changed = true
	}
	if hasRegionProvenance(actualFunc.ReturnProvenance) {
		specialized.ReturnProvenance = cloneRegionRefState(actualFunc.ReturnProvenance)
		specialized.ReturnProvenanceKnown = true
		changed = true
	}
	if hasBorrowedOwnerRefSummary(actualFunc.ReturnBorrowedOwnerRefs) {
		specialized.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(actualFunc.ReturnBorrowedOwnerRefs)
		specialized.ReturnBorrowedOwnerRefsKnown = true
		changed = true
	}
	return specialized, changed
}

func cloneStructTypeWithFields(base *StructType, fields map[string]Field) *StructType {
	if base == nil {
		return nil
	}
	cloned := *base
	cloned.Fields = fields
	return &cloned
}

func cloneStructFields(fields map[string]Field) map[string]Field {
	if len(fields) == 0 {
		return map[string]Field{}
	}
	cloned := make(map[string]Field, len(fields))
	for name, field := range fields {
		cloned[name] = field
	}
	return cloned
}

func (a *Analyzer) lookupResolvedFieldType(actual Type, name string) (Type, bool) {
	fields, ok := a.resolvedStructFields(actual)
	if !ok {
		return nil, false
	}
	for _, field := range fields {
		if field.Name == name {
			return field.Type, true
		}
	}
	return nil, false
}

func (a *Analyzer) specializeCallbackCarryingType(expected Type, actual Type) (Type, bool) {
	if expected == nil || actual == nil {
		return expected, false
	}
	if specialized, ok := a.specializeFunctionValueType(expected, actual); ok {
		return specialized, true
	}
	switch tt := expected.(type) {
	case *AggregateStateType:
		nextBase, changed := a.specializeCallbackCarryingType(tt.Base, StripAggregateStateType(actual))
		if !changed {
			return expected, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *StructType:
		changed := false
		fields := cloneStructFields(tt.Fields)
		for name, field := range tt.Fields {
			actualFieldType, ok := a.lookupResolvedFieldType(actual, name)
			if !ok {
				continue
			}
			nextType, fieldChanged := a.specializeCallbackCarryingType(field.Type, actualFieldType)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok {
			return expected, false
		}
		bindings := genericBindingsForStructInstance(baseStruct, tt.Args)
		changed := false
		fields := cloneStructFields(baseStruct.Fields)
		for name, field := range baseStruct.Fields {
			expectedFieldType := field.Type
			if len(bindings) != 0 {
				expectedFieldType = a.substituteType(expectedFieldType, bindings, nil, nil, nil)
			}
			actualFieldType, ok := a.lookupResolvedFieldType(actual, name)
			if !ok {
				continue
			}
			nextType, fieldChanged := a.specializeCallbackCarryingType(expectedFieldType, actualFieldType)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return expected, false
	}
}

func (a *Analyzer) specializeCallbackCarryingTypeFromExpr(expected Type, actualExpr ast.Expr) (Type, bool) {
	if expected == nil || actualExpr == nil {
		return expected, false
	}
	if actualType := a.exprTypes[actualExpr]; actualType != nil {
		if specialized, ok := a.specializeCallbackCarryingType(expected, actualType); ok {
			return specialized, true
		}
	}
	if specialized, ok := a.functionValueTypeForExpr(actualExpr); ok {
		if next, changed := a.specializeFunctionValueType(expected, specialized); changed {
			return next, true
		}
	}
	switch tt := expected.(type) {
	case *AggregateStateType:
		nextBase, changed := a.specializeCallbackCarryingTypeFromExpr(tt.Base, actualExpr)
		if !changed {
			return expected, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *StructType:
		changed := false
		fields := cloneStructFields(tt.Fields)
		for name, field := range tt.Fields {
			fieldExpr, ok := a.resolveProjectedFieldValueExpr(actualExpr, name)
			if !ok || fieldExpr == nil {
				continue
			}
			nextType, fieldChanged := a.specializeCallbackCarryingTypeFromExpr(field.Type, fieldExpr)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok {
			return expected, false
		}
		bindings := genericBindingsForStructInstance(baseStruct, tt.Args)
		changed := false
		fields := cloneStructFields(baseStruct.Fields)
		for name, field := range baseStruct.Fields {
			fieldExpr, ok := a.resolveProjectedFieldValueExpr(actualExpr, name)
			if !ok || fieldExpr == nil {
				continue
			}
			expectedFieldType := field.Type
			if len(bindings) != 0 {
				expectedFieldType = a.substituteType(expectedFieldType, bindings, nil, nil, nil)
			}
			nextType, fieldChanged := a.specializeCallbackCarryingTypeFromExpr(expectedFieldType, fieldExpr)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return expected, false
	}
}

func (a *Analyzer) analyzeCallExpr(expr *ast.CallExpr) Type {
	if expr != nil && expr.Safe {
		return a.analyzeSafeCallExpr(expr)
	}
	switch a.rewriteBuiltinDictMethodCall(expr) {
	case builtinDictMethodRewriteApplied:
		return a.analyzeCallExpr(expr)
	case builtinDictMethodRewriteInvalid:
		return invalidType
	}
	if resultType, ok := a.analyzeBuiltinDictEntryCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDictEntryInsertCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDictEntryGetOrInsertCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayPushCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayExtendCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayReserveCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayClearCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinDarrayTruncateCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStorePushCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStoreReserveCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStoreClearCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStoreTruncateCall(expr); ok {
		return resultType
	}
	if resultType, ok := a.analyzeBuiltinStoreRowsCall(expr); ok {
		return resultType
	}
	switch a.rewriteExtensionMethodCall(expr) {
	case extensionMethodCallRewriteInvalid:
		return invalidType
	}
	if storeType, ok := a.packedStoreConstructorCall(expr); ok {
		return storeType
	}
	if storeType, ok := a.treeStoreConstructorCall(expr); ok {
		return storeType
	}
	if callIdentName(expr) == "freeze" {
		return a.analyzeFreezeCallExpr(expr)
	}
	if helperType, ok := a.analyzePackedNodeHelperCall(expr); ok {
		return helperType
	}
	if helperType, ok := a.analyzeProofCarryingViewHelperCall(expr); ok {
		a.recordBuiltinHelperFuncType(expr, callIdentName(expr), helperType)
		return helperType
	}
	if helperType, ok := a.analyzeTreeTraversalHelperCall(expr); ok {
		a.recordBuiltinHelperFuncType(expr, callIdentName(expr), helperType)
		return helperType
	}
	if enumType, variant, ok := a.enumConstructorCall(expr); ok {
		if variant == nil {
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
		if enumType.Packed {
			return a.analyzeScopedPackedAllocExpr(&ast.AllocExpr{Position: expr.Pos(), Value: expr})
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
				var actual Type
				orderedArgs[i], actual = a.analyzeCallLikeValueExpr(orderedArgs[i], variant.Payload[i])
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
	if treeType, variant, ok := a.treeConstructorCall(expr); ok {
		if variant != nil {
			a.requireActiveTreeConstructorOwner(expr.Pos(), treeType, variant)
		}
		return a.analyzeTreeConstructorCallExpr(expr, treeType, variant)
	}
	if memberType, ok := a.treeExactMemberConstructorCall(expr); ok {
		if family, ok := TreeFamilyForMemberType(memberType); ok {
			a.requireActiveTreeFamilyConstructorOwner(expr.Pos(), family, memberType.String())
		}
		return a.analyzeTreeExactMemberConstructorCallExpr(expr, memberType)
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
	orderedArgs, orderedOK := a.resolveFunctionCallArgs(expr, ft)
	if !orderedOK {
		return invalidType
	}
	return a.analyzeResolvedCallExpr(expr, ft, orderedArgs)
}

func (a *Analyzer) analyzeResolvedCallExpr(expr *ast.CallExpr, ft *FuncType, orderedArgs []ast.Expr) Type {
	if expr == nil || ft == nil {
		return invalidType
	}
	explicitParamCount := funcTypeExplicitParamCount(ft)
	if !ft.Variadic && len(orderedArgs) != explicitParamCount {
		a.errorf(expr.Pos(), "function %q expects %d arguments, got %d", ft.Name, explicitParamCount, len(orderedArgs))
	}
	if ft.Variadic && len(orderedArgs) < explicitParamCount {
		a.errorf(expr.Pos(), "variadic function %q expects at least %d arguments, got %d", ft.Name, explicitParamCount, len(orderedArgs))
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	regionBindings := map[string]string{}
	permissionBindings := map[string][]ast.PermissionRef{}
	specializedParamTypes := map[int]Type{}
	regionParams := regionParamSet(ft.RegionParams)
	limit := explicitParamCount
	if len(orderedArgs) < limit {
		limit = len(orderedArgs)
	}
	for i := 0; i < len(orderedArgs); i++ {
		var argType Type
		if i < limit {
			expectedType := a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
			orderedArgs[i], argType = a.analyzeCallLikeValueExpr(orderedArgs[i], expectedType)
			if actualFuncType, ok := argType.(*FuncType); ok {
				if !actualFuncType.ReturnProvenanceKnown {
					a.inferFuncReturnProvenanceForExpr(orderedArgs[i], actualFuncType)
				}
				if !actualFuncType.ReturnBorrowedOwnerRefsKnown {
					a.inferFuncReturnBorrowedOwnerRefsForExpr(orderedArgs[i], actualFuncType)
				}
			}
			a.collectTypeBindings(ft.Params[i], argType, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			expectedType = a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
			if specializedType, ok := a.specializeFunctionValueType(expectedType, argType); ok {
				expectedType = specializedType
				specializedParamTypes[i] = specializedType
			}
			if specializedType, ok := a.specializeCallbackCarryingTypeFromExpr(expectedType, orderedArgs[i]); ok {
				expectedType = specializedType
				specializedParamTypes[i] = specializedType
			}
			if !AssignableTo(expectedType, argType) {
				a.errorf(orderedArgs[i].Pos(), "argument %d to %q expects %s, got %s", i+1, ft.Name, expectedType.String(), argType.String())
				a.reportMutableRefArgumentNote(orderedArgs[i].Pos(), expectedType, argType)
				a.reportShapeMismatchNotes(orderedArgs[i].Pos(), expectedType, argType)
			}
			if !a.tryConsumeSinkCallArg(expr.Func, ft, i, orderedArgs[i], expectedType) {
				a.consumeAffineValueExpr(orderedArgs[i], expectedType, "argument to call "+strconv.Quote(ft.Name))
			}
		} else {
			argType = a.analyzeExpr(orderedArgs[i])
		}
		if (ft.Name == "spawn1" && i == 1) || (ft.Name == "pool_submit1" && i == 2) {
			a.validateThreadTransferArg(ft.Name, orderedArgs[i], argType)
		}
		if isAtomicRmwCallName(ft.Name) && i == 0 {
			a.validateAtomicRmwArg(ft.Name, orderedArgs[i], argType)
		}
	}
	a.resolveImplicitCallArgs(expr, ft, bindings, shapeBindings, regionBindings, permissionBindings)
	for _, name := range ft.RegionParams {
		if _, ok := regionBindings[name]; !ok {
			a.errorf(expr.Pos(), "cannot infer region parameter %q for call to %q", name, ft.Name)
		}
	}
	for _, param := range ft.GenericParams {
		if param.Kind != ast.GenericParamType || param.InterfaceBound == "" {
			continue
		}
		bound, ok := bindings[param.Name]
		if !ok || bound == nil {
			a.errorf(expr.Pos(), "cannot infer interface-constrained type parameter %q for call to %q; use explicit specialization", param.Name, ft.Name)
			continue
		}
		iface := a.staticInterfaces[param.InterfaceBound]
		if iface == nil {
			var ok bool
			iface, _, ok = a.lookupVisibleStaticInterface(param.InterfaceBound)
			if !ok || iface == nil {
				a.errorf(expr.Pos(), "unknown interface %q", param.InterfaceBound)
				continue
			}
		}
		if !a.typeSatisfiesStaticInterface(bound, iface) {
			a.errorf(expr.Pos(), "type %q does not implement interface %q for call to %q", bound.String(), iface.Name, ft.Name)
		}
	}
	for _, name := range ft.RefStorageParams {
		if _, ok := bindings[name]; !ok {
			a.errorf(expr.Pos(), "cannot infer refstorage parameter %q for call to %q", name, ft.Name)
		}
	}
	for _, name := range ft.RefStateParams {
		if _, ok := bindings[name]; !ok {
			a.errorf(expr.Pos(), "cannot infer refstate parameter %q for call to %q", name, ft.Name)
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
	if len(specializedParamTypes) != 0 {
		if clonedApplied, ok := a.substituteType(appliedType, nil, nil, nil, nil).(*FuncType); ok && clonedApplied != nil {
			appliedType = clonedApplied
		}
		for i, specializedType := range specializedParamTypes {
			if i < len(appliedType.Params) {
				appliedType.Params[i] = specializedType
			}
		}
		appliedType.ReturnProvenance = regionRefState{}
		appliedType.ReturnProvenanceKnown = false
		appliedType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
		appliedType.ReturnBorrowedOwnerRefsKnown = false
		appliedType.ReturnIsolation = ReturnIsolationSummary{}
		appliedType.ReturnIsolationKnown = false
	}
	if len(bindings) != 0 || len(shapeBindings) != 0 || len(regionBindings) != 0 || len(permissionBindings) != 0 || len(specializedParamTypes) != 0 {
		a.exprTypes[expr.Func] = appliedType
	}
	if resultPayload, ok := threadTransferResultPayloadType(ft.Name, appliedType.Return); ok {
		a.validateThreadTransferResultType(ft.Name, expr.Pos(), resultPayload)
	}
	originalTrackedByRoot := map[*Symbol]Type{}
	loweredArgs := append([]ast.Expr(nil), orderedArgs...)
	loweredArgs = append(loweredArgs, expr.ResolvedImplicitArgs...)
	poststateLimit := len(loweredArgs)
	if len(appliedType.Params) < poststateLimit {
		poststateLimit = len(appliedType.Params)
	}
	for i := 0; i < poststateLimit; i++ {
		paramType := a.substituteType(ft.Params[i], bindings, shapeBindings, regionBindings, permissionBindings)
		if specializedType, ok := specializedParamTypes[i]; ok {
			paramType = specializedType
		}
		a.recordCallArgPoststates(loweredArgs[i], paramType, funcPoststatesForParam(appliedType.Poststates, i), originalTrackedByRoot)
	}
	a.rememberConditionalCallPoststates(expr, appliedType, originalTrackedByRoot)
	switch ft.Name {
	case "pool_shutdown":
		if len(expr.Args) >= 1 {
			if key, ok := a.lookupProtocolTargetKey(expr.Args[0]); ok {
				a.recordAffineConsumption(key, "argument to call \"pool_shutdown\"")
			}
		}
	case "task_group_add":
		if len(expr.Args) >= 1 {
			if key, ok := a.lookupProtocolTargetKey(expr.Args[0]); ok {
				a.markLiveProtocolDescription(key, "task group with pending tasks")
			}
		}
	case "task_group_wait_all":
		if len(expr.Args) >= 1 {
			if key, ok := a.lookupProtocolTargetKey(expr.Args[0]); ok {
				a.clearLiveProtocolTracking(key)
			}
		}
	}
	a.recordFunctionPermissionRefs(functionPermissionRefs(appliedType))
	if ft.Return == nil {
		return a.namedTypes["void"]
	}
	a.bindFreshReturnShapes(appliedType, shapeBindings)
	return a.substituteType(appliedType.Return, bindings, shapeBindings, regionBindings, permissionBindings)
}

func (a *Analyzer) safeChainReceiverType(receiverType Type) (Type, bool) {
	if receiverType == nil {
		return nil, false
	}
	if opt, ok := receiverType.(*OptionalType); ok && opt != nil && opt.Value != nil {
		return opt.Value, true
	}
	if ref, ok := receiverType.(*RefType); ok && ref != nil && ref.State != RefStateNonNull {
		return cloneRefTypeWithState(ref, RefStateNonNull), true
	}
	return nil, false
}

func optionalizeSafeChainResult(result Type) Type {
	if result == nil || IsInvalidType(result) || IsNeverType(result) {
		return result
	}
	return &OptionalType{Value: result}
}

func (a *Analyzer) analyzeSafeFieldExpr(expr *ast.FieldExpr) Type {
	if a == nil || expr == nil || expr.Object == nil || expr.Field == "" {
		return invalidType
	}
	receiverType := a.analyzeExpr(expr.Object)
	baseReceiverType, ok := a.safeChainReceiverType(receiverType)
	if !ok {
		a.errorf(expr.Pos(), "optional chaining requires an optional or nullable reference receiver, got %s", receiverType.String())
		return invalidType
	}
	if field, ok := dstrSyntheticField(baseReceiverType, expr.Field); ok {
		field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
		return optionalizeSafeChainResult(field.Type)
	}
	field, ok := a.lookupField(baseReceiverType, expr.Field, expr.Pos())
	if !ok {
		return invalidType
	}
	field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
	return optionalizeSafeChainResult(field.Type)
}

func (a *Analyzer) analyzeSafeCallExpr(expr *ast.CallExpr) Type {
	if a == nil || expr == nil {
		return invalidType
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil || fieldExpr.Field == "" {
		a.errorf(expr.Pos(), "optional call requires member-call syntax")
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	baseReceiverType, ok := a.safeChainReceiverType(receiverType)
	if !ok {
		a.errorf(expr.Pos(), "optional chaining requires an optional or nullable reference receiver, got %s", receiverType.String())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	if field, ok := a.lookupFieldNoError(baseReceiverType, fieldExpr.Field); ok {
		field.Type = a.specializeProjectedFunctionFieldType(fieldExpr, field.Type)
		ft, ok := field.Type.(*FuncType)
		if !ok {
			a.errorf(expr.Pos(), "cannot call non-function value of type %s", field.Type.String())
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
		orderedArgs, orderedOK := a.resolveFunctionCallArgs(expr, ft)
		if !orderedOK {
			return invalidType
		}
		resultType := a.analyzeResolvedCallExpr(expr, ft, orderedArgs)
		if ft.Return == nil || isVoidType(resultType) {
			return a.namedTypes["void"]
		}
		return optionalizeSafeChainResult(resultType)
	}
	method, methodOK, err := a.lookupVisibleExtensionMethod(fieldExpr.Field, baseReceiverType)
	if err != nil {
		a.errorf(expr.Pos(), "%s", err.Error())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	var resolvedSym *Symbol
	if methodOK && method != nil {
		resolvedSym = method.Symbol
	} else {
		resolvedSym, methodOK, err = a.lookupVisibleUFCSFunction(fieldExpr.Field, baseReceiverType)
		if err != nil {
			a.errorf(expr.Pos(), "%s", err.Error())
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
		if !methodOK || resolvedSym == nil {
			a.errorf(expr.Pos(), "optional call receiver %s has no member or UFCS function %q", receiverType.String(), fieldExpr.Field)
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return invalidType
		}
	}
	ft, ok := resolvedSym.Type.(*FuncType)
	if !ok || ft == nil {
		a.errorf(expr.Pos(), "cannot call non-function value of type %s", resolvedSym.Type.String())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	synthetic := &ast.CallExpr{
		Position:      expr.Position,
		Func:          &ast.Ident{Position: fieldExpr.Position, Name: resolvedSym.Name},
		Args:          append([]ast.Expr{&ast.ZeroedLit{Position: fieldExpr.Object.Pos()}}, expr.Args...),
		ArgNames:      append([]string{""}, expr.ArgNames...),
		WithArgs:      append([]ast.WithArg(nil), expr.WithArgs...),
		WithBundles:   append([]ast.WithBundleUse(nil), expr.WithBundles...),
		WithItemOrder: append([]ast.WithItem(nil), expr.WithItemOrder...),
	}
	orderedArgs, orderedOK := a.resolveFunctionCallArgs(synthetic, ft)
	if !orderedOK {
		return invalidType
	}
	resultType := a.analyzeResolvedCallExpr(synthetic, ft, orderedArgs)
	a.safeCalls[expr] = &SafeCallInfo{
		ResolvedFuncName: resolvedSym.Name,
		TailArgs:         append([]ast.Expr(nil), orderedArgs[1:]...),
	}
	if ft.Return == nil || isVoidType(resultType) {
		return a.namedTypes["void"]
	}
	return optionalizeSafeChainResult(resultType)
}

func (a *Analyzer) analyzeBuiltinDarrayPushCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray push expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray push does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray push requires a mutable darray receiver")
	}
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "darray push requires an active in <arena>: scope")
	}
	argType := a.analyzeValueExpr(expr.Args[0], darrayType.Elem)
	if !AssignableTo(darrayType.Elem, argType) {
		a.errorf(expr.Args[0].Pos(), "darray push expects %s, got %s", darrayType.Elem.String(), argType.String())
	}
	a.consumeAffineValueExpr(expr.Args[0], darrayType.Elem, "move into darray push")
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "darray.push",
		Params: []Type{resultType, darrayType.Elem},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayExtendCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "extend" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray extend expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray extend does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray extend requires a mutable darray receiver")
	}
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "darray extend requires an active in <arena>: scope")
	}
	sourceType := a.analyzeValueExpr(expr.Args[0], nil)
	if !builtinDArrayExtendSourceCompatible(darrayType.Elem, sourceType) {
		a.errorf(expr.Args[0].Pos(), "darray extend expects a compatible darray, dview, or array source of %s, got %s", darrayType.Elem.String(), sourceType.String())
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "darray.extend",
		Params: []Type{resultType, sourceType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayReserveCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray reserve expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray reserve does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray reserve requires a mutable darray receiver")
	}
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "darray reserve requires an active in <arena>: scope")
	}
	usizeType := a.namedTypes["usize"]
	argType := a.analyzeValueExpr(expr.Args[0], usizeType)
	if !AssignableTo(usizeType, argType) {
		a.errorf(expr.Args[0].Pos(), "darray reserve expects %s, got %s", usizeType.String(), argType.String())
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "darray.reserve",
		Params: []Type{resultType, usizeType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayClearCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray clear expects 0 arguments, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray clear requires a mutable darray receiver")
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{Name: "darray.clear", Params: []Type{resultType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayTruncateCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray truncate expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray truncate requires a mutable darray receiver")
	}
	usizeType := a.namedTypes["usize"]
	argType := a.analyzeValueExpr(expr.Args[0], usizeType)
	if !AssignableTo(usizeType, argType) {
		a.errorf(expr.Args[0].Pos(), "darray truncate expects %s, got %s", usizeType.String(), argType.String())
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{Name: "darray.truncate", Params: []Type{resultType, usizeType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func builtinStoreResultRefType(storeType *StructType, receiverRefType *RefType) *RefType {
	if receiverRefType != nil {
		return receiverRefType
	}
	return &RefType{Elem: storeType, Mutable: true, State: RefStateNonNull, Storage: RefStorageAny, ExplicitStorage: true}
}

func (a *Analyzer) analyzeBuiltinStorePushCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store push requires a mutable store receiver")
	}
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "store push requires an active in <arena>: scope")
	}
	if len(expr.Args) != len(storeType.StoreFieldOrder) {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store push expects %d arguments, got %d", len(storeType.StoreFieldOrder), len(expr.Args))
		resultType := builtinStoreResultRefType(storeType, receiverRefType)
		a.exprTypes[expr] = resultType
		return resultType, true
	}
	for i, name := range storeType.StoreFieldOrder {
		field := storeType.Fields[name]
		darrayField, ok := field.Type.(*DArrayType)
		if !ok || darrayField == nil {
			continue
		}
		argType := a.analyzeValueExpr(expr.Args[i], darrayField.Elem)
		if !AssignableTo(darrayField.Elem, argType) {
			a.errorf(expr.Args[i].Pos(), "store push argument %d (%s) expects %s, got %s", i+1, name, darrayField.Elem.String(), argType.String())
		}
		a.consumeAffineValueExpr(expr.Args[i], darrayField.Elem, "move into store push")
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.push", Params: []Type{resultType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreReserveCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store reserve requires a mutable store receiver")
	}
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "store reserve requires an active in <arena>: scope")
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store reserve expects 1 argument, got %d", len(expr.Args))
	}
	usizeType := a.namedTypes["usize"]
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], usizeType)
		if !AssignableTo(usizeType, argType) {
			a.errorf(expr.Args[0].Pos(), "store reserve expects %s, got %s", usizeType.String(), argType.String())
		}
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.reserve", Params: []Type{resultType, usizeType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreClearCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store clear requires a mutable store receiver")
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store clear expects 0 arguments, got %d", len(expr.Args))
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.clear", Params: []Type{resultType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreTruncateCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store truncate requires a mutable store receiver")
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store truncate expects 1 argument, got %d", len(expr.Args))
	}
	usizeType := a.namedTypes["usize"]
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], usizeType)
		if !AssignableTo(usizeType, argType) {
			a.errorf(expr.Args[0].Pos(), "store truncate expects %s, got %s", usizeType.String(), argType.String())
		}
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.truncate", Params: []Type{resultType, usizeType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreRowsCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "rows" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, _, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store rows expects 0 arguments, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "store rows does not support named arguments")
	}
	resultType := &StoreRowsViewType{Store: storeType}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "store.rows",
		Params: []Type{receiverType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func builtinDArrayPushReceiverType(t Type) (*DArrayType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if darrayType, ok := t.(*DArrayType); ok && darrayType != nil {
		return darrayType, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil {
		return nil, nil, false
	}
	darrayType, ok := refType.Elem.(*DArrayType)
	if !ok || darrayType == nil {
		return nil, nil, false
	}
	return darrayType, refType, true
}

func builtinDArrayPushReceiverWritable(a *Analyzer, receiver ast.Expr, receiverType Type, receiverRefType *RefType) bool {
	if receiverRefType != nil {
		return receiverRefType.Mutable
	}
	return a != nil && a.exprCanYieldWritableRef(receiver)
}

func builtinDArrayExtendSourceCompatible(elemType Type, sourceType Type) bool {
	if elemType == nil || sourceType == nil {
		return false
	}
	switch tt := sourceType.(type) {
	case *DArrayType:
		return SameType(elemType, tt.Elem)
	case *DArrayViewType:
		return SameType(elemType, tt.Elem)
	case *ArrayType:
		return SameType(elemType, tt.Elem)
	case *RefType:
		if tt == nil || tt.Elem == nil {
			return false
		}
		switch inner := tt.Elem.(type) {
		case *DArrayType:
			return SameType(elemType, inner.Elem)
		case *DArrayViewType:
			return SameType(elemType, inner.Elem)
		case *ArrayType:
			return SameType(elemType, inner.Elem)
		}
	}
	return false
}

func builtinStoreReceiverType(t Type) (*StructType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if st, ok := StripAggregateStateType(t).(*StructType); ok && st != nil && st.Store {
		return st, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	st, ok := StripAggregateStateType(refType.Elem).(*StructType)
	if !ok || st == nil || !st.Store {
		return nil, nil, false
	}
	return st, refType, true
}

func storeRowViewField(t Type, fieldName string) (Field, bool) {
	rowType, ok := t.(*StoreRowViewType)
	if !ok || rowType == nil || rowType.Store == nil {
		return Field{}, false
	}
	field, ok := rowType.Store.Fields[fieldName]
	if !ok {
		return Field{}, false
	}
	darrayType, ok := field.Type.(*DArrayType)
	if !ok || darrayType == nil {
		return Field{}, false
	}
	return Field{Name: fieldName, Type: darrayType.Elem, Mutable: false}, true
}

func builtinDictEntryValueRefType(dictType *DictType) *RefType {
	if dictType == nil {
		return &RefType{Elem: invalidType, Mutable: true, State: RefStateNullable, Storage: RefStorageAny, ExplicitStorage: true}
	}
	return &RefType{Elem: dictType.Value, Mutable: true, State: RefStateNullable, Storage: RefStorageAny, ExplicitStorage: true}
}

func builtinDictEntryReceiverType(t Type) (*DictEntryType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if entryType, ok := StripAggregateStateType(t).(*DictEntryType); ok && entryType != nil {
		return entryType, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	entryType, ok := StripAggregateStateType(refType.Elem).(*DictEntryType)
	if !ok || entryType == nil {
		return nil, nil, false
	}
	return entryType, refType, true
}

func builtinDictReceiverType(t Type) (*DictType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if dictType, ok := t.(*DictType); ok && dictType != nil {
		return dictType, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	dictType, ok := refType.Elem.(*DictType)
	if !ok || dictType == nil {
		return nil, nil, false
	}
	return dictType, refType, true
}

func builtinDictReceiverTypeExpr(pos lexer.Pos, dictType *DictType, mutable bool) ast.TypeExpr {
	if dictType == nil {
		return refToTypeExprWithStorage(&ast.NamedType{Position: pos, Name: "void"}, false, ast.RefStorageAny)
	}
	keyExpr := astTypeExprForBuiltinMethodRewrite(pos, dictType.Key)
	valueExpr := astTypeExprForBuiltinMethodRewrite(pos, dictType.Value)
	elem := &ast.BuiltinTypeExpr{
		Position: pos,
		Name:     "dict",
		TypeArgs: []ast.TypeExpr{keyExpr, valueExpr},
	}
	if mutable {
		elem = &ast.BuiltinTypeExpr{
			Position: pos,
			Name:     "dict",
			TypeArgs: []ast.TypeExpr{keyExpr, valueExpr},
		}
		return refToTypeExprWithStorage(&ast.MutableType{Position: pos, Elem: elem}, false, ast.RefStorageAny)
	}
	return refToTypeExprWithStorage(elem, false, ast.RefStorageAny)
}

func astTypeExprForBuiltinMethodRewrite(pos lexer.Pos, typ Type) ast.TypeExpr {
	switch t := StripAggregateStateType(typ).(type) {
	case *BuiltinType:
		return &ast.NamedType{Position: pos, Name: t.Name}
	case *OptionalType:
		return &ast.OptionalTypeExpr{Position: pos, Value: astTypeExprForBuiltinMethodRewrite(pos, t.Value)}
	case *RefType:
		elem := astTypeExprForBuiltinMethodRewrite(pos, t.Elem)
		if t.Mutable {
			elem = &ast.MutableType{Position: pos, Elem: elem}
		}
		return refToTypeExprWithStorage(elem, t.State != RefStateNonNull, ast.RefStorage(t.Storage))
	case *DStrType:
		if isWildcardShape(t.Shape) {
			return &ast.BuiltinTypeExpr{Position: pos, Name: "dstr"}
		}
		return &ast.BuiltinTypeExpr{Position: pos, Name: "dstr", ValueArgs: []ast.Expr{&ast.Ident{Position: pos, Name: t.Shape.String()}}}
	case *DictType:
		return &ast.BuiltinTypeExpr{
			Position: pos,
			Name:     "dict",
			TypeArgs: []ast.TypeExpr{
				astTypeExprForBuiltinMethodRewrite(pos, t.Key),
				astTypeExprForBuiltinMethodRewrite(pos, t.Value),
			},
		}
	default:
		return &ast.NamedType{Position: pos, Name: typ.String()}
	}
}

func runtimeBackedDictSupportDiagnostic(dictType *DictType) string {
	if dictType == nil {
		return "runtime-backed dict operations currently support only dict[dstr, V]"
	}
	return fmt.Sprintf("runtime-backed dict operations currently support only dict[dstr, V], got %s", dictType.String())
}

func (a *Analyzer) ensureRuntimeBackedDictSupported(pos lexer.Pos, dictType *DictType) bool {
	if dictSupportsRuntimeBackedOps(dictType) {
		return true
	}
	a.errorf(pos, "%s", runtimeBackedDictSupportDiagnostic(dictType))
	return false
}

type builtinDictMethodRewriteStatus int

const (
	builtinDictMethodRewriteNone builtinDictMethodRewriteStatus = iota
	builtinDictMethodRewriteApplied
	builtinDictMethodRewriteInvalid
)

func dictMethodReceiverExpr(receiver ast.Expr, receiverType Type, mutable bool) ast.Expr {
	if _, ok := receiverType.(*RefType); ok {
		return receiver
	}
	dictType, _, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return &ast.AddrOfExpr{Position: receiver.Pos(), Operand: receiver}
	}
	return &ast.CastExpr{
		Position: receiver.Pos(),
		Operand:  &ast.AddrOfExpr{Position: receiver.Pos(), Operand: receiver},
		Target:   builtinDictReceiverTypeExpr(receiver.Pos(), dictType, mutable),
		Origin:   ast.CastExprOriginGeneral,
	}
}

func (a *Analyzer) rewriteBuiltinDictMethodCall(expr *ast.CallExpr) builtinDictMethodRewriteStatus {
	if a == nil || expr == nil {
		return builtinDictMethodRewriteNone
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return builtinDictMethodRewriteNone
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return builtinDictMethodRewriteNone
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	dictType, receiverRefType, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return builtinDictMethodRewriteNone
	}
	if !a.ensureRuntimeBackedDictSupported(fieldExpr.Object.Pos(), dictType) {
		return builtinDictMethodRewriteInvalid
	}
	method := fieldExpr.Field
	helperName := ""
	mutates := false
	needsAlloc := false
	switch method {
	case "get":
		helperName = "arena_dict_get"
	case "contains":
		helperName = "arena_dict_contains"
	case "remove":
		helperName = "arena_dict_remove"
		mutates = true
	case "clear":
		helperName = "arena_dict_clear"
		mutates = true
	case "reserve":
		helperName = "arena_dict_reserve"
		mutates = true
		needsAlloc = true
	case "put":
		helperName = "arena_dict_put"
		mutates = true
		needsAlloc = true
	case "get_or_insert":
		helperName = "arena_dict_get_or_insert"
		mutates = true
		needsAlloc = true
	default:
		return builtinDictMethodRewriteNone
	}
	if mutates && receiverRefType == nil && !a.exprCanYieldWritableRef(fieldExpr.Object) {
		a.errorf(fieldExpr.Object.Pos(), "dict %s requires a mutable dict receiver", method)
		return builtinDictMethodRewriteInvalid
	}
	if needsAlloc {
		if a.currentAllocExpr == nil {
			a.errorf(expr.Pos(), "dict %s requires an active in <arena>: scope", method)
			return builtinDictMethodRewriteInvalid
		}
	}
	rewrittenArgs := make([]ast.Expr, 0, len(expr.Args)+2)
	if needsAlloc {
		rewrittenArgs = append(rewrittenArgs, a.currentAllocExpr)
	}
	rewrittenArgs = append(rewrittenArgs, dictMethodReceiverExpr(fieldExpr.Object, receiverType, mutates))
	rewrittenArgs = append(rewrittenArgs, expr.Args...)
	expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: helperName}
	expr.Args = rewrittenArgs
	expr.ArgNames = nil
	return builtinDictMethodRewriteApplied
}

func (a *Analyzer) analyzeBuiltinDictEntryCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "entry" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	dictType, receiverRefType, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return nil, false
	}
	if !a.ensureRuntimeBackedDictSupported(fieldExpr.Object.Pos(), dictType) {
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "dict entry expects 1 argument, got %d", len(expr.Args))
	}
	if len(expr.Args) >= 1 {
		keyType := a.analyzeValueExpr(expr.Args[0], dictType.Key)
		if !AssignableTo(dictType.Key, keyType) {
			a.errorf(expr.Args[0].Pos(), "dict entry expects key of type %s, got %s", dictType.Key.String(), keyType.String())
		}
	}
	mutable := false
	if receiverRefType != nil {
		mutable = receiverRefType.Mutable
	} else {
		mutable = a.exprCanYieldWritableRef(fieldExpr.Object)
	}
	resultType := &DictEntryType{Dict: dictType, Mutable: mutable}
	a.exprTypes[expr.Func] = &FuncType{Name: "dict.entry", Params: []Type{receiverType, dictType.Key}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDictEntryInsertCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "insert" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	entryType, _, ok := builtinDictEntryReceiverType(receiverType)
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, false
	}
	if !a.ensureRuntimeBackedDictSupported(fieldExpr.Object.Pos(), entryType.Dict) {
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !entryType.Mutable {
		a.errorf(fieldExpr.Object.Pos(), "dict entry insert requires an entry created from a mutable dict receiver")
	}
	if a.currentAllocExpr == nil {
		a.errorf(expr.Pos(), "dict entry insert requires an active in <arena>: scope")
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "dict entry insert expects 1 argument, got %d", len(expr.Args))
	}
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], entryType.Dict.Value)
		if !AssignableTo(entryType.Dict.Value, argType) {
			a.errorf(expr.Args[0].Pos(), "dict entry insert expects %s, got %s", entryType.Dict.Value.String(), argType.String())
		}
		a.consumeAffineValueExpr(expr.Args[0], entryType.Dict.Value, "move into dict entry insert")
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	a.exprTypes[expr.Func] = &FuncType{Name: "dict.entry.insert", Params: []Type{receiverType, entryType.Dict.Value}, Return: valueRefType}
	a.exprTypes[expr] = valueRefType
	return valueRefType, true
}

func (a *Analyzer) analyzeBuiltinDictEntryGetOrInsertCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "get_or_insert" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	entryType, _, ok := builtinDictEntryReceiverType(receiverType)
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, false
	}
	if !a.ensureRuntimeBackedDictSupported(fieldExpr.Object.Pos(), entryType.Dict) {
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !entryType.Mutable {
		a.errorf(fieldExpr.Object.Pos(), "dict entry get_or_insert requires an entry created from a mutable dict receiver")
	}
	if a.currentAllocExpr == nil {
		a.errorf(expr.Pos(), "dict entry get_or_insert requires an active in <arena>: scope")
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "dict entry get_or_insert expects 1 argument, got %d", len(expr.Args))
	}
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], entryType.Dict.Value)
		if !AssignableTo(entryType.Dict.Value, argType) {
			a.errorf(expr.Args[0].Pos(), "dict entry get_or_insert expects %s, got %s", entryType.Dict.Value.String(), argType.String())
		}
		a.consumeAffineValueExpr(expr.Args[0], entryType.Dict.Value, "move into dict entry get_or_insert")
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	a.exprTypes[expr.Func] = &FuncType{Name: "dict.entry.get_or_insert", Params: []Type{receiverType, entryType.Dict.Value}, Return: valueRefType}
	a.exprTypes[expr] = valueRefType
	return valueRefType, true
}

func (a *Analyzer) resolveFunctionCallArgs(expr *ast.CallExpr, ft *FuncType) ([]ast.Expr, bool) {
	if expr == nil || ft == nil {
		return nil, false
	}
	explicitParamCount := funcTypeExplicitParamCount(ft)
	if expr.ResolvedArgsValid && expr.ResolvedCommonArgs == nil {
		return expr.ResolvedArgs, true
	}
	if !expr.HasArgForward && expr.NamedArgCount() == 0 {
		if len(expr.Args) > explicitParamCount {
			expr.ResolvedArgsValid = true
			expr.ResolvedArgs = expr.Args
			expr.ResolvedCommonArgs = nil
			return expr.Args, true
		}
		ordered := make([]ast.Expr, explicitParamCount)
		copy(ordered, expr.Args)
		filled := make([]bool, explicitParamCount)
		for i := range expr.Args {
			filled[i] = true
		}
		if !a.fillMissingDefaultCallArgs(expr, ft, ordered, filled, true) {
			return nil, false
		}
		expr.ResolvedArgsValid = true
		expr.ResolvedArgs = ordered
		expr.ResolvedCommonArgs = nil
		return ordered, true
	}
	if ft.Variadic {
		if expr.HasArgForward {
			a.errorf(expr.ArgForwardPos, "call argument forwarding `..` is not supported for variadic function %q", ft.Name)
		} else {
			a.errorf(expr.Pos(), "named arguments are not supported for variadic function %q", ft.Name)
		}
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, false
	}
	if len(ft.ExplicitParamNames) != explicitParamCount {
		a.errorf(expr.Pos(), "function %q does not expose parameter names for named argument calls", ft.Name)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, false
	}
	nameToIndex := make(map[string]int, len(ft.ExplicitParamNames))
	for i, name := range ft.ExplicitParamNames {
		if name == "" {
			a.errorf(expr.Pos(), "function %q does not expose parameter names for named argument calls", ft.Name)
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return nil, false
		}
		nameToIndex[name] = i
	}
	ordered := make([]ast.Expr, explicitParamCount)
	filled := make([]bool, explicitParamCount)
	filledExplicit := make([]bool, explicitParamCount)
	if expr.HasArgForward {
		for i, name := range ft.ExplicitParamNames {
			if arg, ok := a.lookupCallForwardValueExpr(name); ok {
				ordered[i] = arg
				filled[i] = true
			}
		}
	}
	sawNamed := false
	nextPositional := 0
	ok := true
	for i, arg := range expr.Args {
		name := expr.ArgName(i)
		if name == "" {
			if expr.HasArgForward {
				a.errorf(arg.Pos(), "call argument forwarding `..` only supports named arguments")
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			if sawNamed {
				a.errorf(arg.Pos(), "function %q cannot use positional arguments after named arguments", ft.Name)
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			if nextPositional >= explicitParamCount {
				a.errorf(arg.Pos(), "function %q expects %d arguments, got %d", ft.Name, explicitParamCount, len(expr.Args))
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			ordered[nextPositional] = arg
			filled[nextPositional] = true
			filledExplicit[nextPositional] = true
			nextPositional++
			continue
		}
		sawNamed = true
		index, found := nameToIndex[name]
		if !found {
			a.errorf(arg.Pos(), "function %q has no parameter %q", ft.Name, name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		if filledExplicit[index] {
			a.errorf(arg.Pos(), "function %q parameter %q is specified more than once", ft.Name, name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		ordered[index] = arg
		filled[index] = true
		filledExplicit[index] = true
	}
	if !a.fillMissingDefaultCallArgs(expr, ft, ordered, filled, false) {
		ok = false
	}
	if !ok {
		return nil, false
	}
	expr.ResolvedArgsValid = true
	expr.ResolvedArgs = ordered
	expr.ResolvedCommonArgs = nil
	return ordered, true
}

func (a *Analyzer) lookupCallForwardValueExpr(name string) (ast.Expr, bool) {
	if a == nil || name == "" || a.currentScope == nil {
		return nil, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope == a.globalScope {
			if sym, ok := scope.Symbols[name]; ok && sym != nil {
				return nil, false
			}
			break
		}
		sym, ok := scope.Symbols[name]
		if !ok || sym == nil {
			continue
		}
		root := symbolAliasRoot(sym)
		if root == nil {
			root = sym
		}
		if root.Kind != SymbolLocal && root.Kind != SymbolParam {
			return nil, false
		}
		return &ast.Ident{Position: lexer.Pos{}, Name: name}, true
	}
	return nil, false
}

func (a *Analyzer) fillMissingDefaultCallArgs(expr *ast.CallExpr, ft *FuncType, ordered []ast.Expr, filled []bool, preferGenericMissing bool) bool {
	if expr == nil || ft == nil {
		return false
	}
	explicitParamCount := funcTypeExplicitParamCount(ft)
	ok := true
	reportedGenericMissing := false
	for i := 0; i < explicitParamCount; i++ {
		if i < len(filled) && filled[i] {
			continue
		}
		if funcTypeExplicitParamHasDefault(ft, i) {
			defaultExpr := cloneDefaultArgExpr(funcTypeExplicitParamDefaultExpr(ft, i))
			if defaultExpr == nil {
				a.errorf(expr.Pos(), "internal error: unable to clone default argument for parameter %d on %q", i+1, ft.Name)
				ok = false
				continue
			}
			ordered[i] = defaultExpr
			if i < len(filled) {
				filled[i] = true
			}
			continue
		}
		if preferGenericMissing {
			if !reportedGenericMissing {
				a.errorf(expr.Pos(), "function %q expects %d arguments, got %d", ft.Name, explicitParamCount, len(expr.Args))
				reportedGenericMissing = true
			}
		} else if i < len(ft.ExplicitParamNames) && ft.ExplicitParamNames[i] != "" {
			a.errorf(expr.Pos(), "function %q is missing argument for parameter %q", ft.Name, ft.ExplicitParamNames[i])
		} else if !reportedGenericMissing {
			a.errorf(expr.Pos(), "function %q expects %d arguments, got %d", ft.Name, explicitParamCount, len(expr.Args))
			reportedGenericMissing = true
		}
		ok = false
	}
	return ok
}

type extensionMethodCallRewriteStatus int

const (
	extensionMethodCallRewriteNone extensionMethodCallRewriteStatus = iota
	extensionMethodCallRewriteApplied
	extensionMethodCallRewriteInvalid
)

func (a *Analyzer) rewriteExtensionMethodCall(expr *ast.CallExpr) extensionMethodCallRewriteStatus {
	if a == nil || expr == nil {
		return extensionMethodCallRewriteNone
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil || fieldExpr.Field == "" {
		return extensionMethodCallRewriteNone
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return extensionMethodCallRewriteNone
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	if receiverType == nil || IsInvalidType(receiverType) {
		return extensionMethodCallRewriteNone
	}
	if _, ok := a.lookupFieldNoError(receiverType, fieldExpr.Field); ok {
		return extensionMethodCallRewriteNone
	}
	method, ok, err := a.lookupVisibleExtensionMethod(fieldExpr.Field, receiverType)
	if err != nil {
		a.errorf(expr.Pos(), "%s", err.Error())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return extensionMethodCallRewriteInvalid
	}
	if !ok || method == nil || method.Symbol == nil {
		ufcsSym, ufcsOK, ufcsErr := a.lookupVisibleUFCSFunction(fieldExpr.Field, receiverType)
		if ufcsErr != nil {
			a.errorf(expr.Pos(), "%s", ufcsErr.Error())
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return extensionMethodCallRewriteInvalid
		}
		if !ufcsOK || ufcsSym == nil {
			return extensionMethodCallRewriteNone
		}
		prependedArgs := make([]ast.Expr, 0, len(expr.Args)+1)
		prependedArgs = append(prependedArgs, fieldExpr.Object)
		prependedArgs = append(prependedArgs, expr.Args...)
		expr.Args = prependedArgs
		if len(expr.ArgNames) != 0 {
			prependedNames := make([]string, 0, len(expr.ArgNames)+1)
			prependedNames = append(prependedNames, "")
			prependedNames = append(prependedNames, expr.ArgNames...)
			expr.ArgNames = prependedNames
		}
		expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: ufcsSym.Name}
		return extensionMethodCallRewriteApplied
	}
	prependedArgs := make([]ast.Expr, 0, len(expr.Args)+1)
	prependedArgs = append(prependedArgs, fieldExpr.Object)
	prependedArgs = append(prependedArgs, expr.Args...)
	expr.Args = prependedArgs
	if len(expr.ArgNames) != 0 {
		prependedNames := make([]string, 0, len(expr.ArgNames)+1)
		prependedNames = append(prependedNames, "")
		prependedNames = append(prependedNames, expr.ArgNames...)
		expr.ArgNames = prependedNames
	}
	expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: method.Symbol.Name}
	return extensionMethodCallRewriteApplied
}

func (a *Analyzer) exprResolvesToTypePath(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	name, ok := qualifiedTypePathFromExpr(expr)
	if !ok || name == "" {
		return false
	}
	if _, ok := a.lookupTypeParam(name); ok {
		return true
	}
	_, _, ok = a.lookupVisibleType(name)
	return ok
}

func (a *Analyzer) analyzeTreeConstructorCallExpr(expr *ast.CallExpr, treeType *TreeCategoryType, variant *EnumVariant) Type {
	if expr == nil || treeType == nil {
		return invalidType
	}
	if variant == nil {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	orderedArgs, commonArgs, ok := a.resolveTreeConstructorArgs(expr, treeType, variant)
	if !ok {
		return treeType
	}
	if len(orderedArgs) != len(variant.Payload) {
		a.errorf(expr.Pos(), "tree constructor %q expects %d arguments, got %d", treeType.Name+"."+variant.Name, len(variant.Payload), len(expr.Args))
	}
	limit := len(orderedArgs)
	if len(variant.Payload) < limit {
		limit = len(variant.Payload)
	}
	for i := 0; i < len(orderedArgs); i++ {
		if i < limit {
			var actual Type
			orderedArgs[i], actual = a.analyzeCallLikeValueExpr(orderedArgs[i], variant.Payload[i])
			if !AssignableTo(variant.Payload[i], actual) {
				label := variant.PayloadLabel(i)
				if label != "" {
					a.errorf(orderedArgs[i].Pos(), "tree constructor argument %d (%s) to %q expects %s, got %s", i+1, label, treeType.Name+"."+variant.Name, variant.Payload[i].String(), actual.String())
				} else {
					a.errorf(orderedArgs[i].Pos(), "tree constructor argument %d to %q expects %s, got %s", i+1, treeType.Name+"."+variant.Name, variant.Payload[i].String(), actual.String())
				}
			}
			a.consumeAffineValueExpr(orderedArgs[i], variant.Payload[i], a.variantConstructorMoveReason("tree", treeType.Name, variant, i))
		} else {
			a.analyzeExpr(orderedArgs[i])
		}
	}
	for commonName, field := range treeType.Common {
		arg, ok := commonArgs[commonName]
		if !ok {
			continue
		}
		var actual Type
		commonArgs[commonName], actual = a.analyzeCallLikeValueExpr(arg, field.Type)
		if !AssignableTo(field.Type, actual) {
			a.errorf(commonArgs[commonName].Pos(), "tree common field %q for %q expects %s, got %s", commonName, treeType.Name+"."+variant.Name, field.Type.String(), actual.String())
		}
		a.consumeAffineValueExpr(commonArgs[commonName], field.Type, "move into tree common field "+strconv.Quote(commonName))
	}
	return treeType
}

func (a *Analyzer) analyzeTreeExactMemberConstructorCallExpr(expr *ast.CallExpr, memberType Type) Type {
	if expr == nil || memberType == nil {
		return invalidType
	}
	fieldDecls := treeExactMemberFieldDecls(memberType)
	orderedArgs, ok := a.resolveTreeExactMemberConstructorArgs(expr, memberType)
	if !ok {
		return memberType
	}
	if len(orderedArgs) != len(fieldDecls) {
		a.errorf(expr.Pos(), "tree constructor %q expects %d arguments, got %d", memberType.String(), len(fieldDecls), len(expr.Args))
	}
	limit := len(orderedArgs)
	if len(fieldDecls) < limit {
		limit = len(fieldDecls)
	}
	for i := 0; i < len(orderedArgs); i++ {
		if i >= limit {
			a.analyzeExpr(orderedArgs[i])
			continue
		}
		field, ok := TreeExactFieldInfo(memberType, fieldDecls[i].Name)
		if !ok {
			a.analyzeExpr(orderedArgs[i])
			continue
		}
		var actual Type
		orderedArgs[i], actual = a.analyzeCallLikeValueExpr(orderedArgs[i], field.Type)
		if !AssignableTo(field.Type, actual) {
			a.errorf(orderedArgs[i].Pos(), "tree constructor field %q for %q expects %s, got %s", fieldDecls[i].Name, memberType.String(), field.Type.String(), actual.String())
		}
		a.consumeAffineValueExpr(orderedArgs[i], field.Type, "move into tree constructor field "+strconv.Quote(fieldDecls[i].Name))
	}
	return memberType
}

func (a *Analyzer) tryConsumeSinkCallArg(funcExpr ast.Expr, fnType *FuncType, index int, arg ast.Expr, expected Type) bool {
	if a == nil || fnType == nil || arg == nil || !a.funcParamAllowsImplicitSink(funcExpr, fnType, index) {
		return false
	}
	if _, moved := explicitMoveOperand(arg); moved {
		return false
	}
	if !a.containsAffineHandleValues(expected, map[string]bool{}) {
		return false
	}
	key, ok := a.lookupAffineValueKey(arg)
	if !ok {
		return false
	}
	a.recordAffineConsumption(key, "argument to call "+strconv.Quote(fnType.Name))
	return true
}

func (a *Analyzer) funcParamAllowsImplicitSink(funcExpr ast.Expr, fnType *FuncType, index int) bool {
	if a == nil || fnType == nil || index < 0 {
		return false
	}
	if decl, resolvedType, ok := a.resolveSinkFuncDecl(funcExpr); ok && resolvedType != nil {
		if !resolvedType.SinkParamsKnown && decl != nil {
			a.inferFuncSinkParams(decl, resolvedType)
		}
		if resolvedType.SinkParamsKnown {
			if resolvedType != fnType {
				fnType.SinkParams = append([]bool(nil), resolvedType.SinkParams...)
				fnType.SinkParamsKnown = true
			}
			return index < len(resolvedType.SinkParams) && resolvedType.SinkParams[index]
		}
	}
	if !fnType.SinkParamsKnown {
		a.inferFuncSinkParamsForExpr(funcExpr, fnType)
	}
	return fnType.SinkParamsKnown && index < len(fnType.SinkParams) && fnType.SinkParams[index]
}

func (a *Analyzer) analyzeProofCarryingViewHelperCall(expr *ast.CallExpr) (Type, bool) {
	switch callIdentName(expr) {
	case "enumerate":
		return a.analyzeEnumerateHelperCall(expr), true
	case "readonly":
		return a.analyzeReadonlyHelperCall(expr), true
	case "split_at":
		return a.analyzeSplitAtHelperCall(expr), true
	case "chunks_exact":
		return a.analyzeChunksExactHelperCall(expr), true
	case "reduce_sum":
		return a.analyzeReduceSumHelperCall(expr), true
	case "zip_map":
		return a.analyzeZipMapHelperCall(expr), true
	default:
		return nil, false
	}
}

func (a *Analyzer) analyzeEnumerateHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "enumerate expects 1 argument, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	sourceType := a.analyzeExpr(expr.Args[0])
	info, ok := a.resolveIterLoopSourceInfo(expr.Args[0], sourceType)
	if !ok {
		a.errorf(expr.Args[0].Pos(), "enumerate expects an iterable source, got %s", sourceType.String())
		return invalidType
	}
	base, ok := a.namedTypes["EnumerateView"].(*StructType)
	if !ok || base == nil {
		a.errorf(expr.Pos(), "missing builtin EnumerateView carrier type")
		return invalidType
	}
	itemType := EnumerateTupleType(info.ItemType)
	if itemType == nil {
		a.errorf(expr.Pos(), "enumerate requires a concrete iterable item type")
		return invalidType
	}
	return &GenericInstanceType{Name: "EnumerateView", Base: base, Args: []Type{sourceType, itemType}}
}

type treeChildrenSourceKind int

const (
	treeChildrenSourceInvalid treeChildrenSourceKind = iota
	treeChildrenSourceCategory
	treeChildrenSourceExact
	treeChildrenSourceFamily
)

type treeChildrenSource struct {
	Kind     treeChildrenSourceKind
	Category *TreeCategoryType
	Variant  *EnumVariant
	Exact    Type
	Family   *TreeType
}

func resolveTreeChildrenSourceInfo(sourceType Type) (treeChildrenSource, bool) {
	switch tt := StripAggregateStateType(sourceType).(type) {
	case *TreeCategoryType:
		if tt == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceCategory, Category: tt, Family: tt.Family}, true
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Variant == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceCategory, Category: tt.Category, Variant: tt.Variant, Family: tt.Category.Family}, true
	case *TreeBlockType:
		if tt == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceExact, Exact: tt, Family: tt.Family}, true
	case *TreeStructType:
		if tt == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceExact, Exact: tt, Family: tt.Family}, true
	case *TreeNodeType:
		if tt == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceFamily, Family: tt.Family}, true
	default:
		return treeChildrenSource{}, false
	}
}

func appendTreeStructuralChildCandidates(candidates *[]Type, sourceType Type) {
	switch tt := StripAggregateStateType(sourceType).(type) {
	case *TreeCategoryType:
		if tt == nil {
			return
		}
		for _, variant := range tt.Variants {
			appendTreeVariantStructuralChildCandidates(candidates, variant)
		}
	case *TreeVariantViewType:
		if tt == nil || tt.Variant == nil {
			return
		}
		appendTreeVariantStructuralChildCandidates(candidates, tt.Variant)
	case *TreeBlockType:
		appendTreeExactStructuralChildCandidates(candidates, tt)
	case *TreeStructType:
		appendTreeExactStructuralChildCandidates(candidates, tt)
	case *TreeNodeType:
		if tt == nil || tt.Family == nil {
			return
		}
		for _, member := range TreeFamilyExactMembersInTagOrder(tt.Family) {
			appendTreeStructuralChildCandidates(candidates, member)
		}
	}
}

func appendTreeVariantStructuralChildCandidates(candidates *[]Type, variant *EnumVariant) {
	if variant == nil {
		return
	}
	for payloadIndex, payloadType := range variant.Payload {
		relation := variant.PayloadRelation(payloadIndex)
		if itemType, ok := TreeStructuralChildItemType(payloadType, relation); ok && itemType != nil {
			*candidates = append(*candidates, itemType)
		}
	}
}

func appendTreeExactStructuralChildCandidates(candidates *[]Type, exact Type) {
	family, ok := TreeFamilyForMemberType(exact)
	if !ok || family == nil {
		return
	}
	var decls []ast.FieldDecl
	switch tt := StripAggregateStateType(exact).(type) {
	case *TreeBlockType:
		decls = TreeBlockFieldDeclsWithCommon(tt)
	case *TreeStructType:
		decls = TreeStructFieldDeclsWithCommon(tt)
	default:
		return
	}
	for _, fieldDecl := range decls {
		field, ok := TreeExactFieldInfo(exact, fieldDecl.Name)
		if !ok {
			continue
		}
		relation := TreeFieldStructuralRelation(family, field.Type)
		if itemType, ok := TreeStructuralChildItemType(field.Type, relation); ok && itemType != nil {
			*candidates = append(*candidates, itemType)
		}
	}
}

func treeChildrenCandidateItemType(sourceType Type) (Type, bool) {
	var candidates []Type
	appendTreeStructuralChildCandidates(&candidates, sourceType)
	if len(candidates) == 0 {
		return nil, false
	}
	itemType := candidates[0]
	for _, candidate := range candidates[1:] {
		if !SameType(itemType, candidate) {
			return invalidType, true
		}
	}
	return itemType, true
}

func treeChildrenOverrideItemType(sourceType Type, overrideType Type) (Type, string, bool) {
	if sourceType == nil || overrideType == nil {
		return nil, "", false
	}
	if _, ok := resolveTreeChildrenSourceInfo(overrideType); !ok {
		return nil, "", false
	}
	var candidates []Type
	appendTreeStructuralChildCandidates(&candidates, sourceType)
	if len(candidates) == 0 {
		return nil, fmt.Sprintf("children(...) requires at least one structural child edge on %s", treeChildrenSourceLabel(sourceType)), true
	}
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if !AssignableTo(overrideType, candidate) {
			return nil, fmt.Sprintf("children(...) override %s is incompatible with structural child %s", overrideType.String(), candidate.String()), true
		}
	}
	return overrideType, "", true
}

func treeChildrenSourceLabel(sourceType Type) string {
	switch tt := StripAggregateStateType(sourceType).(type) {
	case *TreeCategoryType:
		if tt != nil {
			return tt.Name
		}
	case *TreeVariantViewType:
		if tt != nil && tt.Category != nil && tt.Variant != nil {
			return tt.Category.Name + "." + tt.Variant.Name
		}
	case *TreeBlockType:
		if tt != nil {
			return tt.Name
		}
	case *TreeStructType:
		if tt != nil {
			return tt.Name
		}
	case *TreeNodeType:
		if tt != nil {
			return tt.Name
		}
	}
	return sourceType.String()
}

func (a *Analyzer) analyzeTreeTraversalHelperCall(expr *ast.CallExpr) (Type, bool) {
	switch callIdentName(expr) {
	case "children":
		return a.analyzeChildrenHelperCall(expr), true
	default:
		return nil, false
	}
}

func proofCarryingViewType(t Type) bool {
	switch t.(type) {
	case *ViewType, *DArrayViewType, *DStrType, *SViewType:
		return true
	default:
		return false
	}
}

func (a *Analyzer) analyzeChildrenHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "children expects 1 argument, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	sourceExpr := expr.Args[0]
	sourceType := a.analyzeExpr(sourceExpr)
	carrierSourceType := sourceType
	if castExpr, ok := sourceExpr.(*ast.CastExpr); ok {
		actualSourceType := a.exprTypes[castExpr.Operand]
		if actualSourceType == nil {
			actualSourceType = a.analyzeExpr(castExpr.Operand)
		}
		if overrideItemType, diag, handled := treeChildrenOverrideItemType(actualSourceType, sourceType); handled {
			if diag != "" {
				a.errorf(sourceExpr.Pos(), "%s", diag)
				return invalidType
			}
			carrierSourceType = actualSourceType
			base, ok := a.namedTypes["TreeChildren"].(*StructType)
			if !ok || base == nil {
				a.errorf(expr.Pos(), "missing builtin TreeChildren carrier type")
				return invalidType
			}
			return &GenericInstanceType{Name: "TreeChildren", Base: base, Args: []Type{carrierSourceType, overrideItemType}}
		}
	}
	sourceInfo, ok := resolveTreeChildrenSourceInfo(carrierSourceType)
	if !ok {
		a.errorf(expr.Args[0].Pos(), "children expects a tree node, refined tree view, exact tree member, or Family.Node value, got %s", carrierSourceType.String())
		return invalidType
	}
	itemType, ok := treeChildrenCandidateItemType(carrierSourceType)
	if !ok {
		a.errorf(expr.Args[0].Pos(), "children(...) requires at least one structural child edge on %s", treeChildrenSourceLabel(carrierSourceType))
		return invalidType
	}
	if IsInvalidType(itemType) {
		a.errorf(expr.Args[0].Pos(), "children(...) requires all structural child payloads to have the same item type")
		return invalidType
	}
	base, ok := a.namedTypes["TreeChildren"].(*StructType)
	if !ok || base == nil {
		a.errorf(expr.Pos(), "missing builtin TreeChildren carrier type")
		return invalidType
	}
	_ = sourceInfo
	return &GenericInstanceType{Name: "TreeChildren", Base: base, Args: []Type{carrierSourceType, itemType}}
}

func denseDViewType(t Type) (*DArrayViewType, bool) {
	view, ok := t.(*DArrayViewType)
	if !ok || view == nil {
		return nil, false
	}
	if view.SurfaceName == "packedtags" {
		return nil, false
	}
	return view, true
}

func zipMapDenseViewType(t Type) (Type, Type, bool) {
	switch tt := t.(type) {
	case *ViewType:
		if tt == nil {
			return nil, nil, false
		}
		return tt, tt.Elem, true
	case *DArrayViewType:
		if tt == nil || tt.SurfaceName == "packedtags" {
			return nil, nil, false
		}
		return tt, tt.Elem, true
	default:
		return nil, nil, false
	}
}

func (a *Analyzer) analyzeReadonlyHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "readonly expects 1 argument, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	argType := a.analyzeExpr(expr.Args[0])
	if !proofCarryingViewType(argType) {
		a.errorf(expr.Args[0].Pos(), "readonly expects a view-like argument, got %s", argType.String())
		return invalidType
	}
	return argType
}

func (a *Analyzer) analyzeSplitAtHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 2 {
		a.errorf(expr.Pos(), "split_at expects 2 arguments, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	viewType, ok := denseDViewType(a.analyzeExpr(expr.Args[0]))
	if !ok {
		actual := a.exprTypes[expr.Args[0]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[0].Pos(), "split_at expects a dense dview[T], got %s", actual.String())
		return invalidType
	}
	indexType := a.analyzeValueExpr(expr.Args[1], a.namedTypes["usize"])
	if !IsNumericType(indexType) {
		a.errorf(expr.Args[1].Pos(), "split_at index must be numeric, got %s", indexType.String())
	} else if !IsIntegralStorageType(indexType) {
		a.errorf(expr.Args[1].Pos(), "split_at index must be integral, got %s", indexType.String())
	}
	base, ok := a.namedTypes["SplitView"].(*StructType)
	if !ok || base == nil {
		a.errorf(expr.Pos(), "missing builtin SplitView carrier type")
		return invalidType
	}
	return &GenericInstanceType{Name: "SplitView", Base: base, Args: []Type{viewType.Elem}}
}

func (a *Analyzer) analyzeChunksExactHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 2 {
		a.errorf(expr.Pos(), "chunks_exact expects 2 arguments, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	viewType, ok := denseDViewType(a.analyzeExpr(expr.Args[0]))
	if !ok {
		actual := a.exprTypes[expr.Args[0]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[0].Pos(), "chunks_exact expects a dense dview[T], got %s", actual.String())
		return invalidType
	}
	chunkSizeType := a.analyzeValueExpr(expr.Args[1], a.namedTypes["usize"])
	if !IsNumericType(chunkSizeType) {
		a.errorf(expr.Args[1].Pos(), "chunks_exact chunk size must be numeric, got %s", chunkSizeType.String())
	} else if !IsIntegralStorageType(chunkSizeType) {
		a.errorf(expr.Args[1].Pos(), "chunks_exact chunk size must be integral, got %s", chunkSizeType.String())
	}
	if value, ok := a.evalConstExpr(expr.Args[1]); ok && value.Kind == ConstInt && value.Int == 0 {
		a.errorf(expr.Args[1].Pos(), "chunks_exact chunk size cannot be zero")
	}
	base, ok := a.namedTypes["ChunksExactView"].(*StructType)
	if !ok || base == nil {
		a.errorf(expr.Pos(), "missing builtin ChunksExactView carrier type")
		return invalidType
	}
	return &GenericInstanceType{Name: "ChunksExactView", Base: base, Args: []Type{viewType.Elem}}
}

func (a *Analyzer) analyzeReduceSumHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) < 2 {
		a.errorf(expr.Pos(), "reduce_sum expects at least 2 arguments, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	srcViewType, srcElemType, srcOK := zipMapDenseViewType(a.analyzeExpr(expr.Args[0]))
	callbackType := a.analyzeExpr(expr.Args[1])
	extraArgTypes := make([]Type, 0, len(expr.Args)-2)
	for _, arg := range expr.Args[2:] {
		extraArgTypes = append(extraArgTypes, a.analyzeExpr(arg))
	}

	if !srcOK {
		actual := a.exprTypes[expr.Args[0]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[0].Pos(), "reduce_sum source expects a dense view[T], got %s", actual.String())
		return invalidType
	}
	if !a.exprSupportsReadonlyDenseView(expr.Args[0]) {
		a.errorf(expr.Args[0].Pos(), "reduce_sum requires source to be a readonly contiguous exact-extent view, got %s", srcViewType.String())
	}

	callbackFn, ok := callbackType.(*FuncType)
	if !ok {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback expects a function value, got %s", callbackType.String())
		return invalidType
	}
	if callbackFn.Variadic || len(callbackFn.Params) != len(extraArgTypes)+1 {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must accept the source element followed by %d extra arguments", len(extraArgTypes))
		return invalidType
	}
	if len(callbackFn.Permissions) != 0 {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must not declare effect permissions")
	}
	if callbackFn.Return == nil || isVoidType(callbackFn.Return) {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must return a numeric accumulator")
		return invalidType
	}
	if _, ok := callbackFn.Return.(*ErrorUnionType); ok {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must not return an error union")
		return invalidType
	}
	if !IsNumericType(callbackFn.Return) {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must return a numeric accumulator, got %s", callbackFn.Return.String())
		return invalidType
	}
	if !AssignableTo(callbackFn.Params[0], srcElemType) {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback first parameter expects %s, got %s", callbackFn.Params[0].String(), srcElemType.String())
	}
	for i, argType := range extraArgTypes {
		if !AssignableTo(callbackFn.Params[i+1], argType) {
			a.errorf(expr.Args[1].Pos(), "reduce_sum callback parameter %d expects %s, got %s", i+2, callbackFn.Params[i+1].String(), argType.String())
		}
	}
	return callbackFn.Return
}

func (a *Analyzer) analyzeZipMapHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 4 {
		a.errorf(expr.Pos(), "zip_map expects 4 arguments, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	dstViewType, dstElemType, dstOK := zipMapDenseViewType(a.analyzeExpr(expr.Args[0]))
	src1ViewType, src1ElemType, src1OK := zipMapDenseViewType(a.analyzeExpr(expr.Args[1]))
	src2ViewType, src2ElemType, src2OK := zipMapDenseViewType(a.analyzeExpr(expr.Args[2]))
	callbackType := a.analyzeExpr(expr.Args[3])

	if !dstOK {
		actual := a.exprTypes[expr.Args[0]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[0].Pos(), "zip_map destination expects a dense view[T], got %s", actual.String())
	}
	if !src1OK {
		actual := a.exprTypes[expr.Args[1]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[1].Pos(), "zip_map source 1 expects a dense view[T], got %s", actual.String())
	}
	if !src2OK {
		actual := a.exprTypes[expr.Args[2]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[2].Pos(), "zip_map source 2 expects a dense view[T], got %s", actual.String())
	}
	if !dstOK || !src1OK || !src2OK {
		return a.namedTypes["void"]
	}

	if !a.exprSupportsDenseWrite(expr.Args[0]) {
		a.errorf(expr.Args[0].Pos(), "zip_map requires a writable dense destination view, got %s", dstViewType.String())
	}
	if !a.exprSupportsReadonlyDenseView(expr.Args[1]) {
		a.errorf(expr.Args[1].Pos(), "zip_map requires source 1 to be a readonly contiguous exact-extent view, got %s", src1ViewType.String())
	}
	if !a.exprSupportsReadonlyDenseView(expr.Args[2]) {
		a.errorf(expr.Args[2].Pos(), "zip_map requires source 2 to be a readonly contiguous exact-extent view, got %s", src2ViewType.String())
	}
	if !a.exprsHaveEqualExtentSize(expr.Args[0], expr.Args[1]) {
		a.errorf(expr.Pos(), "zip_map requires destination and source 1 to have equal extents")
	}
	if !a.exprsHaveEqualExtentSize(expr.Args[0], expr.Args[2]) {
		a.errorf(expr.Pos(), "zip_map requires destination and source 2 to have equal extents")
	}
	if !a.exprsAreDisjoint(expr.Args[0], expr.Args[1]) {
		a.errorf(expr.Pos(), "zip_map requires destination and source 1 to be provably disjoint")
	}
	if !a.exprsAreDisjoint(expr.Args[0], expr.Args[2]) {
		a.errorf(expr.Pos(), "zip_map requires destination and source 2 to be provably disjoint")
	}

	callbackFn, ok := callbackType.(*FuncType)
	if !ok {
		a.errorf(expr.Args[3].Pos(), "zip_map callback expects a function value, got %s", callbackType.String())
		return a.namedTypes["void"]
	}
	if callbackFn.Variadic || len(callbackFn.Params) != 2 {
		a.errorf(expr.Args[3].Pos(), "zip_map callback must have type func(A, B) -> R")
		return a.namedTypes["void"]
	}
	if len(callbackFn.Permissions) != 0 {
		a.errorf(expr.Args[3].Pos(), "zip_map callback must not declare effect permissions")
	}
	if callbackFn.Return == nil || isVoidType(callbackFn.Return) {
		a.errorf(expr.Args[3].Pos(), "zip_map callback must return a value assignable to %s", dstElemType.String())
		return a.namedTypes["void"]
	}
	if _, ok := callbackFn.Return.(*ErrorUnionType); ok {
		a.errorf(expr.Args[3].Pos(), "zip_map callback must not return an error union")
	}
	if !AssignableTo(callbackFn.Params[0], src1ElemType) {
		a.errorf(expr.Args[3].Pos(), "zip_map callback first parameter expects %s, got %s", callbackFn.Params[0].String(), src1ElemType.String())
	}
	if !AssignableTo(callbackFn.Params[1], src2ElemType) {
		a.errorf(expr.Args[3].Pos(), "zip_map callback second parameter expects %s, got %s", callbackFn.Params[1].String(), src2ElemType.String())
	}
	if !AssignableTo(dstElemType, callbackFn.Return) {
		a.errorf(expr.Args[3].Pos(), "zip_map callback result expects %s, got %s", dstElemType.String(), callbackFn.Return.String())
	}
	return a.namedTypes["void"]
}

func (a *Analyzer) exprSupportsDenseWrite(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	facts, ok := a.exprFacts[expr]
	if !ok {
		return false
	}
	return facts.Contiguous && facts.UnitStride && !facts.ReadOnly
}

func (a *Analyzer) exprSupportsReadonlyDenseView(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	facts, ok := a.exprFacts[expr]
	if !ok {
		return false
	}
	return facts.ReadOnly && facts.Contiguous && facts.UnitStride && facts.HasExactExtent()
}

func (a *Analyzer) exprsHaveEqualExtentSize(left ast.Expr, right ast.Expr) bool {
	if a == nil {
		return false
	}
	leftFacts, ok := a.exprFacts[left]
	if !ok {
		return false
	}
	rightFacts, ok := a.exprFacts[right]
	if !ok {
		return false
	}
	return leftFacts.SameExtentSize(rightFacts)
}

func (a *Analyzer) exprsAreDisjoint(left ast.Expr, right ast.Expr) bool {
	if a == nil {
		return false
	}
	leftFacts, ok := a.exprFacts[left]
	if !ok {
		return false
	}
	rightFacts, ok := a.exprFacts[right]
	if !ok {
		return false
	}
	return leftFacts.Disjoint(rightFacts)
}

func (a *Analyzer) resolveEnumConstructorArgs(expr *ast.CallExpr, enumType *EnumType, variant *EnumVariant) ([]ast.Expr, bool) {
	if expr == nil || variant == nil {
		return nil, false
	}
	if expr.ResolvedArgsValid && len(expr.ResolvedArgs) == len(variant.Payload) && expr.ResolvedCommonArgs == nil {
		return expr.ResolvedArgs, true
	}
	namedCount := expr.NamedArgCount()
	if namedCount == 0 {
		expr.ResolvedArgsValid = true
		expr.ResolvedArgs = expr.Args
		expr.ResolvedCommonArgs = nil
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
	expr.ResolvedArgsValid = true
	expr.ResolvedArgs = ordered
	expr.ResolvedCommonArgs = nil
	return ordered, true
}

func (a *Analyzer) resolvePackedEnumConstructorArgs(expr *ast.CallExpr, enumType *EnumType, variant *EnumVariant) ([]ast.Expr, map[string]ast.Expr, bool) {
	if expr == nil || enumType == nil || variant == nil {
		return nil, nil, false
	}
	if expr.ResolvedArgsValid && len(expr.ResolvedArgs) == len(variant.Payload) {
		return expr.ResolvedArgs, expr.ResolvedCommonArgs, true
	}
	namedCount := expr.NamedArgCount()
	if namedCount == 0 {
		expr.ResolvedArgsValid = true
		expr.ResolvedArgs = expr.Args
		expr.ResolvedCommonArgs = nil
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
	expr.ResolvedArgsValid = true
	expr.ResolvedArgs = ordered
	expr.ResolvedCommonArgs = commonArgs
	return ordered, commonArgs, true
}

func (a *Analyzer) resolveTreeConstructorArgs(expr *ast.CallExpr, treeType *TreeCategoryType, variant *EnumVariant) ([]ast.Expr, map[string]ast.Expr, bool) {
	if expr == nil || treeType == nil || variant == nil {
		return nil, nil, false
	}
	if expr.ResolvedArgsValid && len(expr.ResolvedArgs) == len(variant.Payload) {
		return expr.ResolvedArgs, expr.ResolvedCommonArgs, true
	}
	namedCount := expr.NamedArgCount()
	if namedCount == 0 {
		if treeType.Family != nil && treeType.Family.Decl != nil && len(treeType.Family.Decl.Common) != 0 {
			a.errorf(expr.Pos(), "tree constructor %q has required common fields and therefore requires named arguments", treeType.Name+"."+variant.Name)
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return nil, nil, false
		}
		expr.ResolvedArgsValid = true
		expr.ResolvedArgs = expr.Args
		expr.ResolvedCommonArgs = nil
		return expr.Args, nil, true
	}
	if namedCount != len(expr.Args) {
		a.errorf(expr.Pos(), "tree constructor %q cannot mix positional and named arguments", treeType.Name+"."+variant.Name)
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
				a.errorf(arg.Pos(), "tree constructor %q payload field %q is specified more than once", treeType.Name+"."+variant.Name, name)
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			ordered[index] = arg
			seenPayload[index] = true
			continue
		}
		if _, found := treeType.Common[name]; found {
			if _, exists := commonArgs[name]; exists {
				a.errorf(arg.Pos(), "tree constructor %q common field %q is specified more than once", treeType.Name+"."+variant.Name, name)
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			commonArgs[name] = arg
			continue
		}
		a.errorf(arg.Pos(), "tree constructor %q has no payload or common field %q", treeType.Name+"."+variant.Name, name)
		a.analyzeExpr(arg)
		ok = false
	}
	for i, wasSeen := range seenPayload {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label != "" {
				a.errorf(expr.Pos(), "tree constructor %q is missing payload field %q", treeType.Name+"."+variant.Name, label)
			} else {
				a.errorf(expr.Pos(), "tree constructor %q is missing argument %d", treeType.Name+"."+variant.Name, i+1)
			}
			ok = false
		}
	}
	for _, fieldDecl := range treeType.Family.Decl.Common {
		if _, ok := commonArgs[fieldDecl.Name]; ok {
			continue
		}
		a.errorf(expr.Pos(), "tree constructor %q is missing common field %q", treeType.Name+"."+variant.Name, fieldDecl.Name)
		ok = false
	}
	if !ok {
		return nil, nil, false
	}
	expr.ResolvedArgsValid = true
	expr.ResolvedArgs = ordered
	expr.ResolvedCommonArgs = commonArgs
	return ordered, commonArgs, true
}

func (a *Analyzer) resolveTreeExactMemberConstructorArgs(expr *ast.CallExpr, memberType Type) ([]ast.Expr, bool) {
	fieldDecls := treeExactMemberFieldDecls(memberType)
	if expr == nil {
		return nil, false
	}
	if expr.ResolvedArgsValid && len(expr.ResolvedArgs) == len(fieldDecls) && expr.ResolvedCommonArgs == nil {
		return expr.ResolvedArgs, true
	}
	namedCount := expr.NamedArgCount()
	if namedCount == 0 {
		expr.ResolvedArgsValid = true
		expr.ResolvedArgs = expr.Args
		expr.ResolvedCommonArgs = nil
		return expr.Args, true
	}
	if namedCount != len(expr.Args) {
		a.errorf(expr.Pos(), "tree constructor %q cannot mix positional and named arguments", memberType.String())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, false
	}
	ordered := make([]ast.Expr, len(fieldDecls))
	seen := make([]bool, len(fieldDecls))
	ok := true
	for i, arg := range expr.Args {
		name := expr.ArgName(i)
		fieldIndex := -1
		for j, fieldDecl := range fieldDecls {
			if fieldDecl.Name == name {
				fieldIndex = j
				break
			}
		}
		if fieldIndex < 0 {
			a.errorf(arg.Pos(), "tree constructor %q has no field %q", memberType.String(), name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		if seen[fieldIndex] {
			a.errorf(arg.Pos(), "tree constructor %q field %q is specified more than once", memberType.String(), name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		ordered[fieldIndex] = arg
		seen[fieldIndex] = true
	}
	for i, wasSeen := range seen {
		if !wasSeen {
			a.errorf(expr.Pos(), "tree constructor %q is missing field %q", memberType.String(), fieldDecls[i].Name)
			ok = false
		}
	}
	if !ok {
		return nil, false
	}
	expr.ResolvedArgsValid = true
	expr.ResolvedArgs = ordered
	expr.ResolvedCommonArgs = nil
	return ordered, true
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
			a.collectTypeBindings(patternDict.Key, bridge.DynDict.Args[0], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectTypeBindings(patternDict.Value, bridge.DynDict.Args[1], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return true
		}
		if patternDynDict, ok := dynDictRuntimeInstance(pattern); ok {
			a.collectTypeBindings(patternDynDict.Args[0], bridge.Dict.Key, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectTypeBindings(patternDynDict.Args[1], bridge.Dict.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
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
	case *RefStorageParamType:
		if _, exists := bindings[p.Name]; !exists {
			bindings[p.Name] = actual
		}
	case *RefStateParamType:
		if _, exists := bindings[p.Name]; !exists {
			bindings[p.Name] = actual
		}
	case *ErrorUnionType:
		if act, ok := actual.(*ErrorUnionType); ok {
			a.collectTypeBindings(p.Value, act.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return
		}
		a.collectTypeBindings(p.Value, actual, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
	case *OptionalType:
		if act, ok := actual.(*OptionalType); ok {
			a.collectTypeBindings(p.Value, act.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *RefType:
		if act, ok := actual.(*RefType); ok {
			if p.StateParam != "" {
				if _, exists := bindings[p.StateParam]; !exists {
					bindings[p.StateParam] = &RefStateValueType{State: act.State}
				}
			}
			if p.StorageParam != "" {
				if _, exists := bindings[p.StorageParam]; !exists {
					bindings[p.StorageParam] = &RefStorageValueType{Storage: act.Storage}
				}
			}
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
	case *TupleType:
		if act, ok := actual.(*TupleType); ok && len(p.Fields) == len(act.Fields) {
			for i := range p.Fields {
				a.collectTypeBindings(p.Fields[i].Type, act.Fields[i].Type, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			}
		}
	case *AggregateStateType:
		if act, ok := actual.(*AggregateStateType); ok {
			a.collectTypeBindings(p.Base, act.Base, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
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
	if expr != nil && expr.Safe {
		return a.analyzeSafeFieldExpr(expr)
	}
	if viewType, ok := a.lookupRefinedPackedVariantView(expr.Object); ok {
		if field, ok := viewType.Field(expr.Field); ok {
			field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
			a.reportInvalidRegionUse(expr, field.Type)
			if state, ok := a.lookupAffineValueState(expr); ok && a.containsAffineHandleValues(field.Type, map[string]bool{}) {
				a.errorf(expr.Pos(), "%s %q cannot be used after %s", affineHandleKind(field.Type), affineValueDisplayName(expr), state.ConsumedBy)
			}
			a.reportBorrowedOwnerRefUseAfterConsume(expr, field.Type)
			return field.Type
		}
	}
	if field, ok := dstrSyntheticField(a.analyzeExpr(expr.Object), expr.Field); ok {
		field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
		a.reportInvalidRegionUse(expr, field.Type)
		if state, ok := a.lookupAffineValueState(expr); ok && a.containsAffineHandleValues(field.Type, map[string]bool{}) {
			a.errorf(expr.Pos(), "%s %q cannot be used after %s", affineHandleKind(field.Type), affineValueDisplayName(expr), state.ConsumedBy)
		}
		a.reportBorrowedOwnerRefUseAfterConsume(expr, field.Type)
		return field.Type
	}
	field, ok := a.lookupField(a.analyzeExpr(expr.Object), expr.Field, expr.Pos())
	if !ok {
		return invalidType
	}
	field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
	a.reportInvalidRegionUse(expr, field.Type)
	if state, ok := a.lookupAffineValueState(expr); ok && a.containsAffineHandleValues(field.Type, map[string]bool{}) {
		a.errorf(expr.Pos(), "%s %q cannot be used after %s", affineHandleKind(field.Type), affineValueDisplayName(expr), state.ConsumedBy)
	}
	a.reportBorrowedOwnerRefUseAfterConsume(expr, field.Type)
	return field.Type
}

func (a *Analyzer) resolveProjectedFieldValueExpr(objectExpr ast.Expr, field string) (ast.Expr, bool) {
	return a.resolveProjectedFieldValueExprAtPath(objectExpr, []borrowReturnAnnotationStep{{Field: field}})
}

func (a *Analyzer) resolveIndexedValueExpr(objectExpr ast.Expr, indexExpr ast.Expr) (ast.Expr, bool) {
	step, ok := a.resolveProjectedFieldIndexStep(indexExpr)
	if !ok {
		return nil, false
	}
	return a.resolveProjectedFieldValueExprAtPath(objectExpr, []borrowReturnAnnotationStep{step})
}

func (a *Analyzer) resolveProjectedFieldValueExprAtPath(objectExpr ast.Expr, path []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if objectExpr == nil {
		return nil, false
	}
	if len(path) == 0 {
		return objectExpr, true
	}
	switch n := objectExpr.(type) {
	case *ast.ParenExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Inner, path)
	case *ast.CastExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Operand, path)
	case *ast.MoveExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Operand, path)
	case *ast.AllocExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Value, path)
	case *ast.CanExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Expr, path)
	case *ast.IndexExpr:
		step, ok := a.resolveProjectedFieldIndexStep(n.Index)
		if !ok {
			return nil, false
		}
		combinedPath := make([]borrowReturnAnnotationStep, 0, len(path)+1)
		combinedPath = append(combinedPath, step)
		combinedPath = append(combinedPath, path...)
		return a.resolveProjectedFieldValueExprAtPath(n.Object, combinedPath)
	case *ast.SliceExpr:
		step := path[0]
		if step.Index == nil || step.Field != "" || step.Wildcard {
			return nil, false
		}
		start, ok := a.resolveProjectedFieldConstIntExpr(n.Start)
		if !ok || start < 0 {
			return nil, false
		}
		index := start + *step.Index
		if index < 0 {
			return nil, false
		}
		if end, ok := a.resolveProjectedFieldConstIntExpr(n.End); ok && index >= end {
			return nil, false
		}
		indexCopy := index
		combinedPath := make([]borrowReturnAnnotationStep, 0, len(path))
		combinedPath = append(combinedPath, borrowReturnAnnotationStep{Index: &indexCopy})
		combinedPath = append(combinedPath, path[1:]...)
		return a.resolveProjectedFieldValueExprAtPath(n.Object, combinedPath)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				if sym.Mutable {
					return nil, false
				}
				if a.currentValueBindings != nil {
					if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
						return a.resolveProjectedFieldValueExprAtPath(valueExpr, path)
					}
					if root := symbolAliasRoot(sym); root != nil && root != sym {
						if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
							return a.resolveProjectedFieldValueExprAtPath(valueExpr, path)
						}
					}
				}
				if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok {
					return a.resolveProjectedFieldValueExprAtPath(valueExpr, path)
				}
				return nil, false
			}
		}
		sym, _, ok := a.lookupVisibleGlobal(n.Name)
		if !ok || sym.Mutable {
			return nil, false
		}
		valueExpr, ok := a.immutableValueExprForSymbol(sym)
		if !ok {
			return nil, false
		}
		return a.resolveProjectedFieldValueExprAtPath(valueExpr, path)
	case *ast.ListLitExpr:
		step := path[0]
		if step.Index == nil || step.Field != "" || step.Wildcard {
			return nil, false
		}
		index := int(*step.Index)
		if index < 0 || index >= len(n.Elems) {
			return nil, false
		}
		return a.resolveProjectedFieldValueExprAtPath(n.Elems[index], path[1:])
	case *ast.StructLitExpr:
		actual := a.exprTypes[n]
		if actual == nil {
			actual = a.analyzeExpr(n)
		}
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return nil, false
		}
		step := path[0]
		if step.Field == "" {
			return nil, false
		}
		for i, resolved := range fields {
			if resolved.Name != step.Field {
				continue
			}
			if i >= len(n.Args) {
				return nil, false
			}
			return a.resolveProjectedFieldValueExprAtPath(n.Args[i], path[1:])
		}
		return nil, false
	case *ast.TupleExpr:
		actual := a.exprTypes[n]
		if actual == nil {
			actual = a.analyzeExpr(n)
		}
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return nil, false
		}
		step := path[0]
		if step.Field == "" {
			return nil, false
		}
		for i, resolved := range fields {
			if resolved.Name != step.Field {
				continue
			}
			if i >= len(n.Elems) {
				return nil, false
			}
			return a.resolveProjectedFieldValueExprAtPath(n.Elems[i], path[1:])
		}
		return nil, false
	case *ast.CallExpr:
		return a.resolveProjectedFieldValueFromCallExpr(n, path)
	case *ast.FieldExpr:
		combinedPath := make([]borrowReturnAnnotationStep, 0, len(path)+1)
		combinedPath = append(combinedPath, borrowReturnAnnotationStep{Field: n.Field})
		combinedPath = append(combinedPath, path...)
		return a.resolveProjectedFieldValueExprAtPath(n.Object, combinedPath)
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveProjectedFieldIndexStep(indexExpr ast.Expr) (borrowReturnAnnotationStep, bool) {
	if a == nil || indexExpr == nil {
		return borrowReturnAnnotationStep{}, false
	}
	index, ok := a.resolveProjectedFieldConstIntExpr(indexExpr)
	if !ok || index < 0 {
		return borrowReturnAnnotationStep{}, false
	}
	return borrowReturnAnnotationStep{Index: &index}, true
}

func (a *Analyzer) resolveProjectedFieldConstIntExpr(expr ast.Expr) (int64, bool) {
	if a == nil || expr == nil {
		return 0, false
	}
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstInt {
		return 0, false
	}
	return value.Int, true
}

func (a *Analyzer) resolveProjectedFieldValueThroughIndexOffset(base ast.Expr, offset int64, path []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if a == nil || base == nil || len(path) == 0 {
		return nil, false
	}
	step := path[0]
	if step.Index == nil || step.Field != "" || step.Wildcard {
		return nil, false
	}
	index := offset + *step.Index
	if index < 0 {
		return nil, false
	}
	indexCopy := index
	combinedPath := make([]borrowReturnAnnotationStep, 0, len(path))
	combinedPath = append(combinedPath, borrowReturnAnnotationStep{Index: &indexCopy})
	combinedPath = append(combinedPath, path[1:]...)
	return a.resolveProjectedFieldValueExprAtPath(base, combinedPath)
}

func (a *Analyzer) resolveProjectedFieldValueFromBuiltinViewHelperCall(call *ast.CallExpr, path []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if call == nil || len(path) == 0 {
		return nil, false
	}
	switch optimizationHelperName(call.Func) {
	case "arena_da_from_view":
		if len(call.Args) < 2 {
			return nil, false
		}
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[1], 0, path)
	case "arena_da_view", "arena_da_view_slice":
		if len(call.Args) < 3 {
			return nil, false
		}
		offset, ok := a.resolveProjectedFieldConstIntExpr(call.Args[1])
		if !ok {
			return nil, false
		}
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], offset, path)
	case "arena_da_view_prefix":
		if len(call.Args) < 2 {
			return nil, false
		}
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], 0, path)
	case "arena_da_view_suffix":
		if len(call.Args) < 2 {
			return nil, false
		}
		offset, ok := a.resolveProjectedFieldConstIntExpr(call.Args[1])
		if !ok {
			return nil, false
		}
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], offset, path)
	case "split_at":
		if len(call.Args) < 2 || len(path) < 2 {
			return nil, false
		}
		fieldStep := path[0]
		if fieldStep.Index != nil || fieldStep.Wildcard || fieldStep.Field == "" {
			return nil, false
		}
		switch fieldStep.Field {
		case "left":
			return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], 0, path[1:])
		case "right":
			offset, ok := a.resolveProjectedFieldConstIntExpr(call.Args[1])
			if !ok {
				return nil, false
			}
			return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], offset, path[1:])
		default:
			return nil, false
		}
	case "chunks_exact":
		if len(call.Args) < 2 || len(path) < 2 {
			return nil, false
		}
		chunkStep := path[0]
		if chunkStep.Index == nil || chunkStep.Field != "" || chunkStep.Wildcard {
			return nil, false
		}
		chunkSize, ok := a.resolveProjectedFieldConstIntExpr(call.Args[1])
		if !ok || chunkSize < 0 {
			return nil, false
		}
		offset := (*chunkStep.Index) * chunkSize
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], offset, path[1:])
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveProjectedFieldValueFromCallExpr(call *ast.CallExpr, path []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if call == nil || len(path) == 0 {
		return nil, false
	}
	if expr, ok := a.resolveProjectedFieldValueFromBuiltinViewHelperCall(call, path); ok {
		return expr, true
	}
	decl, ok := a.resolveProjectedFieldExternFuncDecl(call.Func)
	if !ok || decl == nil {
		return nil, false
	}
	for _, annotation := range decl.Annotations {
		if annotation.Name != "borrows_return_field" && annotation.Name != "borrows_return_field_rebased" {
			continue
		}
		if len(annotation.Args) == 0 || len(annotation.Args)%2 != 0 {
			continue
		}
		for i := 0; i < len(annotation.Args); i += 2 {
			returnSteps, ok := parseExternReturnTargetPath(annotation.Args[i])
			wildcardCaptures, matched := borrowReturnAnnotationStepsMatchPrefix(path, returnSteps)
			if !ok || len(returnSteps) > len(path) || !matched {
				continue
			}
			if expr, ok := a.resolveProjectedFieldBorrowSourceExprFromCall(call, decl, annotation.Args[i+1], wildcardCaptures); ok {
				return a.resolveProjectedFieldValueExprAtPath(expr, path[len(returnSteps):])
			}
		}
	}
	return nil, false
}

func borrowReturnAnnotationStepsMatchPrefix(path []borrowReturnAnnotationStep, prefix []borrowReturnAnnotationStep) ([]borrowReturnAnnotationStep, bool) {
	if len(prefix) > len(path) {
		return nil, false
	}
	wildcardCaptures := make([]borrowReturnAnnotationStep, 0, len(prefix))
	for i := range prefix {
		capture, captured, ok := borrowReturnAnnotationStepMatches(path[i], prefix[i])
		if !ok {
			return nil, false
		}
		if captured {
			wildcardCaptures = append(wildcardCaptures, capture)
		}
	}
	return wildcardCaptures, true
}

func borrowReturnAnnotationStepsEqual(left, right borrowReturnAnnotationStep) bool {
	switch {
	case left.Field != "" || right.Field != "":
		return left.Field != "" && right.Field != "" && left.Field == right.Field
	case left.Wildcard || right.Wildcard:
		return left.Wildcard && right.Wildcard
	case left.Index != nil || right.Index != nil:
		return left.Index != nil && right.Index != nil && *left.Index == *right.Index
	default:
		return false
	}
}

func borrowReturnAnnotationStepMatches(actual, pattern borrowReturnAnnotationStep) (borrowReturnAnnotationStep, bool, bool) {
	switch {
	case pattern.Wildcard:
		if actual.Wildcard {
			return cloneBorrowReturnAnnotationStep(actual), true, true
		}
		if actual.Index != nil {
			return cloneBorrowReturnAnnotationStep(actual), true, true
		}
		return borrowReturnAnnotationStep{}, false, false
	default:
		return borrowReturnAnnotationStep{}, false, borrowReturnAnnotationStepsEqual(actual, pattern)
	}
}

func cloneBorrowReturnAnnotationStep(step borrowReturnAnnotationStep) borrowReturnAnnotationStep {
	clone := step
	if step.Index != nil {
		index := *step.Index
		clone.Index = &index
	}
	return clone
}

func substituteBorrowReturnWildcardSteps(steps []borrowReturnAnnotationStep, wildcardCaptures []borrowReturnAnnotationStep) ([]borrowReturnAnnotationStep, bool) {
	if len(steps) == 0 {
		return nil, true
	}
	out := make([]borrowReturnAnnotationStep, 0, len(steps))
	captureIndex := 0
	for _, step := range steps {
		if !step.Wildcard {
			out = append(out, cloneBorrowReturnAnnotationStep(step))
			continue
		}
		if captureIndex >= len(wildcardCaptures) {
			return nil, false
		}
		out = append(out, cloneBorrowReturnAnnotationStep(wildcardCaptures[captureIndex]))
		captureIndex++
	}
	return out, true
}

func (a *Analyzer) resolveProjectedFieldExternFuncDecl(fnExpr ast.Expr) (*ast.ExternFuncDecl, bool) {
	if fnExpr == nil {
		return nil, false
	}
	switch n := fnExpr.(type) {
	case *ast.ParenExpr:
		return a.resolveProjectedFieldExternFuncDecl(n.Inner)
	case *ast.SpecializeExpr:
		return a.resolveProjectedFieldExternFuncDecl(n.Operand)
	case *ast.Ident:
		if a.globalScope == nil {
			return nil, false
		}
		sym, _, ok := a.lookupVisibleGlobal(n.Name)
		if !ok {
			return nil, false
		}
		decl, ok := sym.Node.(*ast.ExternFuncDecl)
		return decl, ok
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveProjectedFieldBorrowSourceExprFromCall(call *ast.CallExpr, decl *ast.ExternFuncDecl, pathText string, wildcardCaptures []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if call == nil || decl == nil {
		return nil, false
	}
	paramName, steps, ok := parseBorrowReturnAnnotationPath(pathText)
	if !ok || paramName == "" {
		return nil, false
	}
	steps, ok = substituteBorrowReturnWildcardSteps(steps, wildcardCaptures)
	if !ok {
		return nil, false
	}
	current, ok := resolveProjectedFieldCallArgByParamName(call, decl, paramName)
	if !ok || current == nil {
		return nil, false
	}
	for _, step := range steps {
		switch {
		case step.Field != "":
			current = &ast.FieldExpr{Position: call.Position, Object: current, Field: step.Field}
		case step.Index != nil:
			current = &ast.IndexExpr{Position: call.Position, Object: current, Index: &ast.IntLit{Position: call.Position, Value: strconv.FormatInt(*step.Index, 10), Suffix: "u"}}
		default:
			return nil, false
		}
	}
	if normalized, ok := a.normalizeProjectedBorrowSourceExpr(current); ok {
		return normalized, true
	}
	return current, true
}

func (a *Analyzer) normalizeProjectedBorrowSourceExpr(expr ast.Expr) (ast.Expr, bool) {
	if expr == nil {
		return nil, false
	}
	root, path, ok := a.extractProjectedBorrowSourcePath(expr)
	if !ok || len(path) == 0 {
		return expr, true
	}
	resolved, ok := a.resolveProjectedFieldValueExprAtPath(root, path)
	if !ok || resolved == nil {
		return expr, true
	}
	return resolved, true
}

func (a *Analyzer) extractProjectedBorrowSourcePath(expr ast.Expr) (ast.Expr, []borrowReturnAnnotationStep, bool) {
	if expr == nil {
		return nil, nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.extractProjectedBorrowSourcePath(n.Inner)
	case *ast.CastExpr:
		return a.extractProjectedBorrowSourcePath(n.Operand)
	case *ast.MoveExpr:
		return a.extractProjectedBorrowSourcePath(n.Operand)
	case *ast.CanExpr:
		return a.extractProjectedBorrowSourcePath(n.Expr)
	case *ast.AllocExpr:
		return a.extractProjectedBorrowSourcePath(n.Value)
	case *ast.FieldExpr:
		root, path, ok := a.extractProjectedBorrowSourcePath(n.Object)
		if !ok {
			return nil, nil, false
		}
		path = append(path, borrowReturnAnnotationStep{Field: n.Field})
		return root, path, true
	case *ast.IndexExpr:
		step, ok := a.resolveProjectedFieldIndexStep(n.Index)
		if !ok {
			return nil, nil, false
		}
		root, path, ok := a.extractProjectedBorrowSourcePath(n.Object)
		if !ok {
			return nil, nil, false
		}
		path = append(path, step)
		return root, path, true
	default:
		return expr, nil, true
	}
}

func resolveProjectedFieldCallArgByParamName(call *ast.CallExpr, decl *ast.ExternFuncDecl, paramName string) (ast.Expr, bool) {
	if call == nil || decl == nil || paramName == "" {
		return nil, false
	}
	for i, param := range decl.Params {
		if param.Name != paramName {
			continue
		}
		if i < len(call.Args) {
			return call.Args[i], true
		}
		return nil, false
	}
	return nil, false
}

func (a *Analyzer) immutableValueExprForSymbol(sym *Symbol) (ast.Expr, bool) {
	if sym == nil || sym.Mutable {
		return nil, false
	}
	declSym := sym
	if root := symbolAliasRoot(sym); root != nil {
		declSym = root
	}
	switch decl := declSym.Node.(type) {
	case *ast.VarDeclStmt:
		if decl == nil || decl.Value == nil {
			return nil, false
		}
		return decl.Value, true
	case *ast.ConstDecl:
		if decl == nil || decl.Value == nil {
			return nil, false
		}
		return decl.Value, true
	case *ast.GlobalDecl:
		if decl == nil || decl.Value == nil {
			return nil, false
		}
		return decl.Value, true
	default:
		return nil, false
	}
}

func (a *Analyzer) specializeProjectedFunctionFieldType(expr *ast.FieldExpr, declared Type) Type {
	if expr == nil || declared == nil {
		return declared
	}
	if _, ok := declared.(*FuncType); !ok {
		return declared
	}
	fieldExpr, ok := a.resolveProjectedFieldValueExpr(expr.Object, expr.Field)
	if !ok || fieldExpr == nil {
		return declared
	}
	actualType := a.analyzeExpr(fieldExpr)
	actualFunc, ok := actualType.(*FuncType)
	if !ok {
		return declared
	}
	if !actualFunc.ReturnProvenanceKnown {
		a.inferFuncReturnProvenanceForExpr(fieldExpr, actualFunc)
	}
	if !actualFunc.ReturnBorrowedOwnerRefsKnown {
		a.inferFuncReturnBorrowedOwnerRefsForExpr(fieldExpr, actualFunc)
	}
	if specialized, ok := a.specializeFunctionValueType(declared, actualFunc); ok {
		return specialized
	}
	return declared
}

func (a *Analyzer) analyzeIndexExpr(expr *ast.IndexExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	indexExpected := a.namedTypes["usize"]
	if indexExpected == nil {
		indexExpected = builtinUsizeType()
	}
	indexType := a.analyzeValueExpr(expr.Index, indexExpected)
	if _, ok := NodeKeyEnumType(indexType); !ok {
		if !IsNumericType(indexType) {
			a.errorf(expr.Index.Pos(), "index must be numeric, got %s", indexType.String())
		} else if !IsIntegralStorageType(indexType) {
			a.errorf(expr.Index.Pos(), "index must be integral, got %s", indexType.String())
		}
	}
	if keyEnum, ok := NodeKeyEnumType(indexType); ok {
		if result, handled := a.analyzeDenseNodeKeyIndexExpr(expr, objType, keyEnum); handled {
			a.reportInvalidRegionUse(expr, result)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
			return result
		}
	}
	if arr, ok := objType.(*ArrayType); ok {
		a.checkConstantArrayIndexBounds(arr, expr.Index)
		if isStringArrayType(arr) {
			result := a.namedTypes["char"]
			a.reportInvalidRegionUse(expr, result)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
			return result
		}
		a.reportInvalidRegionUse(expr, arr.Elem)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, arr.Elem)
		return arr.Elem
	}
	if darray, ok := objType.(*DArrayType); ok {
		a.reportInvalidRegionUse(expr, darray.Elem)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, darray.Elem)
		return darray.Elem
	}
	if view, ok := objType.(*ViewType); ok {
		a.reportInvalidRegionUse(expr, view.Elem)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, view.Elem)
		return view.Elem
	}
	if view, ok := objType.(*DArrayViewType); ok {
		a.reportInvalidRegionUse(expr, view.Elem)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, view.Elem)
		return view.Elem
	}
	if itemType, ok := ChunksExactViewItemType(objType); ok {
		a.reportInvalidRegionUse(expr, itemType)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, itemType)
		return itemType
	}
	if storeType, ok := objType.(*PackedEnumStoreType); ok && storeType.Enum != nil {
		a.reportInvalidRegionUse(expr, storeType.Enum)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, storeType.Enum)
		return storeType.Enum
	}
	if _, ok := objType.(*DStrType); ok {
		result := a.namedTypes["char"]
		a.reportInvalidRegionUse(expr, result)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
		return result
	}
	if isStringViewType(objType) {
		result := a.namedTypes["char"]
		a.reportInvalidRegionUse(expr, result)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
		return result
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "indexing requires proven non-null reference, got %s", objType.String())
			return invalidType
		}
		if arr, ok := ref.Elem.(*ArrayType); ok {
			a.checkConstantArrayIndexBounds(arr, expr.Index)
			if isStringArrayType(arr) {
				result := a.namedTypes["char"]
				a.reportInvalidRegionUse(expr, result)
				a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
				return result
			}
			a.reportInvalidRegionUse(expr, arr.Elem)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, arr.Elem)
			return arr.Elem
		}
		if darray, ok := ref.Elem.(*DArrayType); ok {
			a.reportInvalidRegionUse(expr, darray.Elem)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, darray.Elem)
			return darray.Elem
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			a.reportInvalidRegionUse(expr, view.Elem)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, view.Elem)
			return view.Elem
		}
		if view, ok := ref.Elem.(*DArrayViewType); ok {
			a.reportInvalidRegionUse(expr, view.Elem)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, view.Elem)
			return view.Elem
		}
		if itemType, ok := ChunksExactViewItemType(ref.Elem); ok {
			a.reportInvalidRegionUse(expr, itemType)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, itemType)
			return itemType
		}
		if storeType, ok := ref.Elem.(*PackedEnumStoreType); ok && storeType.Enum != nil {
			a.reportInvalidRegionUse(expr, storeType.Enum)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, storeType.Enum)
			return storeType.Enum
		}
		if _, ok := ref.Elem.(*DStrType); ok {
			result := a.namedTypes["char"]
			a.reportInvalidRegionUse(expr, result)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
			return result
		}
		if isStringViewType(ref.Elem) {
			result := a.namedTypes["char"]
			a.reportInvalidRegionUse(expr, result)
			a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
			return result
		}
		a.reportInvalidRegionUse(expr, ref.Elem)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, ref.Elem)
		return ref.Elem
	}
	a.errorf(expr.Pos(), "indexing requires string, array, view, packed store, or reference type, got %s", objType.String())
	return invalidType
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
	a.errorf(expr.Pos(), "node-key indexing requires Expr.Store[Frozen] or NodeTable[Expr, T], got %s", objType.String())
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
		a.errorf(expr.Pos(), "%s %q is invalid after %s", label, affineValueDisplayName(expr), dep.InvalidatedBy)
	}
}

func (a *Analyzer) reportBorrowedOwnerRefUseAfterConsume(expr ast.Expr, valueType Type) {
	ownerType, ok := borrowableOwnerRefElemType(valueType)
	if !ok {
		return
	}
	key, ok := a.lookupBorrowedOwnerRefKey(expr)
	if !ok {
		return
	}
	state, ok := a.lookupAffineValueStateForKey(key)
	if !ok || state.ConsumedBy == "" {
		return
	}
	a.errorf(expr.Pos(), "%s %q cannot be used after %s", affineHandleKind(ownerType), affineValueDisplayName(expr), state.ConsumedBy)
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
		a.errorf(expr.Start.Pos(), "slice start must be numeric, got %s", startType.String())
	} else if !IsIntegralStorageType(startType) {
		a.errorf(expr.Start.Pos(), "slice start must be integral, got %s", startType.String())
	}
	if !IsNumericType(endType) {
		a.errorf(expr.End.Pos(), "slice end must be numeric, got %s", endType.String())
	} else if !IsIntegralStorageType(endType) {
		a.errorf(expr.End.Pos(), "slice end must be integral, got %s", endType.String())
	}
	if array, ok := objType.(*ArrayType); ok {
		if isStringArrayType(array) {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		return &ViewType{Elem: array.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if view, ok := objType.(*DArrayType); ok {
		return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "dview"}
	}
	if view, ok := objType.(*ViewType); ok {
		return &ViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if view, ok := objType.(*DArrayViewType); ok {
		return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: view.SurfaceName}
	}
	if storeType, ok := objType.(*PackedEnumStoreType); ok && storeType.Enum != nil {
		return &DArrayViewType{Elem: storeType.Enum, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "packedview"}
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
			return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "dview"}
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			return &ViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		if view, ok := ref.Elem.(*DArrayViewType); ok {
			return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: view.SurfaceName}
		}
		if storeType, ok := ref.Elem.(*PackedEnumStoreType); ok && storeType.Enum != nil {
			return &DArrayViewType{Elem: storeType.Enum, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "packedview"}
		}
		if _, ok := ref.Elem.(*DStrType); ok {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		if isStringViewType(ref.Elem) {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
	}
	a.errorf(expr.Pos(), "slicing requires string, array, view, or packed store type, got %s", objType.String())
	return invalidType
}

func contextualTupleType(expected Type) (*TupleType, bool) {
	if unionType, ok := expected.(*ErrorUnionType); ok {
		expected = unionType.Value
	}
	tupleType, ok := StripAggregateStateType(expected).(*TupleType)
	if !ok || tupleType == nil {
		return nil, false
	}
	return tupleType, true
}

func (a *Analyzer) analyzeTupleExprWithExpected(expr *ast.TupleExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	expectedTuple, useExpected := contextualTupleType(expected)
	mismatchedArity := false
	if useExpected && len(expectedTuple.Fields) != len(expr.Elems) {
		a.errorf(expr.Pos(), "tuple expects %d elements, got %d", len(expectedTuple.Fields), len(expr.Elems))
		mismatchedArity = true
	}
	fields := make([]TupleField, 0, len(expr.Elems))
	for i, elem := range expr.Elems {
		fieldName := fmt.Sprintf("_%d", i)
		var expectedElem Type
		if useExpected && i < len(expectedTuple.Fields) {
			expectedElem = expectedTuple.Fields[i].Type
			if expectedTuple.Fields[i].Name != "" {
				fieldName = expectedTuple.Fields[i].Name
			}
		}
		itemType := a.analyzeValueExpr(elem, expectedElem)
		moveType := itemType
		if expectedElem != nil {
			moveType = expectedElem
			if !AssignableTo(expectedElem, itemType) {
				a.errorf(elem.Pos(), "tuple element %d (%s) expects %s, got %s", i+1, fieldName, expectedElem.String(), itemType.String())
				a.reportShapeMismatchNotes(elem.Pos(), expectedElem, itemType)
			}
		}
		a.consumeAffineValueExpr(elem, moveType, "move into tuple element "+strconv.Quote(fieldName))
		fields = append(fields, TupleField{Name: fieldName, Type: itemType})
	}
	if useExpected {
		if mismatchedArity {
			a.recordAnalyzedExprType(expr, invalidType)
			return invalidType
		}
		a.recordAnalyzedExprType(expr, expectedTuple)
		return expectedTuple
	}
	result := &TupleType{Fields: fields}
	a.recordAnalyzedExprType(expr, result)
	return result
}

func (a *Analyzer) analyzeValueExpr(expr ast.Expr, expected Type) Type {
	if _, ok := expr.(*ast.ZeroedLit); ok && expected != nil {
		if dictType, ok := StripAggregateStateType(expected).(*DictType); ok {
			if !a.ensureRuntimeBackedDictSupported(expr.Pos(), dictType) {
				a.recordAnalyzedExprType(expr, invalidType)
				return invalidType
			}
		}
		a.recordAnalyzedExprType(expr, expected)
		return expected
	}
	if shorthandType, ok := a.analyzeContextualShorthandValueExpr(expr, expected); ok {
		return shorthandType
	}
	if alloc, ok := expr.(*ast.AllocExpr); ok {
		result := a.analyzeAllocExprWithExpected(alloc, expected)
		a.recordAnalyzedExprType(alloc, result)
		return result
	}
	if lit, ok := expr.(*ast.StructLitExpr); ok {
		result := a.analyzeStructLiteralExpr(lit, expected)
		a.recordAnalyzedExprType(lit, result)
		return result
	}
	if list, ok := expr.(*ast.ListLitExpr); ok {
		return a.analyzeListLitExprWithExpected(list, expected)
	}
	if tuple, ok := expr.(*ast.TupleExpr); ok {
		return a.analyzeTupleExprWithExpected(tuple, expected)
	}
	if contextualExpected, ok := contextualIntLiteralType(expected); ok {
		if contextualType, ok := a.analyzeContextualIntValueExpr(expr, contextualExpected); ok {
			return contextualType
		}
	}
	if contextualExpected, ok := contextualFloatLiteralType(expected); ok {
		if contextualType, ok := a.analyzeContextualFloatValueExpr(expr, contextualExpected); ok {
			return contextualType
		}
	}
	result := a.analyzeExpr(expr)
	if expectedRef, ok := expected.(*RefType); ok && expectedRef.Mutable {
		if actualRef, ok := result.(*RefType); ok && !actualRef.Mutable {
			if a.mutationPathWritable(expr) || a.exprCanYieldWritableRef(expr) {
				cloned := cloneRefType(actualRef)
				cloned.Mutable = true
				result = cloned
				a.recordAnalyzedExprType(expr, result)
			}
		}
	}
	return result
}

func implicitCallLikeRefUpcastType(expected *RefType, actual Type) (Type, bool) {
	if expected == nil {
		return nil, false
	}
	actualRef, ok := actual.(*RefType)
	if !ok || actualRef == nil {
		return nil, false
	}
	if expected.StateParam != "" || expected.StorageParam != "" || actualRef.StateParam != "" || actualRef.StorageParam != "" {
		return nil, false
	}
	if expected.Storage != RefStorageAny || actualRef.Storage == RefStorageAny {
		return nil, false
	}
	if expected.Mutable && !actualRef.Mutable {
		return nil, false
	}
	if expected.State != actualRef.State || expected.Region != actualRef.Region {
		return nil, false
	}
	if !AssignableTo(expected.Elem, actualRef.Elem) {
		return nil, false
	}
	coerced := cloneRefType(actualRef)
	coerced.Storage = RefStorageAny
	coerced.ExplicitStorage = expected.ExplicitStorage
	return coerced, true
}

func (a *Analyzer) analyzeCallLikeValueExpr(expr ast.Expr, expected Type) (ast.Expr, Type) {
	actual := a.analyzeValueExpr(expr, expected)
	if expected == nil || AssignableTo(expected, actual) {
		return expr, actual
	}
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil {
		return expr, actual
	}
	if upcastType, ok := implicitCallLikeRefUpcastType(expectedRef, actual); ok {
		return expr, upcastType
	}
	if !a.exprCanYieldAddressableValue(expr) || !AssignableTo(expectedRef.Elem, actual) {
		return expr, actual
	}
	autoref := &ast.AddrOfExpr{Position: expr.Pos(), Operand: expr}
	autorefType := a.analyzeExpr(autoref)
	if AssignableTo(expected, autorefType) {
		return autoref, autorefType
	}
	if upcastType, ok := implicitCallLikeRefUpcastType(expectedRef, autorefType); ok {
		return autoref, upcastType
	}
	return expr, actual
}

func (a *Analyzer) recordAnalyzedExprType(expr ast.Expr, result Type) {
	if expr == nil {
		return
	}
	a.exprTypes[expr] = result
	a.recordExprOptimizationFacts(expr, result)
}

func (a *Analyzer) recordExprOptimizationFacts(expr ast.Expr, result Type) {
	if a == nil || expr == nil || a.exprFacts == nil {
		return
	}
	baseFacts := optimizationFactsForType(result)
	if !exprRequiresOptimizationFactInference(expr) && !typeMayCarryRegionProvenanceForOptimization(result) {
		if baseFacts.HasAnyFacts() {
			a.exprFacts[expr] = baseFacts
			return
		}
		delete(a.exprFacts, expr)
		return
	}
	facts := a.inferExprOptimizationFactsWithBase(expr, result, baseFacts)
	if facts.HasAnyFacts() {
		a.exprFacts[expr] = facts
		return
	}
	delete(a.exprFacts, expr)
}

func contextualIntLiteralType(expected Type) (Type, bool) {
	if expected == nil || !IsIntegralStorageType(expected) {
		return nil, false
	}
	return expected, true
}

func (a *Analyzer) analyzeContextualIntValueExpr(expr ast.Expr, expected Type) (Type, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		if n.Suffix != "" {
			return nil, false
		}
		a.recordAnalyzedExprType(n, expected)
		return expected, true
	case *ast.ParenExpr:
		innerType, ok := a.analyzeContextualIntValueExpr(n.Inner, expected)
		if !ok {
			return nil, false
		}
		a.recordAnalyzedExprType(n, innerType)
		return innerType, true
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return nil, false
		}
		operandType, ok := a.analyzeContextualIntValueExpr(n.Operand, expected)
		if !ok {
			return nil, false
		}
		if !IsNumericType(operandType) {
			a.errorf(n.Pos(), "unary operator requires numeric operand")
			a.recordAnalyzedExprType(n, invalidType)
			return invalidType, true
		}
		a.recordAnalyzedExprType(n, operandType)
		return operandType, true
	case *ast.TernaryExpr:
		return a.analyzeContextualIntTernaryExpr(n, expected), true
	default:
		return nil, false
	}
}

func contextualFloatLiteralType(expected Type) (Type, bool) {
	if expected == nil || !IsFloatType(expected) {
		return nil, false
	}
	return expected, true
}

func (a *Analyzer) analyzeContextualFloatValueExpr(expr ast.Expr, expected Type) (Type, bool) {
	switch n := expr.(type) {
	case *ast.FloatLit:
		if n.Suffix != "" {
			return nil, false
		}
		a.recordAnalyzedExprType(n, expected)
		return expected, true
	case *ast.ParenExpr:
		innerType, ok := a.analyzeContextualFloatValueExpr(n.Inner, expected)
		if !ok {
			return nil, false
		}
		a.recordAnalyzedExprType(n, innerType)
		return innerType, true
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return nil, false
		}
		operandType, ok := a.analyzeContextualFloatValueExpr(n.Operand, expected)
		if !ok {
			return nil, false
		}
		if !IsNumericType(operandType) {
			a.errorf(n.Pos(), "unary operator requires numeric operand")
			a.recordAnalyzedExprType(n, invalidType)
			return invalidType, true
		}
		a.recordAnalyzedExprType(n, operandType)
		return operandType, true
	case *ast.BinaryExpr:
		return a.analyzeContextualFloatBinaryExpr(n, expected), true
	case *ast.TernaryExpr:
		return a.analyzeContextualFloatTernaryExpr(n, expected), true
	default:
		return nil, false
	}
}

func (a *Analyzer) analyzeContextualFloatBinaryExpr(expr *ast.BinaryExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if !isContextualFloatArithmeticOp(expr.Op) {
		return a.analyzeBinaryExpr(expr)
	}
	left := a.analyzeValueExpr(expr.Left, expected)
	right := a.analyzeValueExpr(expr.Right, expected)
	if !IsNumericType(left) || !IsNumericType(right) {
		a.errorf(expr.Pos(), "operator requires numeric operands")
		a.recordAnalyzedExprType(expr, invalidType)
		return invalidType
	}
	result := CommonNumericType(left, right)
	a.recordAnalyzedExprType(expr, result)
	return result
}

func isContextualFloatArithmeticOp(op lexer.TokenKind) bool {
	switch op {
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH:
		return true
	default:
		return false
	}
}

func (a *Analyzer) analyzeValueExprInScope(expr ast.Expr, expected Type, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	result := a.analyzeValueExpr(expr, expected)
	a.currentScope = saved
	return result
}

func (a *Analyzer) analyzeValueExprInAffineScope(expr ast.Expr, expected Type, scope *Scope) (Type, affineFlowSnapshot) {
	return a.analyzeValueExprInAffineScopePrepared(expr, expected, scope, nil)
}

func (a *Analyzer) analyzeValueExprInConditionAffineScope(expr ast.Expr, expected Type, parent *Scope, cond ast.Expr, truthy bool) (Type, affineFlowSnapshot) {
	scope := a.refinedScopeForCondition(parent, cond, truthy)
	return a.analyzeValueExprInAffineScopePrepared(expr, expected, scope, func() {
		a.applyConditionRefinementsInternal(scope, cond, truthy, true)
	})
}

func (a *Analyzer) analyzeValueExprInAffineScopePrepared(expr ast.Expr, expected Type, scope *Scope, prepare func()) (Type, affineFlowSnapshot) {
	savedAffine := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	a.currentAffineValues = a.cloneAffineValueStates()
	a.currentBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
	a.currentFunctionValues = a.cloneFunctionValueBindings()
	a.currentSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
	a.currentValueBindings = a.cloneValueBindings()
	if prepare != nil {
		prepare()
	}
	result := a.analyzeValueExprInScope(expr, expected, scope)
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	return result, snapshot
}

func (a *Analyzer) analyzeContextualIntTernaryExpr(expr *ast.TernaryExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	condType := a.analyzeCondExpr(expr.Cond)
	if !IsBoolType(condType) {
		a.errorf(expr.Pos(), "ternary condition must be bool, got %s", condType.String())
	}
	mergedAffine := a.cloneAffineValueStates()
	mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	left, leftSnapshot := a.analyzeValueExprInConditionAffineScope(expr.Value, expected, a.currentScope, expr.Cond, true)
	right, rightSnapshot := a.analyzeValueExprInConditionAffineScope(expr.Alt, expected, a.currentScope, expr.Cond, false)
	mergedAffine = mergeAffineValueStates(mergedAffine, leftSnapshot.Affine)
	mergedAffine = mergeAffineValueStates(mergedAffine, rightSnapshot.Affine)
	mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, leftSnapshot.BorrowedOwnerRefs)
	mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, rightSnapshot.BorrowedOwnerRefs)
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	if mergedFunctionValues, ok := a.intersectFunctionValueFlows(leftSnapshot.FunctionValues, rightSnapshot.FunctionValues); ok {
		a.currentFunctionValues = mergedFunctionValues
	}
	merged := MergeTypes(left, right)
	if IsInvalidType(merged) {
		a.errorf(expr.Pos(), "ternary branches are incompatible: %s and %s", left.String(), right.String())
	}
	a.recordAnalyzedExprType(expr, merged)
	return merged
}

func (a *Analyzer) analyzeContextualFloatTernaryExpr(expr *ast.TernaryExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	condType := a.analyzeCondExpr(expr.Cond)
	if !IsBoolType(condType) {
		a.errorf(expr.Pos(), "ternary condition must be bool, got %s", condType.String())
	}
	mergedAffine := a.cloneAffineValueStates()
	mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	left, leftSnapshot := a.analyzeValueExprInConditionAffineScope(expr.Value, expected, a.currentScope, expr.Cond, true)
	right, rightSnapshot := a.analyzeValueExprInConditionAffineScope(expr.Alt, expected, a.currentScope, expr.Cond, false)
	mergedAffine = mergeAffineValueStates(mergedAffine, leftSnapshot.Affine)
	mergedAffine = mergeAffineValueStates(mergedAffine, rightSnapshot.Affine)
	mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, leftSnapshot.BorrowedOwnerRefs)
	mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, rightSnapshot.BorrowedOwnerRefs)
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	if mergedFunctionValues, ok := a.intersectFunctionValueFlows(leftSnapshot.FunctionValues, rightSnapshot.FunctionValues); ok {
		a.currentFunctionValues = mergedFunctionValues
	}
	merged := MergeTypes(left, right)
	if IsInvalidType(merged) {
		a.errorf(expr.Pos(), "ternary branches are incompatible: %s and %s", left.String(), right.String())
	}
	a.recordAnalyzedExprType(expr, merged)
	return merged
}

func (a *Analyzer) analyzeListLitExprWithExpected(expr *ast.ListLitExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	expectedArray, useExpectedArray := contextualArrayLiteralType(expected)
	expectedDArray, useExpectedDArray := contextualDArrayLiteralType(expected)
	if len(expr.Elems) == 0 {
		switch {
		case useExpectedArray:
			if expectedArray.HasConstSize && expectedArray.ConstSize != 0 {
				a.errorf(expr.Pos(), "array literal expects %d elements, got 0", expectedArray.ConstSize)
			}
			a.exprTypes[expr] = expectedArray
			return expectedArray
		case useExpectedDArray:
			a.exprTypes[expr] = expectedDArray
			return expectedDArray
		}
		a.errorf(expr.Pos(), "empty list literal requires an expected array or darray type")
		a.exprTypes[expr] = invalidType
		return invalidType
	}

	var elemType Type
	if useExpectedArray {
		elemType = expectedArray.Elem
		if expectedArray.HasConstSize && expectedArray.ConstSize != int64(len(expr.Elems)) {
			a.errorf(expr.Pos(), "array literal expects %d elements, got %d", expectedArray.ConstSize, len(expr.Elems))
		}
	} else if useExpectedDArray {
		elemType = expectedDArray.Elem
	}

	for _, elem := range expr.Elems {
		itemType := a.analyzeValueExpr(elem, elemType)
		if useExpectedArray {
			if !AssignableTo(expectedArray.Elem, itemType) {
				a.errorf(elem.Pos(), "array literal element expects %s, got %s", expectedArray.Elem.String(), itemType.String())
			}
			a.consumeAffineValueExpr(elem, expectedArray.Elem, "move into array literal element")
			continue
		}
		if useExpectedDArray {
			if !AssignableTo(expectedDArray.Elem, itemType) {
				a.errorf(elem.Pos(), "darray literal element expects %s, got %s", expectedDArray.Elem.String(), itemType.String())
			}
			a.consumeAffineValueExpr(elem, expectedDArray.Elem, "move into darray literal element")
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

	if useExpectedArray {
		a.exprTypes[expr] = expectedArray
		return expectedArray
	}
	if useExpectedDArray {
		a.exprTypes[expr] = expectedDArray
		return expectedDArray
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

func contextualDArrayLiteralType(expected Type) (*DArrayType, bool) {
	darrayType, ok := expected.(*DArrayType)
	if !ok {
		return nil, false
	}
	return darrayType, true
}

func (a *Analyzer) variantConstructorMoveReason(kind string, containerName string, variant *EnumVariant, index int) string {
	if variant == nil {
		return "move into " + kind + " constructor payload"
	}
	if label := variant.PayloadLabel(index); label != "" {
		return "move into " + kind + " payload " + strconv.Quote(containerName+"."+variant.Name+"."+label)
	}
	return "move into " + kind + " payload " + strconv.Quote(containerName+"."+variant.Name) + " argument " + strconv.Itoa(index+1)
}

func (a *Analyzer) enumConstructorMoveReason(enumName string, variant *EnumVariant, index int) string {
	return a.variantConstructorMoveReason("enum", enumName, variant, index)
}

func containsTypeParam(t Type) bool {
	switch n := t.(type) {
	case nil:
		return false
	case *TypeParamType:
		return true
	case *RefStorageParamType, *RefStateParamType:
		return true
	case *ErrorUnionType:
		return containsTypeParam(n.Value)
	case *OptionalType:
		return containsTypeParam(n.Value)
	case *RefType:
		return n.StateParam != "" || n.StorageParam != "" || containsTypeParam(n.Elem)
	case *ArrayType:
		return containsTypeParam(n.Elem)
	case *DArrayType:
		return containsTypeParam(n.Elem)
	case *ViewType:
		return containsTypeParam(n.Elem)
	case *DArrayViewType:
		return containsTypeParam(n.Elem)
	case *TupleType:
		for _, field := range n.Fields {
			if containsTypeParam(field.Type) {
				return true
			}
		}
		return false
	case *GenericInstanceType:
		for _, arg := range n.Args {
			if containsTypeParam(arg) {
				return true
			}
		}
		return containsTypeParam(n.Base)
	case *AggregateStateType:
		return containsTypeParam(n.Base)
	case *FuncType:
		if len(n.RefStorageParams) != 0 || len(n.RefStateParams) != 0 || len(n.GenericParams) != 0 {
			return true
		}
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
			if sym, _, ok = a.lookupVisibleGlobal(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			if ref, ok := sym.Type.(*RefType); ok {
				if !ref.Mutable {
					a.errorf(n.Pos(), "cannot assign through readonly ref %q", sym.Name)
					a.reportReadonlyRefMutationNote(n.Pos(), n, sym.Type)
					return invalidType
				}
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
		if n.Safe {
			a.errorf(n.Pos(), "optional chaining cannot be used as an assignment target")
			return invalidType
		}
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		a.requireWritableMutationPath(n.Object)
		return field.Type
	case *ast.IndexExpr:
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot assign to %s", kind)
			return invalidType
		}
		if facts, ok := a.exprFacts[n.Object]; ok && facts.ReadOnly {
			a.errorf(n.Pos(), "cannot assign to readonly view index result")
			return invalidType
		}
		a.requireWritableMutationPath(n.Object)
		return targetType
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func stripMutationTargetExpr(expr ast.Expr) ast.Expr {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.MoveExpr:
			expr = n.Operand
		case *ast.CanExpr:
			expr = n.Expr
		default:
			return expr
		}
	}
}

func (a *Analyzer) mutationPathWritable(expr ast.Expr) bool {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return true
	}
	objType, ok := a.exprTypes[stripped]
	if !ok || objType == nil {
		objType = a.analyzeExpr(stripped)
	}
	if ref, ok := objType.(*RefType); ok {
		return a.refExprAllowsMutation(stripped, ref)
	}
	switch n := stripped.(type) {
	case *ast.FieldExpr:
		return a.mutationPathWritable(n.Object)
	case *ast.IndexExpr:
		return a.mutationPathWritable(n.Object)
	case *ast.SliceExpr:
		return a.mutationPathWritable(n.Object)
	default:
		return true
	}
}

func (a *Analyzer) requireWritableMutationPath(expr ast.Expr) bool {
	if a.mutationPathWritable(expr) {
		return true
	}
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return false
	}
	a.errorf(stripped.Pos(), "cannot mutate through readonly ref")
	a.reportReadonlyRefMutationNote(stripped.Pos(), stripped, nil)
	return false
}

func mutableRefSuggestionString(t Type) (string, bool) {
	ref, ok := t.(*RefType)
	if !ok || ref == nil {
		return "", false
	}
	cloned := cloneRefType(ref)
	cloned.Mutable = true
	return cloned.String(), true
}

func writableRefAssignableIgnoringMutability(expected Type, actual Type) bool {
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil || !expectedRef.Mutable {
		return false
	}
	actualRef, ok := actual.(*RefType)
	if !ok || actualRef == nil || actualRef.Mutable {
		return false
	}
	expectedReadonly := cloneRefType(expectedRef)
	expectedReadonly.Mutable = false
	actualReadonly := cloneRefType(actualRef)
	actualReadonly.Mutable = false
	return AssignableTo(expectedReadonly, actualReadonly)
}

func (a *Analyzer) writableRefSuggestionForExpr(expr ast.Expr) (string, bool) {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return "", false
	}
	t := a.exprTypes[stripped]
	if t == nil {
		t = a.analyzeExpr(stripped)
	}
	if suggestion, ok := mutableRefSuggestionString(t); ok {
		return suggestion, true
	}
	switch n := stripped.(type) {
	case *ast.FieldExpr:
		return a.writableRefSuggestionForExpr(n.Object)
	case *ast.IndexExpr:
		return a.writableRefSuggestionForExpr(n.Object)
	case *ast.SliceExpr:
		return a.writableRefSuggestionForExpr(n.Object)
	default:
		return "", false
	}
}

func (a *Analyzer) reportReadonlyRefMutationNote(pos lexer.Pos, expr ast.Expr, t Type) {
	if a == nil {
		return
	}
	if suggestion, ok := mutableRefSuggestionString(t); ok {
		a.errorf(pos, "note: plain refs T& are readonly; use %s if this reference should allow writes", suggestion)
		return
	}
	if suggestion, ok := a.writableRefSuggestionForExpr(expr); ok {
		a.errorf(pos, "note: plain refs T& are readonly; use %s if this reference should allow writes", suggestion)
		return
	}
	a.errorf(pos, "note: plain refs T& are readonly; use mutable T& if this reference should allow writes")
}

func (a *Analyzer) reportMutableRefArgumentNote(pos lexer.Pos, expected Type, actual Type) {
	if a == nil || !writableRefAssignableIgnoringMutability(expected, actual) {
		return
	}
	if suggestion, ok := mutableRefSuggestionString(expected); ok {
		a.errorf(pos, "note: use %s here if the callee should be allowed to write through it", suggestion)
	}
}

func (a *Analyzer) refExprAllowsMutation(expr ast.Expr, ref *RefType) bool {
	if ref == nil {
		return false
	}
	if ref.Mutable {
		return true
	}
	stripped := stripMutationTargetExpr(expr)
	switch n := stripped.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, _, ok = a.lookupVisibleGlobal(n.Name); !ok {
				return false
			}
		}
		return sym.Mutable
	case *ast.FieldExpr:
		if n.Safe {
			return false
		}
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return false
		}
		return field.Mutable
	case *ast.IndexExpr:
		return a.mutationPathWritable(n.Object)
	default:
		return false
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
			if sym, _, ok = a.lookupVisibleGlobal(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			if ref, ok := sym.Type.(*RefType); ok {
				if !ref.Mutable {
					a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
					return a.refTypeWithAsKind(sym.Type, asKind)
				}
				return a.refTypeWithAsKind(sym.Type, asKind)
			}
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
		}
		return a.refTypeWithAsKind(sym.Type, asKind)
	case *ast.FieldExpr:
		if n.Safe {
			a.errorf(n.Pos(), "optional chaining cannot be used as a reference target")
			return invalidType
		}
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		a.requireWritableMutationPath(n.Object)
		return a.refTypeWithAsKind(field.Type, asKind)
	case *ast.IndexExpr:
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot take a reference to %s", kind)
			return invalidType
		}
		if facts, ok := a.exprFacts[n.Object]; ok && facts.ReadOnly {
			a.errorf(n.Pos(), "cannot take a reference to readonly view index result")
			return invalidType
		}
		a.requireWritableMutationPath(n.Object)
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
		if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok && sym.Kind == SymbolGlobal {
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
	return a.lookupFieldWithDiagnostics(objType, fieldName, pos, true)
}

func (a *Analyzer) lookupFieldNoError(objType Type, fieldName string) (Field, bool) {
	return a.lookupFieldWithDiagnostics(objType, fieldName, lexer.Pos{}, false)
}

func (a *Analyzer) lookupFieldWithDiagnostics(objType Type, fieldName string, pos lexer.Pos, emitDiagnostics bool) (Field, bool) {
	if field, ok := dstrSyntheticField(objType, fieldName); ok {
		return field, true
	}
	if field, ok := dictEntrySyntheticField(objType, fieldName); ok {
		return field, true
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			if emitDiagnostics {
				a.errorf(pos, "field access requires proven non-null reference, got %s", objType.String())
			}
			return Field{}, false
		}
		objType = ref.Elem
	}
	objType = StripAggregateStateType(objType)
	if field, ok := storeRowViewField(objType, fieldName); ok {
		return field, true
	}
	if dictType, ok := objType.(*DictType); ok && !dictSupportsRuntimeBackedOps(dictType) {
		if emitDiagnostics {
			a.errorf(pos, "%s", runtimeBackedDictSupportDiagnostic(dictType))
		}
		return Field{}, false
	}
	if field, ok := packedStoreSyntheticField(objType, fieldName); ok {
		return field, true
	}
	if field, ok := TreeKindFieldInfo(objType); ok && fieldName == field.Name {
		return field, true
	}
	if viewType, ok := objType.(*PackedVariantViewType); ok {
		field, ok := viewType.Field(fieldName)
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "%s has no field %q", viewType.String(), fieldName)
			}
			return Field{}, false
		}
		return field, true
	}
	if viewType, ok := objType.(*TreeVariantViewType); ok {
		field, ok := TreeVariantSurfaceFieldInfo(viewType, fieldName)
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "%s has no field %q", viewType.String(), fieldName)
			}
			return Field{}, false
		}
		return field, true
	}
	if enumType, ok := objType.(*EnumType); ok && enumType.Packed {
		field, ok := enumType.Common[fieldName]
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "packed enum %q has no common field %q", enumType.Name, fieldName)
			}
			return Field{}, false
		}
		return field, true
	}
	if categoryType, ok := objType.(*TreeCategoryType); ok {
		field, ok := TreeCategorySurfaceFieldInfo(categoryType, fieldName)
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "tree category %q has no common field %q", categoryType.Name, fieldName)
			}
			return Field{}, false
		}
		return field, true
	}
	if runtimeBacked := a.runtimeBackedStructType(objType); runtimeBacked != nil {
		objType = runtimeBacked
	}
	switch t := objType.(type) {
	case *TupleType:
		for _, field := range t.Fields {
			if field.Name == fieldName {
				return Field{Name: field.Name, Type: field.Type}, true
			}
		}
		if emitDiagnostics {
			a.errorf(pos, "tuple %s has no field %q", t.String(), fieldName)
		}
		return Field{}, false
	case *StructType:
		field, ok := t.Fields[fieldName]
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "struct %q has no field %q", t.Name, fieldName)
			}
			return Field{}, false
		}
		return field, true
	case *TreeBlockType:
		field, ok := TreeExactSurfaceFieldInfo(t, fieldName)
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "tree block %q has no field %q", t.Name, fieldName)
			}
			return Field{}, false
		}
		return field, true
	case *TreeStructType:
		field, ok := TreeExactSurfaceFieldInfo(t, fieldName)
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "tree struct %q has no field %q", t.Name, fieldName)
			}
			return Field{}, false
		}
		return field, true
	case *GenericInstanceType:
		baseStruct, ok := t.Base.(*StructType)
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "field access requires struct type, got %s", objType.String())
			}
			return Field{}, false
		}
		field, ok := baseStruct.Fields[fieldName]
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "struct %q has no field %q", baseStruct.Name, fieldName)
			}
			return Field{}, false
		}
		bindings := genericBindingsForStructInstance(baseStruct, t.Args)
		field.Type = a.substituteType(field.Type, bindings, nil, nil, nil)
		return field, true
	default:
		if emitDiagnostics {
			a.errorf(pos, "field access requires struct type, got %s", objType.String())
		}
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
		if !dictSupportsRuntimeBackedOps(dict) {
			return nil
		}
		base, ok := a.namedTypes["DynDict"]
		if !ok {
			return nil
		}
		return &GenericInstanceType{Name: "DynDict", Base: base, Args: []Type{dict.Key, dict.Value}}
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
	if _, ok := t.(*PackedEnumStoreType); ok {
		return "packed store index result", true
	}
	if view, ok := t.(*DArrayViewType); ok {
		if view.SurfaceName == "packedview" {
			return "packed store view index result", true
		}
		if view.SurfaceName == "packedtags" {
			return "packed store tag view index result", true
		}
	}
	if _, ok := t.(*DStrType); ok {
		return "string index", true
	}
	if array, ok := t.(*ArrayType); ok && isStringArrayType(array) {
		return "string index", true
	}
	if isStringViewType(t) {
		return "string view index", true
	}
	if _, ok := ChunksExactViewItemType(t); ok {
		return "chunked view index result", true
	}
	ref, ok := t.(*RefType)
	if !ok {
		return "", false
	}
	if _, ok := ref.Elem.(*PackedEnumStoreType); ok {
		return "packed store index result", true
	}
	if view, ok := ref.Elem.(*DArrayViewType); ok {
		if view.SurfaceName == "packedview" {
			return "packed store view index result", true
		}
		if view.SurfaceName == "packedtags" {
			return "packed store tag view index result", true
		}
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
	if _, ok := ChunksExactViewItemType(ref.Elem); ok {
		return "chunked view index result", true
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

func dictEntrySyntheticField(t Type, fieldName string) (Field, bool) {
	entryType, _, ok := builtinDictEntryReceiverType(t)
	if !ok || entryType == nil || entryType.Dict == nil {
		return Field{}, false
	}
	switch fieldName {
	case "found":
		return Field{Name: "found", Type: &BuiltinType{Name: "bool"}, Mutable: false}, true
	case "value":
		return Field{Name: "value", Type: builtinDictEntryValueRefType(entryType.Dict), Mutable: false}, true
	default:
		return Field{}, false
	}
}

func packedStoreSyntheticField(t Type, fieldName string) (Field, bool) {
	storeType, ok := t.(*PackedEnumStoreType)
	if !ok {
		return Field{}, false
	}
	switch fieldName {
	case "count":
		return Field{Name: "count", Type: builtinUsizeType(), Mutable: false}, true
	case "tags":
		if !IsFrozenPackedEnumStoreType(storeType) || storeType.Enum == nil || storeType.Enum.TagType == nil {
			return Field{}, false
		}
		return Field{Name: "tags", Type: &DArrayViewType{Elem: storeType.Enum.TagType, SurfaceName: "packedtags"}, Mutable: false}, true
	}
	return Field{}, false
}

func builtinI64Type() Type {
	return &BuiltinType{Name: "i64"}
}

func builtinUsizeType() Type {
	return &BuiltinType{Name: "usize"}
}

func builtinCharType() Type {
	return &BuiltinType{Name: "char"}
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
