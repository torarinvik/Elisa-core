package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
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
			// A private global reachable through the scope chain must still respect
			// visibility: skip it here so resolution falls through to the global
			// lookup (which also filters it) and reports an undefined name when
			// accessed from outside its owning module. Locals/params are never
			// Private, so this only affects qualified globals.
			if sym, ok := a.currentScope.Lookup(n.Name); ok && !(sym.Private && !a.canAccessPrivateName(n.Name)) {
				result = promoteWritableRefType(sym.Type, sym.Mutable)
				if a.suppressGlobalReadCheck == 0 && isGlobalStorageSymbol(sym) {
					a.recordFunctionPermissionRefs(globalReadRefs(n.Position))
					if a.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
						a.recordFunctionPermissionRefs(unsafeMutableGlobalRefs(n.Position))
					}
				}
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
						a.errorf(n.Pos(), invalidatedRegionFactUseMessage(label, n.Name, dep.InvalidatedBy))
						return
					}
				}
				a.reportInvalidStorageViewUse(n)
				if state, ok := a.lookupAffineValueState(n); ok && a.containsAffineHandleValues(result, map[string]bool{}) {
					a.errorf(n.Pos(), consumedFactUseMessage(affineHandleKind(sym.Type), n.Name, state.ConsumedBy))
					return
				}
				if a.suppressUninitReadCheck == 0 && a.isZeroedUninitializedSymbol(sym) {
					a.errorf(n.Pos(), "use of uninitialized variable %q: it was declared `= zeroed` and never assigned before this read; assign it (or a field of it) first", n.Name)
					return
				}
				if ownerType, ok := borrowableOwnerRefElemType(result); ok {
					if key, ok := a.lookupBorrowedOwnerRefKey(n); ok {
						if state, ok := a.lookupAffineValueStateForKey(key); ok && state.ConsumedBy != "" {
							a.errorf(n.Pos(), consumedFactUseMessage(affineHandleKind(ownerType), n.Name, state.ConsumedBy))
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
			if a.suppressGlobalReadCheck == 0 && isGlobalStorageSymbol(sym) {
				a.recordFunctionPermissionRefs(globalReadRefs(n.Position))
				if a.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
					a.recordFunctionPermissionRefs(unsafeMutableGlobalRefs(n.Position))
				}
			}
			if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok {
				if fnType, ok := a.functionValueTypeForExpr(valueExpr); ok {
					result = promoteWritableRefType(fnType, sym.Mutable)
					return
				}
			}
			return
		}
		if valueParam, ok := a.lookupConstParam(n.Name); ok {
			if param, paramOK := valueParam.(*ConstParamType); paramOK && param.ValueType != nil {
				result = param.ValueType
				return
			}
			if value, valueOK := valueParam.(*ConstValueType); valueOK && value.Value.Kind == ConstInt {
				result = a.namedTypes["usize"]
				return
			}
		}
		if a.currentScope != nil {
			if hint, ok := a.currentScope.LookupConditionalBindingHint(n.Name); ok {
				a.errorf(n.Pos(), "%s", hint)
				result = invalidType
				return
			}
		}
		if rewriteDefaultType, ok := a.analyzeRewriteDefaultIdent(n); ok {
			result = rewriteDefaultType
			return
		}
		if n.Name == "variants" || n.Name == "fields" {
			result = &FuncType{Name: n.Name, Return: &ConstValueType{Value: ConstValue{Kind: ConstUnknown}}}
			return
		}
		a.errorf(n.Pos(), "%s", UndefinedIdentifierMessage(n.Name))
		result = invalidType
		return
	case *ast.IntLit:
		if n.Suffix != "" {
			a.warnNumericLiteralSuffix(n, n.Suffix)
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
			a.warnNumericLiteralSuffix(n, n.Suffix)
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
	case *ast.MembershipRangeExpr:
		a.errorf(n.Pos(), "membership ranges are only valid inside brace membership candidate sets")
		a.analyzeValueExpr(n.Start, nil)
		a.analyzeValueExpr(n.End, nil)
		result = invalidType
		return
	case *ast.ListComprehensionExpr:
		result = a.analyzeListComprehensionExprWithExpected(n, nil)
		return
	case *ast.QueryExpr:
		result = a.analyzeQueryExpr(n)
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
					a.errorf(n.Pos(), "tree constructor %q requires explicit constructor arguments", memberType)
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
		recovery := recoveryClauseForExpr(n.Recovery, n.Fallback, n.Position)
		if n.UsesDefaultShorthandForm {
			a.deprecatedf(n.Pos(), "`try? ... default` is deprecated; use `try ... else ...`")
		}
		valueType := a.analyzeExpr(n.Value)
		if unionType, ok := valueType.(*ErrorUnionType); ok {
			if recovery == nil {
				currentUnion, ok := a.currentReturn.(*ErrorUnionType)
				if !ok {
					a.errorf(n.Pos(), "try without else requires the current function to return an error union")
				} else if !ErrorSetAssignable(currentUnion.Errors, unionType.Errors) {
					a.errorf(n.Pos(), "cannot propagate %s from a function returning %s", ErrorSetDiagnosticName(unionType.Errors), ErrorSetDiagnosticName(currentUnion.Errors))
				}
				result = unionType.Value
				return
			}
			fallbackType := a.analyzeRecoveryClause(recovery, unionType.Value, unionType.Errors, "try fallback")
			if !IsNeverType(fallbackType) && !AssignableTo(unionType.Value, fallbackType) {
				a.errorf(n.Pos(), "try fallback expects %s, got %s", unionType.Value, fallbackType)
				a.reportShapeMismatchNotes(n.Pos(), unionType.Value, fallbackType)
			}
			result = unionType.Value
			return
		}
		if n.UsesDefaultShorthandForm {
			a.analyzeRecoveryClause(recovery, invalidType, nil, "try fallback")
			a.errorf(n.Pos(), "try? ... default requires an error union, got %s", valueType)
			if optionalType, ok := valueType.(*OptionalType); ok {
				result = optionalType.Value
			} else {
				result = invalidType
			}
			return
		}
		if optionalType, ok := valueType.(*OptionalType); ok {
			a.deprecatedf(n.Pos(), "`try` on optional values is deprecated; use `value else ...`")
			if recovery == nil {
				a.errorf(n.Pos(), "try without else requires an error union, got %s", valueType)
				result = optionalType.Value
				return
			}
			fallbackType := a.analyzeRecoveryClause(recovery, optionalType.Value, nil, "try fallback")
			if !IsNeverType(fallbackType) && !AssignableTo(optionalType.Value, fallbackType) {
				a.errorf(n.Pos(), "try fallback expects %s, got %s", optionalType.Value, fallbackType)
				a.reportShapeMismatchNotes(n.Pos(), optionalType.Value, fallbackType)
			}
			result = optionalType.Value
			return
		}
		a.errorf(n.Pos(), "try requires a fallible expression, got %s", valueType)
		result = invalidType
		return
	case *ast.CatchExpr:
		result = a.analyzeCatchExpr(n)
		return
	case *ast.UnwrapElseExpr:
		recovery := recoveryClauseForExpr(n.Recovery, n.Fallback, n.Position)
		valueType := a.analyzeExpr(n.Value)
		var resultType Type
		if refType, ok := valueType.(*RefType); ok && refType.State == RefStateNullable {
			resultType = cloneRefTypeWithState(refType, RefStateNonNull)
		} else if optionalType, ok := valueType.(*OptionalType); ok {
			resultType = optionalType.Value
		} else {
			a.errorf(n.Pos(), "else recovery requires an optional or nullable reference (refstate fact nullable), got %s", valueType)
			result = invalidType
			return
		}
		fallbackType := a.analyzeRecoveryClause(recovery, resultType, nil, "else fallback")
		if !IsNeverType(fallbackType) && !AssignableTo(resultType, fallbackType) {
			a.errorf(n.Pos(), "else fallback expects %s, got %s", resultType, fallbackType)
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
			a.errorf(n.Pos(), nullableRefRequirementMessage("let condition", valueType.String()))
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
	case *ast.EmitExpr:
		if a.currentSequenceRewrite == nil || a.currentSequenceRewrite.OutputElem == nil {
			a.errorf(n.Pos(), "emit is only allowed inside sequence rewrite arms")
			result = invalidType
			return
		}
		if n.Nothing || n.Value == nil {
			result = a.namedTypes["void"]
			return
		}
		valueType := a.analyzeExpr(n.Value)
		if n.All {
			elemType, ok := sequenceRewriteCarrierElemType(valueType)
			if !ok || elemType == nil {
				a.errorf(n.Pos(), "emit all expects a darray or dview source, got %s", valueType)
				result = invalidType
				return
			}
			if !IsNeverType(elemType) && !AssignableTo(a.currentSequenceRewrite.OutputElem, elemType) {
				a.errorf(n.Pos(), "emit all expects elements assignable to %s, got %s", a.currentSequenceRewrite.OutputElem, elemType)
				a.reportShapeMismatchNotes(n.Pos(), a.currentSequenceRewrite.OutputElem, elemType)
				result = invalidType
				return
			}
			result = a.namedTypes["void"]
			return
		}
		if !IsNeverType(valueType) && !AssignableTo(a.currentSequenceRewrite.OutputElem, valueType) {
			a.errorf(n.Pos(), "emit expects %s, got %s", a.currentSequenceRewrite.OutputElem, valueType)
			a.reportShapeMismatchNotes(n.Pos(), a.currentSequenceRewrite.OutputElem, valueType)
			result = invalidType
			return
		}
		result = a.namedTypes["void"]
		return
	case *ast.LambdaExpr:
		result = a.analyzeLambdaExpr(n, nil)
		return
	case *ast.IndexExpr:
		result = a.analyzeIndexExpr(n)
		return
	case *ast.SliceExpr:
		result = a.analyzeSliceExpr(n)
		return
	case *ast.CastExpr:
		dst := a.resolveType(n.Target)
		var src Type
		if _, ok := n.Operand.(*ast.ZeroedLit); ok {
			src = a.analyzeValueExpr(n.Operand, dst)
		} else {
			src = a.analyzeExpr(n.Operand)
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
			a.errorf(n.Pos(), "invalid cast from %s to %s", src, dst)
		}
		// Const-correctness: `const_place.ref[mutable T&]` parses to a cast of a
		// freshly-taken borrow (AddrOfExpr) to a mutable ref. A const lives in
		// read-only storage, so handing out a mutable pointer into it would
		// crash on write. Reject mutable borrows rooted in a const. (Non-const
		// places — including non-`mutable` stack locals borrowed for init — and
		// reinterpret casts of plain pointer values are unaffected.)
		if dstRef, ok := dst.(*RefType); ok && dstRef.Mutable {
			operand := n.Operand
			for {
				if paren, ok := operand.(*ast.ParenExpr); ok {
					operand = paren.Inner
					continue
				}
				break
			}
			if addr, ok := operand.(*ast.AddrOfExpr); ok && a.borrowPlaceRootsInConst(addr.Operand) {
				a.errorf(n.Pos(), "cannot take a mutable reference to a const (it lives in read-only storage); use a read-only reference instead")
			}
		}
		if a.enforceUnsafePermissions && n.Origin != ast.CastExprOriginIndirectCall && a.castRequiresUnsafeGuestHostPointerCast(n, dst) {
			a.recordFunctionPermissionRefs(unsafeGuestHostPointerCastRefs(n.Position))
		} else if a.enforceUnsafePermissions && n.Origin != ast.CastExprOriginIndirectCall && castRequiresUnsafePointerCast(src, dst) {
			a.recordFunctionPermissionRefs(unsafePointerCastRefs(n.Position))
		}
		if srcRef, ok := src.(*RefType); ok {
			if dstRef, ok := dst.(*RefType); ok && srcRef.Mutable && !dstRef.Mutable {
				cloned := cloneRefType(dstRef)
				cloned.Mutable = true
				dst = cloned
			}
		}
		a.checkBufferReinterpretCast(n, dst)
		if a.enforceUnsafePermissions && a.unsafeBufferReinterpretCasts[n] {
			a.recordFunctionPermissionRefs(unsafeBufferReinterpretRefs(n.Position))
		}
		a.recordUnsafeLifetimeWiden(n, src, dst)
		result = dst
		return
	case *ast.SizeofExpr:
		if n.DeprecatedSyntax != "" {
			a.deprecatedf(n.Pos(), "`%s` is deprecated; use `%s`", n.DeprecatedSyntax, n.DeprecatedReplacement)
		}
		a.resolveType(n.Type)
		result = a.namedTypes["usize"]
		return
	case *ast.AlignofExpr:
		if n.DeprecatedSyntax != "" {
			a.deprecatedf(n.Pos(), "`%s` is deprecated; use `%s`", n.DeprecatedSyntax, n.DeprecatedReplacement)
		}
		a.resolveType(n.Type)
		result = a.namedTypes["usize"]
		return
	case *ast.OffsetofExpr:
		if n.DeprecatedSyntax != "" {
			a.deprecatedf(n.Pos(), "`%s` is deprecated; use `%s`", n.DeprecatedSyntax, n.DeprecatedReplacement)
		}
		t := a.resolveType(n.Type)
		a.lookupField(t, n.Field, n.Pos())
		result = a.namedTypes["usize"]
		return
	case *ast.TernaryExpr:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "ternary condition must be bool, got %s", condType)
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
			a.errorf(n.Pos(), "ternary branches are incompatible: %s and %s", left, right)
		}
		result = merged
		return
	case *ast.AddrOfExpr:
		// Taking a (mutable) address of a `zeroed`-uninitialized local may
		// initialize it through the pointer; clear the state and don't treat the
		// operand as a value read.
		a.suppressUninitReadCheck++
		inner := a.analyzeExpr(n.Operand)
		a.suppressUninitReadCheck--
		a.clearZeroedUninitializedForExpr(n.Operand)
		if fieldExpr, ok := stripMutationTargetExpr(n.Operand).(*ast.FieldExpr); ok {
			if _, isTreeField := a.treeSurfaceFieldExprInfo(fieldExpr); isTreeField {
				a.errorf(n.Pos(), "cannot take address of tree field %q; tree values are handle-lowered and fields are value-only", fieldExpr.Field)
			}
		}
		if a.containsAffineHandleValues(inner, map[string]bool{}) && !isBorrowableAffineOwnerType(inner) {
			if _, ok := a.lookupAffineValueKey(n.Operand); ok {
				if isAffineHandleType(inner) {
					kind := affineHandleKind(inner)
					if kind == "linear value" {
						a.errorf(n.Pos(), "cannot take address of linear value")
					} else {
						a.errorf(n.Pos(), "cannot take address of %s", kind)
					}
				} else {
					a.errorf(n.Pos(), "cannot take address of value containing linear handles")
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
	case *ast.RecordUpdateExpr:
		result = a.analyzeRecordUpdateExpr(n)
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
func (a *Analyzer) analyzeRewriteDefaultIdent(expr *ast.Ident) (Type, bool) {
	if expr == nil || expr.Name != "default" {
		return nil, false
	}
	if a.currentRewriteDefault == nil {
		a.errorf(expr.Pos(), "default is only allowed inside a rewrite arm body")
		return invalidType, true
	}
	if !a.currentRewriteDefault.Allowed || a.currentRewriteDefault.ResultType == nil {
		message := a.currentRewriteDefault.Message
		if message == "" {
			message = "default is only allowed inside an exact tree rewrite arm"
		}
		a.errorf(expr.Pos(), "%s", message)
		return invalidType, true
	}
	if a.rewriteDefaults != nil {
		a.rewriteDefaults[expr] = true
	}
	return a.currentRewriteDefault.ResultType, true
}
func (a *Analyzer) rewriteDefaultExactType(expr ast.Expr) (Type, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name != "default" || a.currentRewriteDefault == nil || !a.currentRewriteDefault.Allowed || a.currentRewriteDefault.ExactType == nil {
			return nil, false
		}
		return a.currentRewriteDefault.ExactType, true
	case *ast.ParenExpr:
		return a.rewriteDefaultExactType(n.Inner)
	default:
		return nil, false
	}
}
func (a *Analyzer) analyzeExprBlock(expr *ast.ExprBlock) Type {
	if expr == nil || expr.Value == nil {
		return invalidType
	}
	savedScope := a.currentScope
	a.currentScope = NewScope(savedScope)
	var result Type
	a.withLocalParamPackFrame(func() {
		for _, stmt := range expr.Stmts {
			a.analyzeStmt(stmt)
		}
		result = a.analyzeExpr(expr.Value)
	})
	a.currentScope = savedScope
	return result
}

func recoveryClauseForExpr(recovery *ast.RecoveryClause, fallback ast.Expr, pos lexer.Pos) *ast.RecoveryClause {
	if recovery != nil {
		return recovery
	}
	if fallback == nil {
		return nil
	}
	if raise, ok := fallback.(*ast.RaiseExpr); ok {
		return &ast.RecoveryClause{Position: fallback.Pos(), Kind: ast.RecoveryRaise, Value: raise.Error}
	}
	return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryValue, Value: fallback}
}

func (a *Analyzer) analyzeRecoveryClause(recovery *ast.RecoveryClause, expected Type, errorType Type, label string) Type {
	if recovery == nil {
		return invalidType
	}
	switch recovery.Kind {
	case ast.RecoveryValue:
		return a.analyzeExpr(recovery.Value)
	case ast.RecoveryRaise:
		return a.analyzeExpr(&ast.RaiseExpr{Position: recovery.Position, Error: recovery.Value})
	case ast.RecoveryReturn:
		a.analyzeRecoveryReturn(recovery)
		return neverType
	case ast.RecoveryVoid:
		if expected != nil && !isVoidType(expected) && !IsInvalidType(expected) {
			a.errorf(recovery.Position, "%s cannot use else void for non-void result %s", label, expected)
		}
		return a.namedTypes["void"]
	case ast.RecoveryBlock:
		scope := NewScope(a.currentScope)
		if recovery.Binding != "" {
			if errorType == nil {
				a.errorf(recovery.Position, "else error binding requires an error-union operand")
				errorType = invalidType
			}
			a.defineLocalInScope(scope, &Symbol{Name: recovery.Binding, Kind: SymbolLocal, Type: errorType, Mutable: false}, recovery.Position)
		}
		a.analyzeBlockInScope(recovery.Body, scope)
		if blockDefinitelyExits(recovery.Body) {
			return neverType
		}
		if expected != nil && !isVoidType(expected) && !IsInvalidType(expected) {
			a.errorf(recovery.Position, "%s block must return, raise, or produce %s", label, expected)
		}
		return a.namedTypes["void"]
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeRecoveryReturn(recovery *ast.RecoveryClause) {
	if a.currentReturn == nil {
		a.errorf(recovery.Position, "return recovery is only valid inside a function")
		if recovery.Value != nil {
			a.analyzeExpr(recovery.Value)
		}
		return
	}
	if recovery.Value == nil {
		if !isVoidType(a.currentReturn) {
			a.errorf(recovery.Position, "return recovery expects %s, got void", a.currentReturn)
		}
		return
	}
	valueType := a.analyzeExpr(recovery.Value)
	expectedReturn := a.matchReturnType(valueType)
	if !AssignableTo(expectedReturn, valueType) {
		a.errorf(recovery.Position, "return type expects %s, got %s", expectedReturn, valueType)
		a.reportShapeMismatchNotes(recovery.Position, expectedReturn, valueType)
	}
	a.consumeAffineValueExpr(recovery.Value, expectedReturn, "return")
}
