//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"strconv"
)

// emitGetExpr lowers the `get` prefix (the optional analog of `try`). Three
// shapes:
//   - `get arr[i] else <value>`: the value fallback was parsed onto the inner
//     IndexExpr, which is already a complete checked access — emit it directly.
//   - `get arr[i]` / `get arr[i] else return|raise`: a bounds-checked access
//     whose absence path runs the recovery (or, with no else, returns None).
//   - `get opt` / `get opt else ...`: unwrap an optional / nullable reference,
//     with the recovery (or None-propagation) on the absent path.
func (s *functionState) emitGetExpr(expr *ast.GetExpr) (llvmIRValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	if idx, ok := expr.Value.(*ast.IndexExpr); ok && idx.Fallback != nil {
		return s.emitExpr(expr.Value, resultType)
	}
	// Resolve the recovery: an explicit `else`, or the propagation default which
	// early-returns the enclosing function's None.
	recovery := astRecoveryClauseForExpr(expr.Recovery, expr.Fallback, expr.Position)
	if recovery == nil {
		recovery = &ast.RecoveryClause{Position: expr.Position, Kind: ast.RecoveryReturn, Value: &ast.NullLit{Position: expr.Position}}
	}
	if idx, ok := expr.Value.(*ast.IndexExpr); ok {
		if idx.AsBuiltinDictGet != nil {
			return s.emitGetDictIndexExpr(idx, recovery, resultType)
		}
		return s.emitGetCheckedIndexExpr(idx, recovery, resultType)
	}
	if slice, ok := expr.Value.(*ast.SliceExpr); ok {
		return s.emitGetCheckedSliceExpr(slice, recovery, resultType)
	}
	return s.emitGetUnwrapExpr(expr.Value, recovery, resultType)
}

func (s *functionState) emitGetDictIndexExpr(idx *ast.IndexExpr, recovery *ast.RecoveryClause, resultType semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	valueType := s.exprType(idx.AsBuiltinDictGet)
	if valueType == nil {
		dictType, _, ok := builtinDictReceiverType(s.exprType(idx.Object))
		if !ok || dictType == nil {
			return nil, nil, fmt.Errorf("dict index get missing lookup result type")
		}
		mutable := false
		if call := idx.AsBuiltinDictGet; call != nil {
			if ident, ok := call.Func.(*ast.Ident); ok && ident != nil {
				mutable = ident.Name == "arena_dict_get_mut" || ident.Name == "arena_dict_get_cstr_view_mut"
			}
		}
		valueType = builtinDictEntryValueRefType(dictType, mutable)
	}
	refType, ok := valueType.(*semantic.RefType)
	if !ok || refType == nil || refType.State != semantic.RefStateNullable {
		return nil, nil, fmt.Errorf("dict index get requires nullable ref lookup result")
	}
	value, _, err := s.emitExpr(idx.AsBuiltinDictGet, valueType)
	if err != nil {
		return nil, nil, err
	}
	llvmRefType, err := s.g.lowerType(valueType)
	if err != nil {
		return nil, nil, err
	}
	nullValue := llvmConstNull(llvmRefType)
	okCond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), value, nullValue, cStringFree("get.dict.nonnull"))
	return s.emitGetBranch(okCond, func() (llvmIRValueRef, error) {
		loaded, err := s.loadValue(value, refType.Elem, "get.dict.value")
		if err != nil {
			return nil, err
		}
		if !semantic.SameType(refType.Elem, resultType) && !isVoidType(resultType) {
			return s.coerceValue(loaded, refType.Elem, resultType)
		}
		return loaded, nil
	}, recovery, resultType)
}

