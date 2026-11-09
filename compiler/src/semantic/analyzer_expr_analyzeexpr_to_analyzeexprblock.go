package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) analyzeExpr(expr ast.Expr) (result Type) {
	if invocation := a.incrementalBodies; invocation != nil && invocation.tracking && !a.suppressDiagnostics {
		invocation.session.stats.PrimaryExprChecks++
	}
	if a.suppressDiagnostics {
		a.shortCircuitStability = nil
	}
	defer func() {
		if expr != nil {
			// A suppressed-diagnostics speculative pass (return-provenance /
			// borrow inference) may fail to resolve names its real analysis
			// resolved; never let it overwrite a previously computed valid
			// type with <invalid> - codegen reads this cache.
			if !(a.suppressDiagnostics && IsInvalidType(result) && !IsInvalidType(a.exprType(expr)) && a.exprType(expr) != nil) {
				a.exprTypes[expr] = result
			}
			a.recordExprOptimizationFacts(expr, result)
		}
	}()
	switch n := expr.(type) {
	case *ast.Ident:
		result = a.analyzeIdentifierExpr(n)
		return
	case *ast.IntLit:
		if n.Suffix != "" {
			if !intLitRequiresSuffix(n) {
				a.warnNumericLiteralSuffix(n, n.Suffix)
			}
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
		result = a.analyzeQueryExpr(n, nil)
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
		// `old(expr)` inside an `ensure` clause: the value of expr at function entry. Types as the
		// inner expr (analyzed in the entry/param state); the backend captures it at entry.
		if a.inEnsureContext && ast.IsOldCall(n) {
			if len(n.Args) != 1 {
				a.errorf(n.Pos(), "old(...) takes exactly one argument")
				result = invalidType
				a.exprTypes[n] = result
				return
			}
			result = a.analyzeExpr(n.Args[0])
			a.exprTypes[n] = result
			return
		}
		result = a.analyzeCallExpr(n)
		return
	case *ast.EnumColumnExpr:
		result = a.analyzeEnumColumnExpr(n)
		return
	case *ast.FieldExpr:
		if a.rejectDotModulePathExpr(n) {
			result = invalidType
			return
		}
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
		if enumType, ctorType, ok := a.enumVariantExprType(n); ok {
			if ctorType != nil {
				result = ctorType
			} else {
				result = enumType
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
		// A `raise` leaves the function like a `return`: every must-consume value
		// still live here leaks on the error path.
		a.reportUnconsumedProtocolValuesOnExit()
		currentUnion, ok := a.currentReturn.(*ErrorUnionType)
		if !ok {
			// Bare expr-lambda inference (docs/64 Phase 5b): the lambda's error return is not yet known;
			// accumulate the raised set and infer it from the body instead of erroring.
			if a.lambdaErrorAccumulate {
				if errSet, ok := errorType.(*ErrorSetType); ok {
					a.lambdaErrorAccum = UnionErrorSets(a.lambdaErrorAccum, errSet)
				}
				result = neverType
				return
			}
			a.errorf(n.Pos(), "raise requires the current function to return an error union%s", a.errorUnionReturnHint())
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
		valueType := a.analyzeExpr(n.Value)
		if IsInvalidType(valueType) {
			if recovery != nil {
				a.analyzeRecoveryClause(recovery, invalidType, invalidType, "try fallback")
			}
			result = invalidType
			return
		}
		if unionType, ok := valueType.(*ErrorUnionType); ok {
			a.consumeHandledErrorUnionExpr(n.Value, unionType, "try")
			if recovery == nil {
				// docs/126 §2: a `try` with no fallback PROPAGATES — the error path
				// leaves the function right here, with no syntax at the drop point.
				// Every live drop-typed local dies on that invisible edge.
				a.noteTryPropagationDrops(n.Pos())
				currentUnion, ok := a.currentReturn.(*ErrorUnionType)
				if !ok {
					// Bare expr-lambda inference (docs/64 Phase 5b): accumulate the propagated set and
					// infer the lambda's error return from the body instead of erroring.
					if a.lambdaErrorAccumulate {
						a.lambdaErrorAccum = UnionErrorSets(a.lambdaErrorAccum, unionType.Errors)
						result = unionType.Value
						return
					}
					a.errorf(n.Pos(), "try without else requires the current function to return an error union%s", a.errorUnionReturnHint())
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
		if optionalType, ok := valueType.(*OptionalType); ok {
			a.errorf(n.Pos(), "`try` on optional values has been removed; write `get <expr> else ...` to make the absence check explicit (`try` is reserved for error unions)")
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
		if n.LegacyImplicitElse {
			a.errorf(n.Pos(), "implicit `else` unwrap has been removed; write `get <expr> else ...` to make the absence check explicit")
		}
		recovery := recoveryClauseForExpr(n.Recovery, n.Fallback, n.Position)
		valueType := a.analyzeExpr(n.Value)
		var resultType Type
		if refType, ok := valueType.(*RefType); ok && refType.State == RefStateNullable {
			resultType = cloneRefTypeWithState(refType, RefStateNonNull)
		} else if optionalType, ok := valueType.(*OptionalType); ok {
			resultType = optionalType.Value
		} else {
			if !IsInvalidType(valueType) {
				a.errorf(n.Pos(), "else recovery requires an optional or nullable reference (refstate fact nullable), got %s", valueType)
			}
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
	case *ast.GetExpr:
		result = a.analyzeGetExpr(n)
		return
	case *ast.OptionalBindExpr:
		valueType := a.analyzeExpr(n.Value)
		// A place an assignment narrowed from `T?` to `T` still carries its present flag
		// in storage, so the bind is meaningful -- it simply always succeeds. Record it at
		// the DECLARED optional type: the backend lowers the test off this same table, and
		// with the narrowed type there it had no optional to unwrap and refused the body.
		if _, bindable := conditionOptionalBindType(valueType); !bindable {
			if declared, narrowed := a.narrowedOptionalDeclaredType(n.Value); narrowed {
				valueType = declared
			}
		}
		if a.optionalBindSourceTypes != nil {
			if existing, ok := a.optionalBindSourceTypes[n]; !ok {
				a.optionalBindSourceTypes[n] = valueType
			} else if _, existingOK := conditionOptionalBindType(existing); !existingOK {
				if _, valueOK := conditionOptionalBindType(valueType); valueOK {
					a.optionalBindSourceTypes[n] = valueType
				}
			}
		}
		if _, ok := a.optionalBindBoundType(n.Value, valueType); !ok {
			operation := "let condition"
			if n.FromIs {
				// docs/80: `x is name` is a refutable bind; on a non-optional value
				// the match can never fail, so the bind is meaningless.
				operation = "`is` binding"
			}
			if !IsInvalidType(valueType) {
				a.errorf(n.Pos(), nullableRefRequirementMessage(operation, valueType.String()))
			}
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
	case *ast.MachineFromExpr:
		result = a.analyzeMachineFromExpr(n, nil)
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
				a.errorf(n.Pos(), "emit all expects a darray or view source, got %s", valueType)
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
	case *ast.CastExpr, *ast.SizeofExpr, *ast.AlignofExpr, *ast.OffsetofExpr, *ast.QuantifierExpr, *ast.TernaryExpr:
		result = a.analyzeValueTypeExpr(expr)
		return
	case *ast.AddrOfExpr:
		// Taking a (mutable) address of a `zeroed`-uninitialized local may
		// initialize it through the pointer; clear the state and don't treat the
		// operand as a value read.
		a.suppressUninitReadCheck++
		inner := a.analyzeExpr(n.Operand)
		a.suppressUninitReadCheck--
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
		region := ""
		// Taking the address of a container element borrows the container's
		// backing storage. Keep that owner on the resulting reference so a
		// borrowed element cannot outlive the darray/view that contains it.
		if indexed, ok := stripParenExpr(n.Operand).(*ast.IndexExpr); ok && indexed != nil {
			region = containerRegion(a.exprType(indexed.Object))
			// A darray's `.items` is its lowered backing array. Its own
			// type does not carry the source container's region, so indexing
			// `values.items[i]` must inherit the region of `values` itself.
			// This keeps the returned element borrow tied to the owning
			// darray while leaving unrelated field/index paths unchanged.
			if field, ok := stripParenExpr(indexed.Object).(*ast.FieldExpr); ok && field != nil && field.Field == "items" {
				region = containerRegion(a.exprType(field.Object))
			}
		}
		result = &RefType{Elem: inner, Mutable: a.exprCanYieldWritableRef(n.Operand), State: RefStateNonNull, Storage: a.inferAddrOfStorage(n.Operand), Region: region, ExplicitStorage: true}
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
