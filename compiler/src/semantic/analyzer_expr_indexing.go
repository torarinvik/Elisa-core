package semantic

import "elisacore/src/ast"

// exprAsSpecializeTypeArg converts an index subexpression into a type argument for the `fn[T]`
// value-specialization reinterpretation. Only a bare identifier (a type name) is accepted -- that
// covers the value form; multi-arg and generic-type-arg forms are parsed as SpecializeExpr directly.
func exprAsSpecializeTypeArg(e ast.Expr) (ast.TypeExpr, bool) {
	if ident, ok := e.(*ast.Ident); ok && ident != nil {
		return &ast.NamedType{Position: ident.Position, Name: ident.Name}, true
	}
	return nil, false
}

func (a *Analyzer) analyzeIndexExpr(expr *ast.IndexExpr) Type {
	// A reclassified site may take an early non-bounds path (flags/dict/overlay).
	// Revoke only telemetry here; proof/check authority is unchanged.
	if !a.suppressOptimizationFacts {
		a.dropIndexBoundsRuntimeSite(expr)
	}
	// docs/107 typed guest-memory overlay: `base.field[mem]` over a `GuestVAddr[L]` carrier whose
	// layout L is registered is a checked field accessor, not an ordinary index. Recognize and
	// desugar it before analyzing the FieldExpr object (which has no real struct field to resolve).
	if result, handled := a.tryDesugarGuestOverlayIndex(expr); handled {
		return result
	}
	// docs/108 fixed-stride table overlay: `table.field[mem, i]` over a `GuestVAddr[L]` carrier whose
	// layout L declares a `stride`. Recognized only in the table form; recognize before generic
	// index analysis.
	if result, handled := a.tryDesugarGuestOverlayTableIndex(expr); handled {
		return result
	}
	// A two-operand index `obj[a, b]` is only valid as the docs/108 table overlay accessor above.
	// Anything else reaching here is a usage error (ordinary indexing takes one subscript).
	if expr.Index2 != nil {
		a.errorf(expr.Pos(), "overlay-table-index-invalid: two-operand index `obj[a, b]` is only valid as a fixed-stride guest table read `table.field[mem, i]` over a GuestVAddr[L] carrier whose layout declares a stride")
		return invalidType
	}
	objType := a.analyzeExpr(expr.Object)
	// `fn[T]` over a generic function is not an index (functions are not indexable) -- it is a
	// single-type-arg value specialization, the counterpart of the multi-arg `fn[A, B]` the parser
	// produces directly. Reinterpret it as a SpecializeExpr so it materializes the generic function
	// as a value.
	if expr.Fallback == nil {
		if fnType, isFunc := objType.(*FuncType); isFunc && len(genericParamsForFuncType(fnType)) > 0 {
			if typeArg, ok := exprAsSpecializeTypeArg(expr.Index); ok {
				spec := &ast.SpecializeExpr{Position: expr.Position, Operand: expr.Object, TypeArgs: []ast.TypeExpr{typeArg}}
				result := a.analyzeSpecializeExpr(spec)
				expr.AsSpecialize = spec
				a.reportInvalidRegionUse(expr, result)
				a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
				return result
			}
		}
	}
	if flagType, ok := FlagsInstanceType(objType); ok {
		indexType := a.analyzeValueExpr(expr.Index, flagType)
		if expr.Fallback != nil {
			a.errorf(expr.Pos(), "flags membership indexing does not support fallback values")
			_ = a.analyzeValueExpr(expr.Fallback, a.namedTypes["bool"])
		}
		if !AssignableTo(flagType, indexType) {
			a.errorf(expr.Index.Pos(), "flags index expects %s, got %s", flagType, indexType)
			a.reportShapeMismatchNotes(expr.Index.Pos(), flagType, indexType)
		}
		result := a.namedTypes["bool"]
		a.reportInvalidRegionUse(expr, result)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
		return result
	}
	if result, handled := a.analyzeBuiltinDictIndexExpr(expr, objType); handled {
		a.reportInvalidRegionUse(expr, result)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
		return result
	}
	indexExpected := a.namedTypes["usize"]
	if indexExpected == nil {
		indexExpected = builtinUsizeType()
	}
	if storeType, _, ok := builtinStoreReceiverType(objType); ok && storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
		indexExpected = &IDType{Tag: storeType, Storage: a.namedTypes["u32"]}
	}
	indexType := a.analyzeValueExpr(expr.Index, indexExpected)
	a.markIndexBoundsProof(expr, objType)
	if a.indexExprRequiresUncheckedIndexPermission(expr) {
		// A bounds-requiring access that is not statically proven would emit a runtime bounds check;
		// record it for the `NoBoundsChecks` shape law (docs/89). Independent of enforceUnsafePermissions
		// (the shape audit is about what the backend emits, not the unsafe-permission policy).
		a.recordIndexBoundsRuntimeGuard(expr) // distinct current ordinary guard site
		a.currentFunctionGuardedIndexes = append(a.currentFunctionGuardedIndexes, expr.Position)
		if a.enforceUnsafePermissions {
			a.recordFunctionPermissionRefs(unsafeUncheckedIndexRefs(expr.Position))
		}
	}
	finish := func(result Type) Type {
		if expr.Fallback != nil {
			if expr.LegacyElseFallback && !a.getWrappedIndexExprs[expr] {
				a.errorf(expr.Pos(), "implicit `else` index fallback has been removed; write `get <expr>[<index>] else ...` to make the bounds check explicit")
			}
			if !safeIndexFallbackOperandType(objType) {
				a.errorf(expr.Pos(), "index fallback requires an array, darray, view, packed store, or a proven non-null reference to one, got %s", objType)
			}
			if _, ok := NodeKeyEnumType(indexType); ok {
				a.errorf(expr.Index.Pos(), "index fallback does not support dense node-key indices")
			}
			fallbackType := a.analyzeValueExpr(expr.Fallback, result)
			if result != nil && !IsInvalidType(result) && !IsNeverType(fallbackType) && !AssignableTo(result, fallbackType) {
				a.errorf(expr.Pos(), "index fallback expects %s, got %s", result, fallbackType)
				a.reportShapeMismatchNotes(expr.Pos(), result, fallbackType)
			}
		}
		a.reportInvalidRegionUse(expr, result)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
		return result
	}
	// A proven non-null scalar reference (`cursor: mutable usize&`) reads as its referent in index
	// position, exactly as it does as an arithmetic/comparison operand (valueContextOperandType).
	indexCheckType := valueContextOperandType(indexType)
	// P2: an associated-type-typed index (`D.Field` from a generic protocol call) is checked against
	// the concrete base its impls bind, so an integral associated type indexes normally.
	if proj, ok := indexCheckType.(*AssociatedTypeProjection); ok {
		if base, bok := a.associatedTypeProjectionBase(proj); bok {
			indexCheckType = base
		}
	}
	if idType, ok := indexCheckType.(*IDType); ok && idType != nil {
		indexCheckType = idType.Storage
	}
	if _, ok := NodeKeyEnumType(indexType); !ok {
		if !IsNumericType(indexCheckType) {
			a.errorf(expr.Index.Pos(), "index must be numeric, got %s", indexType)
		} else if !IsIntegralStorageType(indexCheckType) {
			a.errorf(expr.Index.Pos(), "index must be integral, got %s", indexType)
		}
	}
	if keyEnum, ok := NodeKeyEnumType(indexType); ok {
		if result, handled := a.analyzeDenseNodeKeyIndexExpr(expr, objType, keyEnum); handled {
			return finish(result)
		}
	}
	if storeType, _, ok := builtinStoreReceiverType(objType); ok && storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
		rowType := &IDType{Tag: storeType, Storage: a.namedTypes["u32"]}
		if !AssignableTo(rowType, indexType) {
			a.errorf(expr.Index.Pos(), "soa row index expects %s, got %s", rowType, indexType)
		}
		return finish(&StoreRowViewType{Store: storeType})
	}
	if arr, ok := objType.(*ArrayType); ok {
		if expr.Fallback == nil {
			a.checkConstantArrayIndexBounds(arr, expr.Index)
		}
		return finish(arr.Elem)
	}
	if darray, ok := objType.(*DArrayType); ok {
		return finish(darray.Elem)
	}
	if view, ok := objType.(*ViewType); ok {
		return finish(view.Elem)
	}
	if view, ok := objType.(*ViewType); ok {
		return finish(view.Elem)
	}
	if tuple, ok := StripAggregateStateType(objType).(*TupleType); ok {
		if fieldType, ok := a.tupleIndexResultType(tuple, expr.Index); ok {
			return finish(fieldType)
		}
		return finish(invalidType)
	}
	if itemType, ok := ChunksExactViewItemType(objType); ok {
		return finish(itemType)
	}
	if storeType, ok := objType.(*PackedEnumStoreType); ok && storeType.Enum != nil {
		return finish(storeType.Enum)
	}
	if _, ok := objType.(*CStrType); ok {
		return finish(a.namedTypes["char"])
	}
	if isStringViewType(objType) {
		return finish(a.namedTypes["char"])
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "indexing requires proven non-null reference, got %s", objType)
			return finish(invalidType)
		}
		if arr, ok := ref.Elem.(*ArrayType); ok {
			if expr.Fallback == nil {
				a.checkConstantArrayIndexBounds(arr, expr.Index)
			}
			return finish(arr.Elem)
		}
		if darray, ok := ref.Elem.(*DArrayType); ok {
			return finish(darray.Elem)
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			return finish(view.Elem)
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			return finish(view.Elem)
		}
		if tuple, ok := StripAggregateStateType(ref.Elem).(*TupleType); ok {
			if fieldType, ok := a.tupleIndexResultType(tuple, expr.Index); ok {
				return finish(fieldType)
			}
			return finish(invalidType)
		}
		if itemType, ok := ChunksExactViewItemType(ref.Elem); ok {
			return finish(itemType)
		}
		if storeType, ok := ref.Elem.(*PackedEnumStoreType); ok && storeType.Enum != nil {
			return finish(storeType.Enum)
		}
		if _, ok := ref.Elem.(*CStrType); ok {
			return finish(a.namedTypes["char"])
		}
		if isStringViewType(ref.Elem) {
			return finish(a.namedTypes["char"])
		}
		return finish(ref.Elem)
	}
	a.errorf(expr.Pos(), "indexing requires string, array, view, packed store, or reference type, got %s", objType)
	return finish(invalidType)
}