// emitGetCheckedSliceExpr emits a bounds-checked slice: when [start, end) is
// within the source (0 <= start <= end <= len) it produces the bounded view;
// otherwise it runs the recovery clause. The bounds are usize (unsigned), so the
// non-negativity of start is free.
func (s *functionState) emitGetCheckedSliceExpr(slice *ast.SliceExpr, recovery *ast.RecoveryClause, resultType semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	usizeType := s.g.namedType("usize")
	startValue, _, err := s.emitExpr(slice.Start, usizeType)
	if err != nil {
		return nil, nil, err
	}
	endValue, _, err := s.emitExpr(slice.End, usizeType)
	if err != nil {
		return nil, nil, err
	}
	lenValue, err := s.emitSliceSourceLength(slice)
	if err != nil {
		return nil, nil, err
	}
	startLEEnd := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULE), startValue, endValue, cStringFree("get.slice.start_le_end"))
	endLELen := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULE), endValue, lenValue, cStringFree("get.slice.end_le_len"))
	okCond := llvmBuildAnd(s.builder, startLEEnd, endLELen, cStringFree("get.slice.in_range"))
	return s.emitGetBranch(okCond, func() (llvmIRValueRef, error) {
		view, _, err := s.emitSliceExpr(slice)
		return view, err
	}, recovery, resultType)
}

// emitSliceSourceLength computes the element count of a slice's source for the
// bounds check: a constant for fixed-size arrays, the runtime count field for
// dynamic containers (darray / view / darray-view).
func (s *functionState) emitSliceSourceLength(slice *ast.SliceExpr) (llvmIRValueRef, error) {
	usizeType := s.g.namedType("usize")
	usizeLLVM, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	objType := s.exprType(slice.Object)
	base := objType
	if ref, ok := base.(*semantic.RefType); ok && ref != nil {
		base = ref.Elem
	}
	if arr, ok := base.(*semantic.ArrayType); ok && arr != nil && arr.HasConstSize {
		return llvmConstInt(usizeLLVM, llvmIRUnsignedLongLong(arr.ConstSize), 0), nil
	}
	switch base.(type) {
	case *semantic.DArrayType, *semantic.ViewType:
		containerPtr, _, err := s.emitAddressOrTemp(slice.Object)
		if err != nil {
			return nil, err
		}
		return s.emitContainerCountValue(containerPtr, base, "get.slice.count")
	default:
		return nil, fmt.Errorf("bounds-checked slice is not supported for %s", objType.String())
	}
}

// emitGetCheckedIndexExpr emits a bounds-checked container access: in-range
// produces the element, out-of-range runs the recovery clause.
func (s *functionState) emitGetCheckedIndexExpr(idx *ast.IndexExpr, recovery *ast.RecoveryClause, resultType semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	usizeType := s.g.namedType("usize")
	indexValue, _, err := s.emitExpr(idx.Index, usizeType)
	if err != nil {
		return nil, nil, err
	}
	countValue, loadValue, err := s.prepareSafeIndexFallback(idx, indexValue)
	if err != nil {
		return nil, nil, err
	}
	okCond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, countValue, cStringFree("get.index.in.range"))
	return s.emitGetBranch(okCond, func() (llvmIRValueRef, error) {
		value, actualType, err := loadValue()
		if err != nil {
			return nil, err
		}
		if !semantic.SameType(actualType, resultType) && !isVoidType(resultType) {
			return s.coerceValue(value, actualType, resultType)
		}
		return value, nil
	}, recovery, resultType)
}

// emitGetUnwrapExpr unwraps an optional / nullable reference, running the
// recovery clause on the absent path.
func (s *functionState) emitGetUnwrapExpr(valueExpr ast.Expr, recovery *ast.RecoveryClause, resultType semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	valueType := s.exprType(valueExpr)
	value, _, err := s.emitExpr(valueExpr, valueType)
	if err != nil {
		return nil, nil, err
	}
	var okCond, okValue llvmIRValueRef
	switch t := valueType.(type) {
	case *semantic.RefType:
		llvmRefType, err := s.g.lowerType(valueType)
		if err != nil {
			return nil, nil, err
		}
		nullValue := llvmConstNull(llvmRefType)
		okCond = llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), value, nullValue, cStringFree("get.nonnull"))
		okValue = value
	case *semantic.OptionalType:
		okCond, err = s.extractOptionalPresent(value, t)
		if err != nil {
			return nil, nil, err
		}
		if !isVoidType(resultType) {
			okValue, err = s.extractOptionalPayload(value, t)
			if err != nil {
				return nil, nil, err
			}
		}
	default:
		return nil, nil, fmt.Errorf("get requires an optional value or nullable ref")
	}
	return s.emitGetBranch(okCond, func() (llvmIRValueRef, error) { return okValue, nil }, recovery, resultType)
}

