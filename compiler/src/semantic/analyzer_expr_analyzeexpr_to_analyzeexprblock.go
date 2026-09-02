package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) analyzeExpr(expr ast.Expr) (result Type) {
	defer func() {
		if expr != nil {
			// A suppressed-diagnostics speculative pass (return-provenance /
			// borrow inference) may fail to resolve names its real analysis
			// resolved; never let it overwrite a previously computed valid
			// type with <invalid> - codegen reads this cache.
			if !(a.suppressDiagnostics && IsInvalidType(result) && !IsInvalidType(a.exprTypes[expr]) && a.exprTypes[expr] != nil) {
				a.exprTypes[expr] = result
			}
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
				if sym.Ghost {
					a.ghostReadSeen = true
				}
				if sym.Ghost && a.ghostReadAllowed == 0 {
					// SOUNDNESS: a ghost var is verification-only and erased from codegen. Real runtime
					// code may never read it (the value does not exist at runtime), and no real value may
					// be assigned from it. Ghost reads are permitted ONLY inside a contract clause
					// (requires/ensure/invariant/assert) or another ghost var's initializer — contexts
					// that raise a.ghostReadAllowed.
					a.errorf(n.Pos(), "ghost variable %q is verification-only and cannot be read by real code: it is erased from codegen, so it may appear only in contracts (requires/ensure/invariant/assert) or another `ghost` declaration", n.Name)
					return
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
				if a.reportRawInteriorAffineAliasUse(n, sym) {
					return
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
		if sym, canonical, ok := a.lookupVisibleGlobal(n.Name); ok {
			a.reportUsingAmbiguity(n.Name, n.Position)
			// Record the resolved canonical (namespace/using/import-qualified) name so
			// the interpreter can resolve namespaced value/function references instead
			// of re-deriving from the bare name without namespace context.
			if a.resolvedValueNames != nil && canonical != "" && canonical != n.Name {
				a.resolvedValueNames[n] = canonical
			}
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
		if qualified, owner, ok := a.inaccessiblePrivateName(n.Name); ok {
			a.errorf(n.Pos(), "%s", PrivateNameMessage(qualified, owner))
		} else {
			a.errorf(n.Pos(), "%s", UndefinedIdentifierMessage(n.Name))
		}
		result = invalidType
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
	case *ast.CastExpr:
		// Postfix-shorthand `recv.Name()` parses as a cast to type `Name` (this is
		// how `x.u64()` works). But `recv.Name()` is meant to read as `Name(recv)`:
		// when `Name` is a type it is a constructor/cast, and otherwise it is a
		// function — including a UFCS method. When the target does NOT resolve to
		// a type, re-interpret the cast as the call `Name(recv)` so PascalCase method
		// names work the same as the lowercase / multi-arg forms already do.
		if n.Origin == ast.CastExprOriginPostfixShorthand {
			if named, ok := n.Target.(*ast.NamedType); ok && named != nil && named.Name != "" {
				// Quietly probe whether the target resolves to a real type (this
				// covers builtins like sview/cstr/dstr that resolveType handles). If
				// it does not, `recv.Name()` is the call `Name(recv)` instead.
				savedSuppress := a.suppressDiagnostics
				a.suppressDiagnostics = true
				probe := a.resolveType(named)
				a.suppressDiagnostics = savedSuppress
				if IsInvalidType(probe) {
					synth := &ast.CallExpr{
						Position: n.Position,
						Func:     &ast.FieldExpr{Position: n.Position, Object: n.Operand, Field: named.Name},
					}
					a.postfixShorthandCalls[n] = synth
					result = a.analyzeExpr(synth)
					a.exprTypes[n] = result
					return
				}
			}
		}
		dst := a.resolveType(n.Target)
		var src Type
		if _, ok := n.Operand.(*ast.ZeroedLit); ok {
			src = a.analyzeValueExpr(n.Operand, dst)
		} else {
			src = a.analyzeExpr(n.Operand)
		}
		if n.Origin == ast.CastExprOriginPostfixShorthand {
			if hookSym, ok := a.lookupVisibleCastHook(src, dst); ok && !a.isSelfCastHook(hookSym) {
				a.resolvedCastHooks[n] = hookSym
				if fnType, ok := hookSym.Type.(*FuncType); ok {
					a.recordFunctionPermissionRefs(functionPermissionRefs(fnType))
				}
				result = dst
				return
			}
		}
		if n.Origin == ast.CastExprOriginExplicitCast && a.isValueConversion(src, dst) {
			// `.cast[T]` is the canonical reinterpret/bitcast; numeric/enum value conversions go
			// through a constructor (`T(x)` / `x.T()`).
			a.errorf(n.Pos(), "`.cast[%s]` is a value conversion, not a reinterpret; use a constructor `%s(x)` or `x.%s()`", dst, dst, dst)
		} else if !a.validCast(src, dst) {
			a.errorf(n.Pos(), "invalid cast from %s to %s", src, dst)
		} else if n.Origin == ast.CastExprOriginExplicitCast && !IsInvalidType(src) && SameType(src, dst) {
			// A reinterpret `.cast[T]` whose operand already HAS type T does nothing — the bits,
			// mutability, region, and storage all match (SameType is exact, so a genuine
			// mutability/region/lifetime-changing cast is not flagged here). Almost always left
			// over after a refactor (e.g. the operand was narrowed or its type changed). Lint it.
			a.warnOncef(n.Pos(), "redundant `.cast[%s]`: the operand already has type %s; remove the cast", dst, dst)
		}
		// A cast is the explicit way to truncate, so runtime narrowing stays silent. But a
		// cast of a compile-time constant that cannot fit the (sub-64-bit) target type
		// silently changes its value — almost always a bug. Warn-on-lossy. (64-bit targets
		// are skipped: a u64 const above int64 is stored as a negative bit pattern.)
		if _, width, ok := BitIntInfo(dst); ok && width < 64 {
			if value, vok := a.evalConstExpr(n.Operand); vok && value.Kind == ConstInt && !IntegerTypeFitsValue(dst, value.Int) {
				a.warnf(n.Pos(), "constant %d does not fit in %s and is truncated by this cast", value.Int, dst)
			}
		}
		// The `x.ref[T]` reference shorthand has been removed in favor of the explicit
		// `&x` (borrow) / `(&x).cast[T]` (reinterpret) forms. Reject every use.
		// Classify so the message points at the right replacement: BORROW (-> `&x`)
		// iff matching pointee (ignoring mutability), the target is not the universal
		// `void&` type-erasure pointer, and `&x`'s mutability suffices — `&x`'s real
		// mutability is exactly `exprCanYieldWritableRef(x)` (the predicate AddrOf
		// uses), so a mutable target on a non-writable place is a mutability-forcing
		// reinterpret, not a borrow. Everything else -> `(&x).cast[T]`.
		if n.RefShorthand {
			replacement := "`(&x).cast[T]` to reinterpret"
			if addr, ok := n.Operand.(*ast.AddrOfExpr); ok {
				voidElem := a.namedTypes["void"]
				if srcRef, sok := src.(*RefType); sok {
					if dstRef, dok := dst.(*RefType); dok && !SameType(dstRef.Elem, voidElem) && SameType(srcRef.Elem, dstRef.Elem) {
						if !dstRef.Mutable || a.exprCanYieldWritableRef(addr.Operand) {
							replacement = "`&x` to borrow"
						}
					}
				}
			}
			a.errorf(n.Pos(), "`x.ref[T]` reference shorthand has been removed; use %s", replacement)
		}
		// `value.cast[T&]` where the value's own type IS T reinterprets the value's bits as a
		// pointer (a wild pointer), not a reference to it — `.cast` does not take an address.
		// Almost always a mistake for `&value`. A genuine int->ptr cast targets a DIFFERENT
		// pointee type (e.g. `addr.cast[u8&]`), so it is not flagged.
		if n.Origin == ast.CastExprOriginExplicitCast {
			if dstRef, ok := dst.(*RefType); ok {
				_, srcIsRef := src.(*RefType)
				// Exclude pointer-width address types (uintptr/usize): a value of that type IS an
				// address, so `addr.cast[uintptr&]` is a legitimate int->ptr, not a missed `&`.
				srcIsAddress := SameType(src, a.namedTypes["uintptr"]) || SameType(src, a.namedTypes["usize"])
				if !srcIsRef && !srcIsAddress && !IsInvalidType(src) && SameType(src, dstRef.Elem) {
					a.warnf(n.Pos(), "casting a value to a reference of its own type reinterprets it as a raw pointer, not a reference to it; use the `&` operator to take a reference (e.g. `&x`)")
				}
			}
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
		// S4 Stage 3: a reinterpret cast to a region-LESS reference inherits the operand's pointee
		// region — a reborrow `(&x).cast[T&]` or a darray-element ref (`xs[i].cast[T&]`) aliases the
		// same allocation, so the result lives in the same region. Sound: a reinterpret cannot change
		// where the pointee lives, so it cannot forge a longer-lived region (it only recovers the
		// region the cast would otherwise erase); an explicit `@r` on the target is respected (only a
		// region-less target inherits). This lets region-poly threading survive the loader's pervasive
		// `(&self).cast[mutable Module&]` reborrow idiom instead of forcing an explicit `in perm:`.
		if dstRef, ok := dst.(*RefType); ok && dstRef.Region == "" {
			if region := a.returnBorrowedRegion(n.Operand); region != "" {
				cloned := cloneRefType(dstRef)
				cloned.Region = region
				dst = cloned
			}
		}
		result = dst
		return
	case *ast.SizeofExpr:
		a.resolveType(n.Type)
		result = a.namedTypes["usize"]
		return
	case *ast.AlignofExpr:
		a.resolveType(n.Type)
		result = a.namedTypes["usize"]
		return
	case *ast.OffsetofExpr:
		t := a.resolveType(n.Type)
		a.lookupField(t, n.Field, n.Pos())
		result = a.namedTypes["usize"]
		return
	case *ast.QuantifierExpr:
		// A spec-only bound quantifier (docs/90 brick 90-4). Binders range over the integers; the body
		// must be bool. Type-checked in a child scope so the binders are in scope for the body and gone
		// after. Never compiled to a runtime check (suppressed at discharge) — provable only by SMT.
		var sourceType Type
		if n.In != nil {
			sourceType = a.analyzeExpr(n.In)
		}
		saved := a.currentScope
		scope := NewScope(saved)
		intType := a.namedTypes["i64"]
		binderTypes := make([]Type, len(n.Vars))
		for i := range binderTypes {
			binderTypes[i] = intType
		}
		if n.In != nil {
			switch ct := stripRefForBounds(sourceType).(type) {
			case *ArrayType:
				if len(n.Vars) == 1 {
					binderTypes[0] = ct.Elem
				}
			case *DArrayType:
				if len(n.Vars) == 1 {
					binderTypes[0] = ct.Elem
				}
			case *SetType:
				if len(n.Vars) == 1 {
					binderTypes[0] = ct.Elem
				}
			case *DictType:
				if len(n.Vars) == 2 {
					binderTypes[0] = ct.Key
					binderTypes[1] = ct.Value
				}
			}
		}
		for i, v := range n.Vars {
			scope.Define(&Symbol{Name: v, Kind: SymbolLocal, Type: binderTypes[i]})
		}
		a.currentScope = scope
		bodyType := a.analyzeExpr(n.Body)
		a.currentScope = saved
		if bodyType != nil && !IsBoolType(bodyType) {
			a.errorf(n.Body.Pos(), "quantifier body must be bool, got %s", bodyType)
		}
		boolType := a.namedTypes["bool"]
		a.exprTypes[n] = boolType
		return boolType
	case *ast.TernaryExpr:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) && !IsInvalidType(condType) {
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
			// Literal-aware fallback, mirroring mergeMatchExprArmTypes: a bare
			// string-LITERAL branch adapts to a string-view branch even without a
			// contextual expected type (e.g. a ternary nested inside a match arm).
			// Non-literal `static u8&` values stay incompatible.
			merged = mergeTernaryBranchTypes(left, right, n.Value, n.Alt)
		}
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
	// docs/119 E4: a value block is pure over outer state — reject direct writes to
	// enclosing bindings before analyzing the body (locals aren't defined yet, so an
	// outer name still resolves to its ancestor-scope binding). The returned allowed-set
	// is pushed so the mutating-CALL half of E4 can consult it during body analysis.
	allowed := a.checkValueBlockOuterMutation(expr)
	a.valueBlockAllowed = append(a.valueBlockAllowed, allowed)
	var result Type
	for _, stmt := range expr.Stmts {
		a.analyzeStmt(stmt)
	}
	result = a.analyzeExpr(expr.Value)
	a.valueBlockAllowed = a.valueBlockAllowed[:len(a.valueBlockAllowed)-1]
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


// narrowedOptionalDeclaredType returns the declared optional type of a place that an
// assignment narrowed to its payload. See Scope.narrowedOptionals.
func (a *Analyzer) narrowedOptionalDeclaredType(place ast.Expr) (Type, bool) {
	if a.currentScope == nil || place == nil {
		return nil, false
	}
	key, ok := exprRefinementKey(place)
	if !ok {
		return nil, false
	}
	return a.currentScope.LookupNarrowedOptional(key)
}