// analyzeGetExpr type-checks the `get` prefix: the optional analog of `try`.
//
//   - `get arr[i]`  — a bounds-checked container access. The bounds obligation is
//     discharged by the check `get` itself performs, so it never demands
//     Unsafe.UncheckedIndex; the absence path is the recovery (or propagation).
//   - `get opt`     — unwraps an Optional / nullable reference.
//
// With a trailing `else` the recovery supplies a fallback value / return / raise.
// Without `else`, `get` propagates absence by early-returning the enclosing
// function's None — which therefore must return an optional or nullable ref.
func (a *Analyzer) analyzeGetExpr(n *ast.GetExpr) Type {
	if n == nil {
		return invalidType
	}
	// Checked-index form. When the inner index already carries a value fallback
	// (the postfix parser grabbed `else <value>`), the access is total on its own;
	// otherwise the `get` itself is the bounds check, so pre-mark it proven so it
	// does not also demand an Unsafe.UncheckedIndex grant.
	idx, isIndex := n.Value.(*ast.IndexExpr)
	indexAlreadyTotal := isIndex && idx.Fallback != nil
	if isIndex {
		// The explicit `get` head owns this index's recovery, so it must not get
		// the bare-`else` deprecation warning.
		a.getWrappedIndexExprs[idx] = true
		if idx.Fallback == nil {
			a.recordIndexBoundsProof(idx)
		}
	}
	// `get arr[a:b]` is a bounds-checked slice: the `get` performs the runtime
	// bounds test, yielding the bounded view on success and taking the recovery /
	// propagation path when the bounds fall outside the source.
	slice, isSlice := n.Value.(*ast.SliceExpr)
	if isSlice {
		a.checkedSliceExprs[slice] = true
	}

	valueType := a.analyzeExpr(n.Value)
	if IsInvalidType(valueType) {
		return invalidType
	}

	if indexAlreadyTotal {
		// `get arr[i] else <value>`: the index fallback supplies the absence path,
		// so the access is total and there is nothing left for `get` to propagate.
		return valueType
	}

	var resultType Type
	switch {
	case isIndex && idx.AsBuiltinDictGet != nil:
		if refType, ok := valueType.(*RefType); ok && refType.State == RefStateNullable {
			resultType = refType.Elem
		} else {
			resultType = valueType
		}
	case isIndex:
		// The element type produced by analyzeIndexExpr is the unwrapped result;
		// `get` performs the bounds check, so the access is total.
		resultType = valueType
	case isSlice:
		// The bounded view is the result; `get` performs the runtime bounds
		// check, taking the recovery / propagation path when out of range.
		resultType = valueType
	case isOptionalValueType(valueType):
		resultType = optionalValueInner(valueType)
	default:
		if refType, ok := valueType.(*RefType); ok && refType.State == RefStateNullable {
			resultType = cloneRefTypeWithState(refType, RefStateNonNull)
			break
		}
		if !IsInvalidType(valueType) {
			a.errorf(n.Pos(), "get requires an optional, a nullable reference, or a container index, got %s", valueType)
		}
		return invalidType
	}

	recovery := recoveryClauseForExpr(n.Recovery, n.Fallback, n.Position)
	if recovery == nil {
		// Propagation form: absence early-returns the enclosing function's None.
		if !a.currentFuncReturnAcceptsAbsence() {
			a.errorf(n.Pos(), "get without else requires the enclosing function to return an optional or nullable reference")
		}
		return resultType
	}
	fallbackType := a.analyzeRecoveryClause(recovery, resultType, nil, "get fallback")
	if !IsNeverType(fallbackType) && !AssignableTo(resultType, fallbackType) {
		a.errorf(n.Pos(), "get fallback expects %s, got %s", resultType, fallbackType)
		a.reportShapeMismatchNotes(n.Pos(), resultType, fallbackType)
	}
	return resultType
}

func isOptionalValueType(t Type) bool {
	_, ok := t.(*OptionalType)
	return ok
}

func optionalValueInner(t Type) Type {
	if opt, ok := t.(*OptionalType); ok && opt != nil {
		return opt.Value
	}
	return t
}

// currentFuncReturnAcceptsAbsence reports whether the enclosing function's return
// type can carry a None — i.e. an Optional or a nullable reference — which is the
// requirement for the propagating (`else`-less) form of `get`.
func (a *Analyzer) currentFuncReturnAcceptsAbsence() bool {
	if a == nil || a.currentFuncType == nil {
		return false
	}
	switch ret := a.currentFuncType.Return.(type) {
	case *OptionalType:
		return true
	case *RefType:
		return ret != nil && ret.State == RefStateNullable
	}
	return false
}