// emitGetBranch is the shared branch/merge skeleton for `get`: when okCond holds
// it takes okValue (computed lazily so the in-range index load only happens on
// the taken path); otherwise it runs the recovery clause, which may fall through
// (value/void) or terminate (return/raise).
func (s *functionState) emitGetBranch(okCond llvmIRValueRef, okValue func() (llvmIRValueRef, error), recovery *ast.RecoveryClause, resultType semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	okBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("get.ok"))
	fallbackBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("get.fallback"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("get.merge"))
	llvmBuildCondBr(s.builder, okCond, okBB, fallbackBB)

	incomingValues := make([]llvmIRValueRef, 0, 2)
	incomingBlocks := make([]llvmIRBlockRef, 0, 2)

	llvmPositionBuilderAtEnd(s.builder, okBB)
	okVal, err := okValue()
	if err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		okEnd := llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, okVal)
		}
		incomingBlocks = append(incomingBlocks, okEnd)
	}

	llvmPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, reachable, err := s.emitRecoveryClause(recovery, resultType, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if reachable && !s.currentBlockTerminated() {
		fallbackEnd := llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, fallbackValue)
		}
		incomingBlocks = append(incomingBlocks, fallbackEnd)
	}

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingBlocks) == 0 {
		llvmBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 {
		return incomingValues[0], resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := llvmBuildPhi(s.builder, phiType, cStringFree("getphi"))
	llvmAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), llvmIRUnsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitIntLiteral(expr *ast.IntLit) (llvmIRValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.namedType("int")
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := strconv.ParseUint(expr.Value, 0, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse integer literal %q: %w", expr.Value, err)
	}
	return llvmConstInt(llvmType, llvmIRUnsignedLongLong(parsed), 0), t, nil
}
func (s *functionState) emitFloatLiteral(expr *ast.FloatLit) (llvmIRValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.namedType("f64")
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := strconv.ParseFloat(expr.Value, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse float literal %q: %w", expr.Value, err)
	}
	return llvmConstReal(llvmType, llvmIRDouble(parsed)), t, nil
}
func (s *functionState) emitStringLiteral(expr *ast.StringLit, expected semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	value := s.emitGlobalStringBytes(expr.Value, "str")
	resultType := s.exprType(expr)
	if resultType == nil {
		// Synthetic literal with no analyzed type (e.g. a backend-built contract-failure
		// message): a global string pointer is exactly a `u8&`, which is what the panic
		// message coercer accepts. Without this it falls back to the default "panic" text.
		resultType = &semantic.RefType{Elem: s.g.namedType("u8")}
	}
	if expected != nil && isStringViewCarrierType(expected) {
		resultType = expected
	}
	// isStringViewCarrierType also accepts `sview&` (a ref wrapping a view), e.g.
	// when the expected type comes from assigning a literal through a
	// `mutable sview&` out-parameter. The literal itself always materializes the
	// two-field view VALUE; unwrap the ref so we lower {ptr,i64} rather than ptr
	// (which produced an invalid `insertvalue ptr undef, ...`).
	if ref, ok := resultType.(*semantic.RefType); ok && isStringViewCarrierType(resultType) {
		resultType = ref.Elem
	}
	if isStringViewCarrierType(resultType) {
		viewLLVMType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		i64Type, err := s.g.lowerType(s.g.namedType("i64"))
		if err != nil {
			return nil, nil, err
		}
		viewValue := llvmGetUndef(viewLLVMType)
		viewValue = llvmBuildInsertValue(s.builder, viewValue, value, 0, cStringFree("str.view.data"))
		viewValue = llvmBuildInsertValue(s.builder, viewValue, llvmConstInt(i64Type, llvmIRUnsignedLongLong(len(expr.Value)), 0), 1, cStringFree("str.view.len"))
		return viewValue, resultType, nil
	}
	return value, resultType, nil
}
func (s *functionState) emitCharLiteral(expr *ast.CharLit) (llvmIRValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.namedType("char")
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, ok := semantic.ParseCharLiteral(expr)
	if !ok {
		return nil, nil, fmt.Errorf("failed to parse char literal %q", expr.Value)
	}
	return llvmConstInt(llvmType, llvmIRUnsignedLongLong(parsed), 0), t, nil
}
